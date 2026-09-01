# Design: Rate limiting и контракты state в форке bitbucket-провайдера

Дата: 2026-09-01
Ветка: `feature/noogadev-fixes`
Предшествует: [ревью round 2](../reviews/2026-09-01-deep-review-round2.md)

## Задача

Первая итерация форка закрыла симптомы (429, unmarshal-краш), но:

- реактивный retry на `go-retryablehttp` даёт в среднем **14м17с** сна на запрос и молчит;
- `flattenBranchRestrictionUsers` вносит вечный diff, которого в upstream не было;
- `Client.Do` паникует на транспортной ошибке;
- продакшен-путь retry покрыт тестами на **0%**, а покрытые тесты проверяют мёртвый код.

Цель — заменить угадывание бэкоффа на использование сигнала, который Bitbucket
реально присылает, и привести контракты state в соответствие с API.

## Измеренное поведение Bitbucket Cloud

Снято живьём (60 неаутентифицированных запросов к `api.bitbucket.org` подряд):

```
HTTP/2 429
content-type: text/plain
x-ratelimit-limit: 60, 60;w=3600
x-ratelimit-remaining: 8
x-ratelimit-reset: 156

Rate limit for this resource has been exceeded
```

Выводы, на которых стоит весь дизайн:

1. **`Retry-After` отсутствует.** `retryablehttp.RateLimitLinearJitterBackoff` была
   выбрана именно ради него — против Bitbucket она всегда сваливается в слепой
   линейный джиттер.
2. **`X-RateLimit-Reset` — delta-секунды до сброса окна.** Это точное время
   ожидания; угадывать не нужно.
3. **`X-RateLimit-Remaining` при 429 равен 8**, то есть у Bitbucket несколько
   перекрывающихся бакетов и `remaining` как сигнал ненадёжен. Опираемся только
   на `reset`.
4. Тело — `text/plain`, не JSON. Существующий fallback в `Client.Do`
   (`apiError.APIError.Message = string(body)`) это уже обрабатывает.

## Архитектура

### `bitbucket/ratelimit.go` (новый)

Заменяет `bitbucket/retry_transport.go` и зависимость `go-retryablehttp`.
Две сущности, каждая с одной ответственностью.

#### `rateLimitGate`

Общий на весь провайдер шлагбаум. Хранит момент, до которого окно закрыто.

```go
type rateLimitGate struct {
    mu    sync.Mutex
    until time.Time

    now   func() time.Time                    // инъекция для тестов
    after func(time.Duration) <-chan time.Time
}

func (g *rateLimitGate) closeUntil(d time.Duration)  // продлевает, но не сокращает
func (g *rateLimitGate) wait(ctx context.Context) error
```

`wait` — цикл, а не одиночный `sleep`: пока горутина спит, другая может продлить
дедлайн, и после пробуждения нужно перепроверить. `ctx.Done()` прерывает ожидание.

Смысл: при `-parallelism=10` первый запрос, поймавший 429, платит за всех.
Остальные девять не тратят попытку на заведомый 429 и не создают thundering herd
в момент открытия окна.

#### `rateLimitTransport`

```go
type rateLimitTransport struct {
    base        http.RoundTripper
    gate        *rateLimitGate
    maxAttempts int           // всего попыток, включая первую
    maxWait     time.Duration // максимум, который согласны ждать
    jitter      func(time.Duration) time.Duration
}
```

`RoundTrip`:

1. `gate.wait(ctx)` — если окно закрыто, ждём.
2. Перемотать тело через `req.GetBody` (проверено: и `Client.Do`, и swagger
   `prepareRequest` передают `*bytes.Buffer`, поэтому `http.NewRequest`
   проставляет `GetBody`). Если `GetBody == nil`, а тело есть — повтор невозможен,
   отдаём ответ как есть.
3. `base.RoundTrip(req)`. Транспортная ошибка или статус ≠ 429 — возвращаем сразу.
4. Попытки исчерпаны — возвращаем **429-ответ, а не ошибку**: диагностику построит
   вызывающий слой, у которого есть endpoint.
5. `resetDelay(resp.Header)` → если больше `maxWait`, возвращаем 429 сразу,
   а не висим молча.
6. Иначе: слить и закрыть тело 429, `gate.closeUntil(d + jitter)`, следующая попытка.

Константы: `maxAttempts = 3`, `maxWait = 120 * time.Second`. Намеренно **не**
настройки: правильный рычаг для пользователя — `-parallelism`, и об этом говорит
текст ошибки.

#### `resetDelay`

```go
func resetDelay(h http.Header, now time.Time) (time.Duration, bool)
```

Порядок источников:

1. `X-RateLimit-Reset` — целое. Значение `> 1e9` трактуем как unix-timestamp,
   иначе как delta-секунды. Страховка на случай смены формата, стоит одну строку.
2. `Retry-After` — целое секунд или HTTP-date. Bitbucket его не шлёт, но прокси
   перед API может.
3. Ничего нет — `false`; вызывающий берёт экспоненциальный fallback
   `baseDelay << (attempt-1)`, ограниченный `maxWait`.

Отрицательные и нечисловые значения игнорируются.

### Диагностика 429

Когда попытки исчерпаны, 429 доходит до `Client.Do` и `handleClientError`.
Общий помощник добавляет к сообщению факты из заголовков:

```
API Error: 429 2.0/repositories/noogadev/foo: rate limit exceeded,
resets in 156s (limit 60 per 3600s).
Reduce -parallelism or re-run after the window resets.
```

### Контракт `users` в `bitbucket_branch_restriction`

Выбран **display_name**, как в upstream PR #252 — будущий rebase пройдёт без
конфликтов. Два отличия в лучшую сторону:

- **`workspaceMemberCache`** — указатель в `Clients`, индекс `display_name → UUID`
  строится один раз на воркспейс за весь план. Без кэша выбранный контракт сам
  провоцирует 429, который мы только что чиним: upstream #252 обходит все страницы
  мемберов на **каждый** create/update.
- **Дубликаты display_name — явная ошибка.** В #252 `memberUUIDs[name] = uuid`
  молча оставляет последнего совпавшего; при неуникальных именах это тихо
  применяет restriction не к тому человеку.

Первая загрузка на воркспейс держит мьютекс, поэтому параллельные ресурсы не
дублируют обход страниц.

Read пишет `DisplayName`; если API вернул пустое имя — `Uuid`, чтобы в state не
попала пустая строка.

### Контракт `groups.owner`

Схема ждёт slug воркспейса. Источники по убыванию надёжности:
`g.Workspace.Slug` → префикс `g.FullSlug` до `:` → `g.Owner.Username`.

Замечание для CHANGELOG: upstream `d.Set("groups", brRes.Groups)` падал с
`Invalid address to set: groups.0.Workspace` **и обнулял set**. Значит в
существующих state сейчас `groups = []`, и первый refresh после фикса покажет
diff. Это ожидаемо.

### `FlexBool`

Форма `type FlexBool bool` сохраняется — именованный bool сериализуется в JSON
нативно, `MarshalJSON` не нужен, а `*FlexBool` + `omitempty` ведёт себя как
`*bool`. У upstream-структуры `struct{Value *bool}` без `MarshalJSON` получилось бы
`{"Value":true}`.

Меняется только разбор: вместо ручной срезки кавычек — `json.Unmarshal` в `bool`,
затем в `string`, затем в `json.Number`. Числа явно сужены до `0/1`.

### Прочие фиксы

| Что | Где |
|---|---|
| nil-deref после `HTTPClient.Do` | `client.go` |
| `xReq, _ := client.Get(...)` + разыменование | `resource_branching_model.go`, `resource_project_branching_model.go`, `resource_group.go`, `resource_group_membership.go`, `data_group.go`, `data_group_members.go`, `data_groups.go` |
| Ошибки `d.Set` → `diag.FromErr` | только тронутые Read-функции |
| Пагинация `GetDeploymentVariables` (#254) | `resource_deployment_variable.go`, идиома из `resource_default_reviewers.go` |
| `gofmt`, `go mod tidy`, `.gitignore` для `tofurc`/`bin/` | корень |

## Тестирование

Инъектируемые часы (`now`/`after`) плюс `httptest` — весь набор проходит за доли
секунды, без реального сна.

**Rate limiting**
1. 429 → 200 с уважением `X-RateLimit-Reset`.
2. Попытки исчерпаны → возвращается 429-ответ, а не `error`.
3. `resetDelay > maxWait` → 429 сразу, без ожидания.
4. Общий шлагбаум: 10 параллельных горутин делают ровно одну лишнюю попытку.
5. Отмена контекста прерывает ожидание.
6. POST переигрывает тело; сервер видит одинаковое тело на обеих попытках.
7. `resetDelay` таблицей: delta, epoch, `Retry-After` секунды, `Retry-After`
   HTTP-date, отрицательное, мусор, отсутствие заголовков.

**Регрессии на паники**
8. `Client.Do` на закрытый порт → ошибка, не паника.
9. Транспорт, возвращающий `(nil, err)` → ошибка, не паника.

**Контракты state**
10. `flattenBranchRestrictionUsers`: display_name, откат на UUID.
11. `flattenBranchRestrictionGroups`: `Workspace.Slug`, `FullSlug`, `Owner.Username`.
12. Резолв display_name → UUID: пагинация, кэш (второй вызов не ходит в сеть),
    ненайденное имя, дубликаты.
13. Пагинация deployment variables: переменная со второй страницы находится.

**FlexBool**
14. Escaped string, отсутствующее поле, marshal round-trip, число вне `0/1`.

## Что осознанно не делается

- Упреждающий token-bucket limiter. Лимиты Bitbucket зависят от ресурса
  (1000–10000/час), одним бакетом их не описать; реактивный шлагбаум по факту
  429 точнее и проще.
- Миграция на `terraform-plugin-testing` и Plugin Framework. Большая отдельная
  работа, зафиксирована как долг.
- Массовая проверка всех 245 вызовов `d.Set` в репозитории.
- `bitbucket_group` на снятом v1 API (upstream #212/#235) — только nil-deref.
