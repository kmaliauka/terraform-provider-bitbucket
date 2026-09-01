# Deep Review (round 2): Bitbucket Provider Fork

Дата: 2026-09-01
Ветка: `feature/noogadev-fixes` (8 коммитов поверх `origin/master` @ `a18e0f13`, v2.52.0)
Область: Go-код провайдера, retry-слой, контракты state, upstream-контекст, версии зависимостей.

## Что проверено фактически

| Проверка | Команда | Результат |
|---|---|---|
| Сборка + тесты | `go test -race -count=1 ./...` | **PASS** (5.1s) |
| Vet | `go vet ./...` | **PASS** |
| Формат | `gofmt -l ./bitbucket` | **FAIL** — `bitbucket/resource_branching_model_test.go` |
| Модули | `go mod tidy -diff` | **FAIL** — `go-retryablehttp` помечен `// indirect`, но импортируется напрямую |
| Покрытие пакета | `go tool cover -func` | 5.2% суммарно; `newHTTPClient` — **0.0%**, мёртвый `RetryTransport` — 63–69% |
| Линтер | `golangci-lint run` | не установлен локально; в CI `only-new-issues: true` |

---

## P0 — блокеры

### P0-1. Nil-dereference panic в `Client.Do` (соответствует upstream issue #211)

[bitbucket/client.go:86-89](bitbucket/client.go#L86-L89):

```go
resp, err := c.HTTPClient.Do(req)
log.Printf("[DEBUG] Resp: %v Err: %v", resp, err)
if resp.StatusCode >= 400 || resp.StatusCode < 200 {   // resp == nil при transport error
```

`err` не проверяется вообще. При DNS/TLS/connection-reset/`context canceled` `http.Client.Do` возвращает `(nil, err)` → паника внутри провайдера, Terraform падает с "plugin crashed".

Почему это стало важнее именно сейчас: до фикса `Client.HTTPClient` был `&http.Client{}`; теперь это `retryablehttp.StandardClient()`, а `CheckRetry` в [provider.go:125-130](bitbucket/provider.go#L125-L130) при `err != nil` возвращает `false, err` — то есть транспортные ошибки **не** ретраятся и попадают в тот же nil-путь. Плюс `req.Close = true` заставляет открывать новое TCP+TLS-соединение на каждый запрос, что повышает вероятность транспортной ошибки под нагрузкой.

Патч:

```go
resp, err := c.HTTPClient.Do(req)
log.Printf("[DEBUG] Resp: %v Err: %v", resp, err)
if err != nil {
	return nil, err
}
if resp == nil {
	return nil, fmt.Errorf("no response from %s %s", method, absoluteendpoint)
}
if resp.StatusCode >= 400 || resp.StatusCode < 200 {
	...
}
```

Тот же класс ошибки — 7 мест, где ответ `client.Get` разыменовывается с явно выброшенной ошибкой:

```
bitbucket/resource_branching_model.go:192
bitbucket/resource_project_branching_model.go:163
bitbucket/resource_group.go:107
bitbucket/resource_group_membership.go:79
bitbucket/data_group.go:53
bitbucket/data_group_members.go:64
bitbucket/data_groups.go:60
```

Все они выглядят как `xReq, _ := client.Get(...)` и сразу читают `xReq.StatusCode`. После фикса `Client.Do` они всё ещё падают, потому что игнорируют `err`. Минимум — исправить два branching-model-файла (они в blast radius этой ветки).

### P0-2. `users` в branch restriction — гарантированный вечный diff

Это **регрессия, внесённая веткой**, а не наследство upstream. Проверено эмпирически (`schema.TestResourceDataRaw` + `d.Set`):

```
upstream:  d.Set("users", []bitbucket.Account{...})
           → error: users.0: '' expected type 'string', got unconvertible type 'bitbucket.Account'
           → ошибка игнорируется, state остаётся равным конфигу → diff'а нет
```

То есть upstream «работал» случайно: `d.Set` молча падал и не трогал state. Теперь `flattenBranchRestrictionUsers` действительно пишет значение, и контракт разъезжается:

- запись ([resource_branch_restriction.go:139-146](bitbucket/resource_branch_restriction.go#L139-L146)) отправляет `bitbucket.Account{Username: <значение из конфига>}`;
- чтение ([resource_branch_restriction.go:236](bitbucket/resource_branch_restriction.go#L236)) предпочитает `acc.Uuid`;
- документация ([docs/resources/branch_restriction.md:28](docs/resources/branch_restriction.md#L28)) обещает `users = [ "my-bitbucket-username" ]`;
- в `bitbucket-go-client v0.11.0` поле помечено `// The user's Bitbucket username. Deprecated by Bitbucket` — Bitbucket Cloud его в ответах не отдаёт (GDPR-удаление), значит ветка `acc.Username` в flatten практически недостижима и в state всегда попадёт UUID.

Итог: конфиг `users = ["ivan"]` → state `users = ["{uuid}"]` → diff на каждом plan, навсегда.

Нужен один контракт. Upstream PR #252 уже выбрал **display_name**: резолвит его в UUID через постраничный обход `WorkspacesWorkspaceMembersGet` на create/update и пишет обратно `display_name` на read. Варианты:

| Вариант | Плюсы | Минусы |
|---|---|---|
| **UUID-only** | без лишних API-вызовов, стабильный идентификатор | ломает существующие конфиги; нужна `ValidateFunc` на формат `{...}` и запись в CHANGELOG |
| **display_name** (как upstream #252) | совместимо с будущим rebase; читаемо | +N запросов к API на каждый create/update (усугубляет 429, ради чего и делался retry); display_name не уникален |
| **Оба, с нормализацией** | не ломает никого | нужна state-миграция и `DiffSuppressFunc` |

Пока решение не принято, эту часть лучше не мержить: она меняет поведение в худшую сторону по сравнению с upstream.

### P0-3. `groups.owner` во flatten берёт не то поле

[resource_branch_restriction.go:250-267](bitbucket/resource_branch_restriction.go#L250-L267) читает `g.Owner.Username` → `g.Owner.DisplayName`. Но в схеме `groups.owner` — это **slug воркспейса** (`owner = "noogadev"`), а `Owner` в ответе API это `Account` без поля `Slug`, у которого `DisplayName` будет человекочитаемым именем воркспейса («Nooga Dev»). Итого — снова вечный diff.

В `bitbucket.Group` есть корректные источники:

```go
Workspace *Workspace  // .Slug — то, что нужно
FullSlug  string      // "acme:developers" — префикс до ':' тоже даёт slug воркспейса
```

Патч:

```go
owner := ""
switch {
case g.Workspace != nil && g.Workspace.Slug != "":
	owner = g.Workspace.Slug
case g.FullSlug != "":
	owner, _, _ = strings.Cut(g.FullSlug, ":")
case g.Owner != nil && g.Owner.Username != "":
	owner = g.Owner.Username
}
```

Отдельно: upstream `d.Set("groups", brRes.Groups)` падал с `Invalid address to set: []string{"groups","0","Workspace"}` **и при этом обнулял set** (проверено). Значит у всех, кто уже применял провайдер, в state сейчас `groups = []`, и первый refresh после фикса покажет diff. Это ожидаемо и правильно — но стоит написать в CHANGELOG.

---

## P1 — серьёзные

### P1-1. Retry-бэкофф даёт до 22 минут сна на один запрос

`newHTTPClient` оставляет `RetryWaitMin`/`RetryWaitMax` дефолтными (1s / 30s) и ставит `RetryMax = 10` с `RateLimitLinearJitterBackoff`. Эта функция — **линейная** с джиттером: `rand[min,max) * (attempt+1)`, а не экспоненциальная (в `docs/superpowers/agents.md` написано «exponential backoff» — неверно).

Смоделировано на реальной `retryablehttp.LinearJitterBackoff` (2000 прогонов):

```
min=1s  max=30s retries=10 -> средний суммарный сон 14m17s, худший наблюдённый 21m46s
min=1s  max=5s  retries=6  -> средний суммарный сон  1m02s, худший наблюдённый  1m28s
```

При постоянном 429 Terraform молча висит ~15 минут **на каждом ресурсе**, без вывода (`client.Logger = nil` глушит и логи ретраев). Это ровно тот сценарий, ради которого фикс делался, и он делает поведение хуже, чем `-parallelism=1`.

Плюс: `RetryMax = 10` — это 1 initial + 10 ретраев = **11 запросов**, а не 10, как заявлено в `agents.md`.

Рекомендация:

```go
client.RetryWaitMin = 1 * time.Second
client.RetryWaitMax = 5 * time.Second
client.RetryMax = 6
client.Logger = ...  // или RequestLogHook с log.Printf("[INFO] ...") — иначе ретраи невидимы в TF_LOG
```

`RateLimitLinearJitterBackoff` стоит оставить: она честно уважает `Retry-After`, который Bitbucket на 429 присылает.

### P1-2. `CheckRetry` слишком узкий

```go
client.CheckRetry = func(ctx context.Context, response *http.Response, err error) (bool, error) {
	if err != nil {
		return false, err
	}
	return response != nil && response.StatusCode == http.StatusTooManyRequests, nil
}
```

Не ретраятся: транспортные ошибки (EOF, connection reset — Bitbucket их отдаёт под нагрузкой), 500/502/**503**/504. Причём выбранный `RateLimitLinearJitterBackoff` явно умеет обрабатывать `Retry-After` для 503 — и этот код никогда не отработает.

Идиоматичнее — надстроить дефолтную политику, а не заменять её:

```go
client.CheckRetry = func(ctx context.Context, resp *http.Response, err error) (bool, error) {
	if resp != nil && resp.StatusCode == http.StatusTooManyRequests {
		return true, nil
	}
	return retryablehttp.DefaultRetryPolicy(ctx, resp, err)
}
```

`DefaultRetryPolicy` заодно корректно возвращает `ctx.Err()` и не ретраит невосстановимые ошибки (redirect/scheme/TLS-cert).

Замечу: `response != nil` — единственное отличие от upstream PR #255, и оно правильное (защитное; при `err == nil` `resp` не бывает nil, но стоить ничего не стоит).

### P1-3. Две реализации retry; тесты покрывают мёртвую

`bitbucket/retry_transport.go` (111 строк) + `bitbucket/retry_transport_test.go` (117 строк) не используются продакшен-кодом: единственные ссылки на `RetryTransport` — сам файл и его тест. Покрытие: `RoundTrip` 63%, `getDelay` 69%, а реальный `newHTTPClient` — **0%**.

То есть все 4 «зелёных» retry-теста из `agents.md` ничего не говорят о поведении провайдера.

Рекомендация: удалить `retry_transport.go` + его тест, а тесты переписать на продакшен-конструктор:

```go
func TestNewHTTPClientRetriesOn429(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&hits, 1) < 3 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	...
}
```

Для этого `newHTTPClient` стоит сделать параметризуемым (`newHTTPClient(retryMax int, waitMin, waitMax time.Duration)`), чтобы тесты не спали по 14 минут.

### P1-4. Гигиена сборки

- `gofmt -w bitbucket/resource_branching_model_test.go` — сейчас `make fmtcheck` (а значит и `make build`) падает. Заодно в этом файле импорт `encoding/json` стоит вне сгруппированного блока.
- `go mod tidy` — перенести `go-retryablehttp v0.7.8` в основной `require` (upstream PR #255 это делает).
- `tofurc` и `bin/` не в `.gitignore`. `tofurc` содержит абсолютный путь `/Users/kirillmalyavko/...` — в репозиторий такое коммитить не стоит.

### P1-5. Ветка дублирует три открытых upstream PR

| Задача в ветке | Upstream PR | Расхождение |
|---|---|---|
| retryablehttp в provider.go | **#255** (2026-08-12) | практически байт-в-байт, кроме `response != nil` |
| FlexBool | **#247** (2026-07-09) | у upstream `struct{Value *bool}` + `MarshalJSON` + `BoolPtr()`; здесь `type FlexBool bool` |
| flatten users | **#252** (2026-07-21) | upstream выбрал `display_name`, здесь UUID-first |

При будущем `git rebase origin/master` (после мержа любого из PR) будет конфликт в тех же строках. Стоит либо привести формы к upstream-виду, либо явно зафиксировать в spec, что это осознанный форк-дивергент.

Кстати, форма `type FlexBool bool` **лучше** upstream-варианта: именованный bool сериализуется в JSON нативно, поэтому `MarshalJSON` не нужен, а `*FlexBool` + `omitempty` работает так же, как `*bool`. У upstream-структуры без `MarshalJSON` был бы `{"Value": true}`.

---

## P2 — улучшения

### P2-1. `FlexBool.UnmarshalJSON` парсит JSON вручную

[flex_bool.go:39-53](bitbucket/flex_bool.go#L39-L53) срезает кавычки байтами. Это ломается на escape-последовательностях (`"true"`) и не отличает `"tr\"ue"` от валидного. Идиоматичнее — отдать разбор `encoding/json`:

```go
func (fb *FlexBool) UnmarshalJSON(data []byte) error {
	var b bool
	if err := json.Unmarshal(data, &b); err == nil {
		*fb = FlexBool(b)
		return nil
	}

	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		s = strings.TrimSpace(s)
		if s == "" {
			*fb = false
			return nil
		}
		parsed, err := strconv.ParseBool(s)
		if err != nil {
			return fmt.Errorf("cannot unmarshal %q into FlexBool: %w", s, err)
		}
		*fb = FlexBool(parsed)
		return nil
	}

	var n json.Number
	if err := json.Unmarshal(data, &n); err == nil {
		i, err := n.Int64()
		if err != nil || (i != 0 && i != 1) {
			return fmt.Errorf("cannot unmarshal number %s into FlexBool", n)
		}
		*fb = FlexBool(i == 1)
		return nil
	}

	return fmt.Errorf("cannot unmarshal %s into FlexBool", data)
}
```

Побочно это документирует, что числа сужены до `0/1` (сейчас `strconv.ParseBool` молча принимает и `"t"`, и `"T"`, а `2` — отвергает; поведение случайное, не выбранное).

Замечание про `null`: обработка ветки `null` внутри `UnmarshalJSON` в текущей схеме недостижима — при `*FlexBool` поле `encoding/json` обнуляет указатель, не вызывая метод. Тест `"null"` в `flex_bool_test.go` использует **не**-указатель `FlexBool`, поэтому ветка живая, но в продакшен-структуре она мертва. Это ок, просто стоит комментария.

### P2-2. Ошибки `d.Set` игнорируются

В репозитории 245 вызовов `d.Set` и **0** проверок ошибки. Переделывать все — вне области этой ветки, но именно там, где ветка меняет поведение (`users`, `groups`, `default_branch_deletion`), ошибка должна становиться диагностикой — иначе повторится ровно тот сценарий, что описан в P0-2: `d.Set` падает, никто не замечает, поведение «случайно правильное».

```go
if err := d.Set("users", flattenBranchRestrictionUsers(brRes.Users)); err != nil {
	return diag.FromErr(err)
}
```

### P2-3. `default_branch_deletion`: `nil` на `TypeBool`

[resource_branching_model.go:232](bitbucket/resource_branching_model.go#L232) — `d.Set("default_branch_deletion", nil)` на `schema.TypeBool` (Optional, без Computed) записывает `false`, а не «отсутствует». Плюс в `expandBranchingModel` используется `GetOkExists`, который на `TypeBool` со значением `false` ненадёжен (отсюда и `//nolint:staticcheck` в upstream). Совокупно: `default_branch_deletion = false` в конфиге может не отправляться в API. Наследие upstream, но раз FlexBool трогает именно это поле — стоит либо добавить `Computed: true`, либо оставить в state предыдущее значение при `nil`.

### P2-4. `req.Close = true` мешает пулу соединений

[client.go:84](bitbucket/client.go#L84) заставляет закрывать соединение после каждого запроса. `retryablehttp` под капотом использует `cleanhttp.DefaultPooledClient()`, весь смысл которого — переиспользование соединений. Новое TCP+TLS-рукопожатие на каждый вызов — это и латентность, и лишний повод для транспортных ошибок под нагрузкой (см. P0-1). Убирать надо аккуратно и отдельным коммитом, с проверкой на реальном плане.

### P2-5. Ретрай POST/PUT

Тело запроса корректно переигрывается: `retryablehttp.FromRequest` буферизует `r.Body` целиком, и `Do` перематывает его на каждой попытке (проверено по исходникам v0.7.8). Риск не в теле, а в семантике: если Bitbucket успел применить POST и только потом отдал 429, повтор создаст дубль. Практически Bitbucket отсекает по rate limit до обработки, но это допущение стоит записать в spec, а не оставлять неявным.

### P2-6. Мелочи по стилю Go

- `tc := tc` в table-driven тестах не нужен: `go.mod` объявляет `go 1.25.8`, а семантика переменной цикла изменилась в 1.22.
- `math/rand` + `rand.Int63n` в `retry_transport.go` — при удалении файла вопрос снимается; иначе `math/rand/v2`.
- Acceptance-тесты используют поле `Providers:`, которое в SDK v2.40.1 помечено `// Deprecated: Providers is deprecated, please use ProviderFactories`. Это касается всего репозитория, не только ветки.

---

## Тесты, которые стоит дописать

Продакшен-путь (`newHTTPClient`) сейчас не покрыт вообще. Приоритетный список:

1. `TestNewHTTPClientRetriesOn429` — `httptest`, 2×429 → 200, проверить число попыток и итоговый статус.
2. `TestNewHTTPClientHonorsRetryAfter` — заголовок `Retry-After: 1`, проверить, что пауза ≥ 1s и < линейного джиттера.
3. `TestNewHTTPClientExhaustsRetries` — всегда 429; проверить, что возвращается **response с 429, а не ошибка** (это контракт `PassthroughErrorHandler`, на него опирается `handleClientError`).
4. `TestNewHTTPClientReplaysBody` — POST с телом, 429 → 200; сервер проверяет, что тело пришло целиком в обеих попытках.
5. `TestNewHTTPClientContextCancellation` — отменённый контекст прерывает ожидание, а не спит весь бэкофф.
6. `TestClientDoTransportError` — регрессия на P0-1: `Client.Do` на закрытый порт возвращает ошибку, а не паникует.
7. `TestClientDoNilResponse` — `HTTPClient` с фейковым транспортом, возвращающим `(nil, err)`.
8. `TestFlexBoolEscapedString` / `TestFlexBoolMarshalRoundTrip` / `TestFlexBoolAbsentField` — добить дыры, оставшиеся после P2-1.
9. `TestFlattenBranchRestrictionGroupsFromWorkspace` — `Group{Workspace: &Workspace{Slug: "noogadev"}}` → `owner == "noogadev"` (регрессия на P0-3).
10. `TestFlattenBranchRestrictionUsersRoundTrip` — таблица «что в конфиге → что отправили → что вернул API → что в state», которая падает, если контракт из P0-2 нарушен.

Пункты 1–5 требуют вынести параметры retry в аргументы `newHTTPClient`, иначе тесты будут спать десятки секунд.

---

## Проверка версий

| Модуль | В `go.mod` | Актуальная (proxy.golang.org, 2026-09-01) | Статус |
|---|---|---|---|
| `github.com/hashicorp/go-retryablehttp` | v0.7.8 | **v0.7.8** (2025-06-18) | актуальна |
| `github.com/hashicorp/terraform-plugin-sdk/v2` | v2.40.1 | **v2.40.1** (2026-04-28) | актуальна |
| `github.com/DrFaust92/bitbucket-go-client` | v0.11.0 | **v0.11.0** (2026-07-20) | актуальна |
| `github.com/ProtonMail/go-crypto` | v1.4.1 | **v1.4.1** (2026-03-18) | актуальна |
| `github.com/hashicorp/terraform-plugin-testing` | — | v1.16.0 | не используется |

Поправка к предыдущему ревью: `go-retryablehttp v0.7.8` — это и есть последняя стабильная версия модуля, апгрейдить нечего.

Go: локально `go1.26.5`, в `go.mod` — `go 1.25.8`. CI (`.github/workflows/lint.yml`) использует `go-version-file: go.mod`, то есть собирает на 1.25.x. Расхождение нормальное (toolchain-совместимость вперёд), поднимать `go` в `go.mod` не нужно — это сузило бы совместимость. В `docs/superpowers/plans/` заявлено Go 1.26.5 — формулировку стоит поправить.

Отдельно: `terraform-plugin-testing` не подключён, хотя это рекомендованный HashiCorp путь для acceptance-тестов (`statecheck`, `plancheck`, `ImportStateKind`). Миграция большая и в область этой ветки не входит — фиксирую как известный долг.

---

## Upstream-контекст: что ещё стоит забрать в форк

Провайдер в maintenance mode (**#242**), мержа PR можно ждать долго — форк оправдан. Открытые issue, релевантные noogadev:

### Кандидат №1 — issue #254: `bitbucket_deployment_variable` теряет переменные (подтверждено в коде)

[resource_deployment_variable.go](bitbucket/resource_deployment_variable.go) в Read вызывает:

```go
rvRes, res, err := pipeApi.GetDeploymentVariables(c.AuthContext, workspace, repoSlug, deployment, nil)
```

Пагинации нет. Bitbucket отдаёт `pagelen=10`, дальше идёт `next`. Любая переменная за пределами первой страницы не находится в цикле поиска по `Uuid` → `d.SetId("")` → **Terraform считает её удалённой и пересоздаёт**. Это тихая потеря состояния, а не просто ошибка плана.

В репозитории уже есть готовая идиома пагинации — [resource_default_reviewers.go:~190](bitbucket/resource_default_reviewers.go) (`options.Page = optional.NewInt32(next)` в цикле по `.Next != ""`). Фикс механический и по ценности для продакшена сопоставим с retry-фиксом.

### Кандидат №2 — issue #227 + P0-2: единая стратегия идентификаторов

`bitbucket_repository_user_permission` требует UUID, `bitbucket_branch_restriction` документирует username, API отдаёт UUID. Одно решение (например, «принимаем UUID **или** display_name, в state пишем UUID, разница гасится `DiffSuppressFunc`») закрывает оба.

### Остальное

- **#211** — panic nil pointer → закрывается P0-1.
- **#224** — блок `development` в `bitbucket_project_branching_model` фактически required, а в docs Optional. Правка на одну строку в docs.
- **#212 / #235** — `bitbucket_group` ходит в снятый v1 API (`1.0/groups/...`) и получает 405. Ровно те же 4 файла, что и в списке nil-deref из P0-1. Если группы не используются — трогать не нужно; если используются, ресурс нерабочий.
- **#215 / #222** — пагинация default reviewers; в текущем master цикл уже есть, отдельно чинить нечего.
- **#217** — `bitbucket_deploy_key` не импортирует ключ.
- **#240** — формат ID при импорте deployments (docs).

---

## Порядок работ

1. **P0-1** — nil-deref в `client.go` + два branching-model-файла. Самостоятельный, безопасный, ценный сам по себе коммит.
2. **P1-4** — `gofmt`, `go mod tidy`, `.gitignore` для `tofurc`/`bin/`. Чинит красный `make build`.
3. **P1-1 + P1-2** — параметры бэкоффа и `CheckRetry` поверх `DefaultRetryPolicy`.
4. **P1-3** — удалить `retry_transport.go` + тест, написать тесты 1–5 на `newHTTPClient`.
5. **P0-3** — `groups.owner` из `Workspace.Slug`.
6. **P0-2** — решить контракт `users`. **До решения ветку в таком виде мержить не стоит.**
7. **P2-1, P2-2** — FlexBool через `encoding/json`, проверка ошибок `d.Set` в изменённых местах.
8. Отдельной веткой — пагинация deployment variables (#254).
9. Обновить `docs/superpowers/agents.md`: «exponential» → «linear jitter with Retry-After», «10 attempts» → «1 + up to 10 retries», убрать «100% test coverage» (фактически `Bool()` — 66.7%, продакшен-retry — 0%).
