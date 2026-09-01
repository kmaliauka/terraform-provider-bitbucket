# Review: Bitbucket Provider Fork Fixes

Дата: 2026-09-01  
Ветка: `feature/noogadev-fixes`  
База: `origin/master` / `v2.52.0`

## Результат проверки

- `go test ./...` — PASS.
- Новые unit-тесты с `-race` — PASS.
- `go vet ./bitbucket` — PASS.
- `make fmtcheck` — FAIL: `bitbucket/resource_branching_model_test.go` требует `gofmt`.
- `go mod tidy -diff` показывает, что `github.com/hashicorp/go-retryablehttp` импортируется напрямую, но объявлен как `// indirect`.
- В рабочем дереве уже присутствуют незакоммиченные `docs/superpowers/agents.md` и `tofurc`; в рамках ревью они не изменялись.

## Findings

### P0 — возможный panic при сетевой ошибке

В `bitbucket/client.go:87-90` после `HTTPClient.Do` сразу разыменовывается `resp`:

```go
resp, err := c.HTTPClient.Do(req)
if resp.StatusCode >= 400 || resp.StatusCode < 200 {
```

При DNS/TLS/network error `resp == nil`. После подключения retry-клиента этот путь становится реальным. Нужно сначала обработать `err` и `resp == nil`.

### P1 — две retry-реализации, одна не используется

`bitbucket/retry_transport.go` содержит собственный `RetryTransport`, но production-код в `provider.go` использует `go-retryablehttp`. Новые тесты проверяют преимущественно мёртвый код, поэтому они не гарантируют поведение production-клиента.

Рекомендация: оставить один механизм. Предпочтительный вариант — `go-retryablehttp`; custom transport и его тесты удалить либо подключить его вместо retryablehttp и тестировать через production-конструктор.

### P1 — backoff не exponential

Используется `retryablehttp.RateLimitLinearJitterBackoff`. Это линейный jitter backoff с поддержкой `Retry-After`, а не exponential backoff. Это подтверждается исходным кодом библиотеки: [RateLimitLinearJitterBackoff](https://github.com/hashicorp/go-retryablehttp/blob/main/client.go).

Кроме того, `RetryMax = 10` означает один initial request плюс до 10 retries, то есть до 11 запросов. Документация проекта утверждает 10 attempts и требует уточнения.

### P1 — branch restriction может создавать perpetual diff

`flattenBranchRestrictionUsers` предпочитает UUID, а `createBranchRestriction` отправляет входное значение как `Username`. Документация ресурса использует usernames. Если конфигурация содержит username, а API возвращает UUID, state и configuration могут постоянно расходиться.

Нужно определить стабильный контракт: UUID-only, username-only с обратной нормализацией или явная поддержка обоих идентификаторов с миграцией state.

### P1 — ошибки `d.Set` игнорируются

В resource Read-функциях ошибки `d.Set(...)` не проверяются. Это может скрыть ошибки state serialization, ради исправления которых добавлялись flatten-функции. Ошибки следует преобразовывать в `diag.Diagnostics`.

### P2 — `FlexBool` требует усиления

Фикс закрывает основной сценарий Issue #234, но реализация вручную удаляет JSON-кавычки и не декодирует escaped strings. Также отсутствуют явные `MarshalJSON`/`BoolPtr` helper-методы, которые присутствуют в upstream PR #247.

Стоит добавить тесты для escaped strings, malformed JSON, `MarshalJSON`, `null` и отсутствующего поля. Числовую поддержку следует явно ограничить `0/1` в комментарии или расширить реализацию осознанно.

### P2 — retry body/side effects не определены

Retry применяется к любым HTTP methods, включая POST/PUT. При нестандартном поведении API запрос мог быть выполнен, но ответить 429; повтор может создать побочный эффект. Нужно документировать допущение Bitbucket API либо ограничить retry идемпотентными операциями/добавить idempotency strategy.

## Version check

- Локальный Go: `go1.26.5`.
- `go.mod`: `go 1.25.8`.
- Implementation plan заявляет Go `1.26.5`, но CI читает версию из `go.mod`, поэтому локальная и CI-среда различаются.
- `go-retryablehttp` должен быть перенесён в основной `require` block.
- Текущая версия `v0.7.8` опубликована 2025-06-18; pkg.go.dev отмечает, что это не latest версия модуля: [package information](https://pkg.go.dev/github.com/hashicorp/go-retryablehttp).

## Upstream context

- [Issue #234](https://github.com/DrFaust92/terraform-provider-bitbucket/issues/234) подтверждает, что Bitbucket возвращает `default_branch_deletion` строкой и ломает plan/import/refresh обеих branching model resources.
- [PR #247](https://github.com/DrFaust92/terraform-provider-bitbucket/pull/247) содержит upstream-подход с `FlexBool`, `MarshalJSON` и helper для pointer.
- [Issue #254](https://github.com/DrFaust92/terraform-provider-bitbucket/issues/254) сообщает о потере deployment variables после v2.52.0 из-за pagination и изменения `bitbucket-go-client v0.11.0`.
- [Issue #242](https://github.com/DrFaust92/terraform-provider-bitbucket/issues/242) отмечает maintenance mode provider-а.
- [Issue #224](https://github.com/DrFaust92/terraform-provider-bitbucket/issues/224) указывает на несоответствие документации и required schema project branching model.
- [Issue #227](https://github.com/DrFaust92/terraform-provider-bitbucket/issues/227) показывает более широкую проблему username/UUID в user permissions.

## Recommended next steps

1. Исправить nil-response panic.
2. Выбрать и покрыть один production retry-механизм.
3. Согласовать UUID/username контракт branch restrictions.
4. Проверять ошибки `d.Set` и закрывать response bodies.
5. Добавить production-level tests: network error, context cancellation, POST/PUT body retry, Retry-After edge cases, UUID/name normalization.
6. Запустить `gofmt`, исправить `go.mod`, затем повторить `make fmtcheck`, `go test -race ./...` и `go vet ./...`.
