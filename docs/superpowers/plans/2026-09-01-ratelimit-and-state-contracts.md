# Rate Limiting & State Contracts Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Заменить слепой retry на реакцию по `X-RateLimit-Reset` с общим шлагбаумом, привести контракты state в соответствие с Bitbucket API и убрать nil-deref паники.

**Architecture:** Один `http.RoundTripper` (`rateLimitTransport`) поверх общего `rateLimitGate`; `go-retryablehttp` и `retry_transport.go` удаляются. Контракт `users` — display_name с кэшируемым индексом воркспейса. Все изменённые Read-функции проверяют ошибки `d.Set`.

**Tech Stack:** Go 1.25 (toolchain 1.26.5), terraform-plugin-sdk/v2 v2.40.1, DrFaust92/bitbucket-go-client v0.11.0, только stdlib для rate limiting.

## Global Constraints

- Спека: [`docs/superpowers/specs/2026-09-01-ratelimit-and-state-contracts-design.md`](../specs/2026-09-01-ratelimit-and-state-contracts-design.md).
- **Коммиты не выполняются автоматически.** Политика репозитория (`~/.claude/CLAUDE.md`): готовим изменения и ждём явной команды. Шаги «Commit» ниже — это подготовленные сообщения, а не разрешение запускать `git commit`.
- Префикс сообщений коммитов: `OI-0: `.
- Никаких AI-трейлеров в сообщениях коммитов.
- Новых зависимостей не добавляем; `go-retryablehttp` удаляется из прямых зависимостей.
- `maxAttempts = 3`, `maxWait = 120 * time.Second` — константы пакета, не поля схемы провайдера.
- После каждой задачи: `gofmt -l ./bitbucket` пусто, `go vet ./...` и `go test -race ./...` зелёные.
- Тесты не должны спать: время инъектируется через поля `now`/`after`.

---

### Task 1: Гигиена сборки

**Files:**
- Modify: `bitbucket/resource_branching_model_test.go` (форматирование)
- Modify: `.gitignore`

- [ ] **Step 1: Убедиться, что fmtcheck красный**

Run: `gofmt -l ./bitbucket`
Expected: `bitbucket/resource_branching_model_test.go`

- [ ] **Step 2: Отформатировать и переставить импорт**

`encoding/json` стоит отдельной строкой после блока импортов — перенести внутрь группы stdlib.

```bash
gofmt -w bitbucket/resource_branching_model_test.go
```

- [ ] **Step 3: Добавить в .gitignore локальные артефакты**

`tofurc` содержит абсолютный путь `/Users/kirillmalyavko/...`, `bin/` — собранный бинарник.

```
bin/
tofurc
```

- [ ] **Step 4: Проверить**

Run: `gofmt -l ./bitbucket && git status --short`
Expected: пустой вывод gofmt; `tofurc` и `bin/` больше не в untracked

- [ ] **Step 5: Commit**

```bash
git add .gitignore bitbucket/resource_branching_model_test.go
git commit -m "OI-0: fix gofmt and ignore local build artifacts"
```

---

### Task 2: Убрать nil-dereference паники

**Files:**
- Modify: `bitbucket/client.go:86-89`
- Modify: `bitbucket/resource_branching_model.go:192`, `bitbucket/resource_project_branching_model.go:163`, `bitbucket/resource_group.go:107`, `bitbucket/resource_group_membership.go:79`, `bitbucket/data_group.go:53`, `bitbucket/data_group_members.go:64`, `bitbucket/data_groups.go:60`
- Test: `bitbucket/client_test.go` (создать)

**Interfaces:**
- Produces: `Client.Do` возвращает `(nil, error)` вместо паники при транспортной ошибке. Ни одна сигнатура не меняется.

- [ ] **Step 1: Написать падающий тест**

```go
package bitbucket

import (
	"errors"
	"net/http"
	"strings"
	"testing"
)

type errRoundTripper struct{ err error }

func (t errRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, t.err
}

func TestClientDoTransportError(t *testing.T) {
	want := errors.New("dial tcp: connection refused")
	c := &Client{HTTPClient: &http.Client{Transport: errRoundTripper{err: want}}}

	resp, err := c.Get("2.0/repositories/example")

	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if resp != nil {
		t.Fatalf("expected nil response, got %v", resp)
	}
	if !strings.Contains(err.Error(), want.Error()) {
		t.Fatalf("expected error to wrap %q, got %q", want, err)
	}
}
```

- [ ] **Step 2: Запустить — убедиться, что падает паникой**

Run: `go test ./bitbucket -run TestClientDoTransportError -v`
Expected: FAIL, `panic: runtime error: invalid memory address or nil pointer dereference` в `client.go`

- [ ] **Step 3: Починить `Client.Do`**

В `bitbucket/client.go` заменить блок после `resp, err := c.HTTPClient.Do(req)`:

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
```

- [ ] **Step 4: Запустить — тест зелёный**

Run: `go test ./bitbucket -run TestClientDoTransportError -v`
Expected: PASS

- [ ] **Step 5: Починить 7 вызовов, игнорирующих ошибку**

В каждом файле паттерн одинаковый. Пример для `bitbucket/resource_branching_model.go:192`:

```go
	branchingModelsReq, err := client.Get(fmt.Sprintf("2.0/repositories/%s/%s/branching-model/settings", owner, repo))
	if err != nil {
		return diag.FromErr(err)
	}
	if branchingModelsReq == nil {
		return diag.Errorf("error getting Branching Model (%s): empty response", d.Id())
	}
```

Важно: `Client.Do` возвращает непустой `resp` **вместе** с ошибкой для статусов ≥ 400, поэтому проверку `StatusCode == http.StatusNotFound` нужно оставить **до** возврата ошибки там, где Read удаляет ресурс из state. Порядок в каждой Read-функции:

```go
	resp, err := client.Get(...)
	if resp != nil && resp.StatusCode == http.StatusNotFound {
		log.Printf("[WARN] ... not found, removing from state")
		d.SetId("")
		return nil
	}
	if err != nil {
		return diag.FromErr(err)
	}
	if resp == nil {
		return diag.Errorf("... empty response")
	}
```

Для data source (`data_group.go`, `data_group_members.go`, `data_groups.go`) ветки «removing from state» нет — там просто `if err != nil { return diag.FromErr(err) }`.

- [ ] **Step 6: Проверить сборку и тесты**

Run: `go build ./... && go vet ./... && go test -race ./...`
Expected: всё зелёное

- [ ] **Step 7: Commit**

```bash
git add bitbucket/
git commit -m "OI-0: return errors instead of panicking on transport failures"
```

---

### Task 3: Rate limiting через X-RateLimit-Reset

**Files:**
- Create: `bitbucket/ratelimit.go`
- Create: `bitbucket/ratelimit_test.go`
- Delete: `bitbucket/retry_transport.go`, `bitbucket/retry_transport_test.go`
- Modify: `bitbucket/provider.go` (`newHTTPClient`)
- Modify: `go.mod`, `go.sum`

**Interfaces:**
- Produces:
  - `func newRateLimitGate() *rateLimitGate`
  - `func (g *rateLimitGate) closeUntil(d time.Duration)`
  - `func (g *rateLimitGate) wait(ctx context.Context) error`
  - `func resetDelay(h http.Header, now time.Time) (time.Duration, bool)`
  - `func newHTTPClient() *http.Client`
  - константы `rateLimitMaxAttempts = 3`, `rateLimitMaxWait = 120 * time.Second`

- [ ] **Step 1: Написать тест на `resetDelay`**

```go
func TestResetDelay(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

	cases := []struct {
		name   string
		header http.Header
		want   time.Duration
		wantOK bool
	}{
		{"reset delta seconds", http.Header{"X-Ratelimit-Reset": {"156"}}, 156 * time.Second, true},
		{"reset unix timestamp", http.Header{"X-Ratelimit-Reset": {"1788609660"}}, 60 * time.Second, true},
		{"reset zero", http.Header{"X-Ratelimit-Reset": {"0"}}, 0, false},
		{"reset negative", http.Header{"X-Ratelimit-Reset": {"-5"}}, 0, false},
		{"reset garbage", http.Header{"X-Ratelimit-Reset": {"soon"}}, 0, false},
		{"retry-after seconds", http.Header{"Retry-After": {"30"}}, 30 * time.Second, true},
		{"retry-after http date", http.Header{"Retry-After": {"Tue, 01 Sep 2026 12:00:45 GMT"}}, 45 * time.Second, true},
		{"retry-after past date", http.Header{"Retry-After": {"Tue, 01 Sep 2026 11:59:00 GMT"}}, 0, false},
		{"reset wins over retry-after", http.Header{"X-Ratelimit-Reset": {"10"}, "Retry-After": {"99"}}, 10 * time.Second, true},
		{"no headers", http.Header{}, 0, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := resetDelay(tc.header, now)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if ok && got != tc.want {
				t.Fatalf("delay = %v, want %v", got, tc.want)
			}
		})
	}
}
```

Значение `1788609660` — это `2026-09-01T12:01:00Z`, то есть `now + 60s`.

- [ ] **Step 2: Запустить — падает компиляцией**

Run: `go test ./bitbucket -run TestResetDelay`
Expected: FAIL, `undefined: resetDelay`

- [ ] **Step 3: Реализовать `resetDelay` в `bitbucket/ratelimit.go`**

```go
package bitbucket

import (
	"context"
	"io"
	"log"
	"math/rand/v2"
	"net/http"
	"strconv"
	"sync"
	"time"
)

const (
	// rateLimitMaxAttempts is the total number of attempts per request,
	// including the first one.
	rateLimitMaxAttempts = 3

	// rateLimitMaxWait bounds how long a single request may block on a closed
	// rate limit window. Bitbucket windows can be an hour long; blocking that
	// long looks like a hang, so past this point the 429 is surfaced with a
	// diagnostic telling the operator to lower -parallelism instead.
	rateLimitMaxWait = 120 * time.Second

	// rateLimitBaseDelay is the first backoff step used when Bitbucket sends no
	// rate limit headers at all.
	rateLimitBaseDelay = 2 * time.Second

	// epochThreshold distinguishes delta-seconds from a unix timestamp.
	// Bitbucket sends delta-seconds, but the header is not contractual.
	epochThreshold = 1_000_000_000
)

// resetDelay reports how long to wait before retrying a rate limited request.
//
// Bitbucket Cloud answers 429 with X-RateLimit-Reset (delta-seconds until the
// window resets) and no Retry-After, so that header is the primary signal.
// Retry-After is still honored because a proxy in front of the API may send it.
func resetDelay(h http.Header, now time.Time) (time.Duration, bool) {
	if v := h.Get("X-RateLimit-Reset"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			if n >= epochThreshold {
				if d := time.Unix(n, 0).Sub(now); d > 0 {
					return d, true
				}
			} else if n > 0 {
				return time.Duration(n) * time.Second, true
			}
		}
	}

	if v := h.Get("Retry-After"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return time.Duration(n) * time.Second, true
		}
		if t, err := http.ParseTime(v); err == nil {
			if d := t.Sub(now); d > 0 {
				return d, true
			}
		}
	}

	return 0, false
}
```

- [ ] **Step 4: Запустить — зелёный**

Run: `go test ./bitbucket -run TestResetDelay -v`
Expected: PASS, 10 подтестов

- [ ] **Step 5: Написать тест на шлагбаум**

```go
func TestRateLimitGateWaitsUntilDeadline(t *testing.T) {
	clock := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	slept := make(chan time.Duration, 4)

	g := &rateLimitGate{
		now: func() time.Time { return clock },
		after: func(d time.Duration) <-chan time.Time {
			slept <- d
			clock = clock.Add(d)
			ch := make(chan time.Time, 1)
			ch <- clock
			return ch
		},
	}

	if err := g.wait(context.Background()); err != nil {
		t.Fatalf("open gate should not block: %v", err)
	}
	if len(slept) != 0 {
		t.Fatal("open gate slept")
	}

	g.closeUntil(30 * time.Second)
	if err := g.wait(context.Background()); err != nil {
		t.Fatalf("wait: %v", err)
	}
	if got := <-slept; got != 30*time.Second {
		t.Fatalf("slept %v, want 30s", got)
	}
}

func TestRateLimitGateCloseUntilNeverShortens(t *testing.T) {
	clock := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	g := &rateLimitGate{now: func() time.Time { return clock }, after: time.After}

	g.closeUntil(60 * time.Second)
	g.closeUntil(5 * time.Second)

	if got := g.deadline().Sub(clock); got != 60*time.Second {
		t.Fatalf("deadline %v, want 60s", got)
	}
}

func TestRateLimitGateHonorsContext(t *testing.T) {
	clock := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	g := &rateLimitGate{
		now:   func() time.Time { return clock },
		after: func(time.Duration) <-chan time.Time { return make(chan time.Time) }, // never fires
	}
	g.closeUntil(time.Hour)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := g.wait(ctx); err != context.Canceled {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}
```

- [ ] **Step 6: Реализовать шлагбаум**

```go
// rateLimitGate serializes rate limit waiting across every in-flight request.
//
// Terraform runs resources in parallel, so without a shared gate each of the
// N concurrent requests spends its own attempt discovering the same closed
// window, and all N wake at once when it reopens. Here the first request to
// see a 429 records the deadline and the rest simply wait for it.
type rateLimitGate struct {
	mu    sync.Mutex
	until time.Time

	now   func() time.Time
	after func(time.Duration) <-chan time.Time
}

func newRateLimitGate() *rateLimitGate {
	return &rateLimitGate{now: time.Now, after: time.After}
}

// closeUntil closes the gate for at least d. An earlier deadline never
// shortens a later one.
func (g *rateLimitGate) closeUntil(d time.Duration) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if deadline := g.now().Add(d); deadline.After(g.until) {
		g.until = deadline
	}
}

func (g *rateLimitGate) deadline() time.Time {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.until
}

// wait blocks until the gate opens or ctx is done. It loops rather than
// sleeping once because another goroutine may extend the deadline while this
// one is asleep.
func (g *rateLimitGate) wait(ctx context.Context) error {
	for {
		d := g.deadline().Sub(g.now())
		if d <= 0 {
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-g.after(d):
		}
	}
}
```

- [ ] **Step 7: Запустить — зелёные**

Run: `go test ./bitbucket -run TestRateLimitGate -v`
Expected: PASS ×3

- [ ] **Step 8: Написать тесты на транспорт**

```go
// rateLimitTestServer answers 429 with the given reset header `fails` times,
// then 200. It records every request it receives.
func rateLimitTestServer(t *testing.T, fails int32, reset string) (*httptest.Server, *int32, *[]string) {
	t.Helper()

	var hits int32
	var bodies []string
	var mu sync.Mutex

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(body))
		mu.Unlock()

		if atomic.AddInt32(&hits, 1) <= fails {
			if reset != "" {
				w.Header().Set("X-RateLimit-Reset", reset)
				w.Header().Set("X-RateLimit-Limit", "60, 60;w=3600")
			}
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte("Rate limit for this resource has been exceeded"))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	return srv, &hits, &bodies
}

// newTestTransport builds a transport whose clock is fake, so no test sleeps.
func newTestTransport(base http.RoundTripper) (*rateLimitTransport, *[]time.Duration) {
	var slept []time.Duration
	clock := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	var mu sync.Mutex

	gate := &rateLimitGate{
		now: func() time.Time { mu.Lock(); defer mu.Unlock(); return clock },
		after: func(d time.Duration) <-chan time.Time {
			mu.Lock()
			slept = append(slept, d)
			clock = clock.Add(d)
			mu.Unlock()
			ch := make(chan time.Time, 1)
			ch <- time.Time{}
			return ch
		},
	}

	return &rateLimitTransport{
		base:        base,
		gate:        gate,
		maxAttempts: rateLimitMaxAttempts,
		maxWait:     rateLimitMaxWait,
		jitter:      func(time.Duration) time.Duration { return 0 },
	}, &slept
}

func TestRateLimitTransportRetriesUsingResetHeader(t *testing.T) {
	srv, hits, _ := rateLimitTestServer(t, 1, "30")
	tr, slept := newTestTransport(http.DefaultTransport)
	client := &http.Client{Transport: tr}

	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if *hits != 2 {
		t.Fatalf("hits = %d, want 2", *hits)
	}
	if len(*slept) != 1 || (*slept)[0] != 30*time.Second {
		t.Fatalf("slept = %v, want [30s]", *slept)
	}
}

func TestRateLimitTransportSurfaces429WhenAttemptsExhausted(t *testing.T) {
	srv, hits, _ := rateLimitTestServer(t, 99, "5")
	tr, _ := newTestTransport(http.DefaultTransport)
	client := &http.Client{Transport: tr}

	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatalf("expected the 429 response, got error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", resp.StatusCode)
	}
	if *hits != rateLimitMaxAttempts {
		t.Fatalf("hits = %d, want %d", *hits, rateLimitMaxAttempts)
	}
}

func TestRateLimitTransportRefusesToWaitLongerThanMaxWait(t *testing.T) {
	srv, hits, _ := rateLimitTestServer(t, 99, "3600")
	tr, slept := newTestTransport(http.DefaultTransport)
	client := &http.Client{Transport: tr}

	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", resp.StatusCode)
	}
	if *hits != 1 {
		t.Fatalf("hits = %d, want 1 (must not retry past maxWait)", *hits)
	}
	if len(*slept) != 0 {
		t.Fatalf("slept %v, want no waiting", *slept)
	}
}

func TestRateLimitTransportReplaysRequestBody(t *testing.T) {
	srv, hits, bodies := rateLimitTestServer(t, 1, "10")
	tr, _ := newTestTransport(http.DefaultTransport)
	client := &http.Client{Transport: tr}

	resp, err := client.Post(srv.URL, "application/json", strings.NewReader(`{"name":"x"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()

	if *hits != 2 {
		t.Fatalf("hits = %d, want 2", *hits)
	}
	for i, b := range *bodies {
		if b != `{"name":"x"}` {
			t.Fatalf("attempt %d body = %q, want the original payload", i+1, b)
		}
	}
}

func TestRateLimitTransportSharesTheWindowAcrossGoroutines(t *testing.T) {
	// Every request is rate limited once. With a shared gate the first
	// goroutine to see the 429 closes the window, so the others wait instead
	// of each spending an attempt discovering it.
	var hits int32
	var limited int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		if atomic.AddInt32(&limited, 1) == 1 {
			w.Header().Set("X-RateLimit-Reset", "20")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	tr, _ := newTestTransport(http.DefaultTransport)
	client := &http.Client{Transport: tr}

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := client.Get(srv.URL)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
			resp.Body.Close()
		}()
	}
	wg.Wait()

	if got := atomic.LoadInt32(&hits); got != 11 {
		t.Fatalf("hits = %d, want 11 (10 requests + exactly 1 retry)", got)
	}
}

func TestRateLimitTransportHonorsContextCancellation(t *testing.T) {
	srv, _, _ := rateLimitTestServer(t, 99, "60")

	gate := &rateLimitGate{
		now:   time.Now,
		after: func(time.Duration) <-chan time.Time { return make(chan time.Time) }, // never fires
	}
	tr := &rateLimitTransport{
		base:        http.DefaultTransport,
		gate:        gate,
		maxAttempts: rateLimitMaxAttempts,
		maxWait:     rateLimitMaxWait,
		jitter:      func(time.Duration) time.Duration { return 0 },
	}
	client := &http.Client{Transport: tr}

	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)

	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	if _, err := client.Do(req); err == nil {
		t.Fatal("expected an error once the context is cancelled")
	}
}
```

- [ ] **Step 9: Реализовать транспорт**

```go
// rateLimitTransport retries HTTP 429 responses, waiting exactly as long as
// Bitbucket says the window needs rather than guessing with a backoff curve.
type rateLimitTransport struct {
	base        http.RoundTripper
	gate        *rateLimitGate
	maxAttempts int
	maxWait     time.Duration
	jitter      func(time.Duration) time.Duration
}

func (t *rateLimitTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	for attempt := 1; ; attempt++ {
		if err := t.gate.wait(req.Context()); err != nil {
			return nil, err
		}

		if attempt > 1 {
			if err := rewindBody(req); err != nil {
				return nil, err
			}
		}

		resp, err := t.base.RoundTrip(req)
		if err != nil || resp.StatusCode != http.StatusTooManyRequests {
			return resp, err
		}

		// The caller turns this response into a diagnostic that names the
		// endpoint, so a spent 429 is returned rather than swallowed.
		if attempt >= t.maxAttempts {
			log.Printf("[WARN] Bitbucket rate limit hit on %s %s, giving up after %d attempts", req.Method, req.URL, attempt)
			return resp, nil
		}

		delay, ok := resetDelay(resp.Header, t.gate.now())
		if !ok {
			delay = rateLimitBaseDelay << (attempt - 1)
		}
		if delay > t.maxWait {
			log.Printf("[WARN] Bitbucket rate limit on %s %s resets in %s, longer than the %s cap", req.Method, req.URL, delay, t.maxWait)
			return resp, nil
		}

		// The body must be drained before the connection can be reused.
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()

		delay += t.jitter(delay)
		log.Printf("[INFO] Bitbucket rate limit on %s %s, waiting %s before attempt %d/%d", req.Method, req.URL, delay, attempt+1, t.maxAttempts)
		t.gate.closeUntil(delay)
	}
}

// rewindBody restores a consumed request body so the request can be replayed.
// http.NewRequest populates GetBody for the *bytes.Buffer payloads both the
// internal client and the generated swagger client build.
func rewindBody(req *http.Request) error {
	if req.Body == nil || req.Body == http.NoBody {
		return nil
	}
	if req.GetBody == nil {
		return fmt.Errorf("cannot retry %s %s: request body is not replayable", req.Method, req.URL)
	}

	body, err := req.GetBody()
	if err != nil {
		return err
	}
	req.Body = body

	return nil
}

// jitterUpTo spreads the moment concurrent requests resume so they do not all
// hit the API on the same millisecond when the window reopens.
func jitterUpTo(d time.Duration) time.Duration {
	const cap = 500 * time.Millisecond
	if d > cap {
		d = cap
	}
	if d <= 0 {
		return 0
	}
	return time.Duration(rand.Int64N(int64(d)))
}
```

Добавить `"fmt"` в импорты `ratelimit.go`.

- [ ] **Step 10: Запустить тесты транспорта**

Run: `go test ./bitbucket -run TestRateLimitTransport -race -v`
Expected: PASS ×6

- [ ] **Step 11: Переключить провайдер и удалить старое**

В `bitbucket/provider.go` заменить `newHTTPClient` целиком и убрать импорт `retryablehttp`:

```go
// newHTTPClient builds the HTTP client shared by the internal and generated
// Bitbucket clients. The shared gate means a rate limit discovered by one
// request pauses all of them.
func newHTTPClient() *http.Client {
	return &http.Client{
		Transport: &rateLimitTransport{
			base:        http.DefaultTransport,
			gate:        newRateLimitGate(),
			maxAttempts: rateLimitMaxAttempts,
			maxWait:     rateLimitMaxWait,
			jitter:      jitterUpTo,
		},
	}
}
```

```bash
git rm bitbucket/retry_transport.go bitbucket/retry_transport_test.go
go mod tidy
```

- [ ] **Step 12: Проверить, что зависимость ушла**

Run: `grep -c retryablehttp go.mod; go mod tidy -diff && echo TIDY-OK`
Expected: `0`, затем `TIDY-OK`

- [ ] **Step 13: Полная проверка**

Run: `gofmt -l ./bitbucket && go vet ./... && go test -race -count=1 ./...`
Expected: всё зелёное, gofmt пустой

- [ ] **Step 14: Commit**

```bash
git add bitbucket/ratelimit.go bitbucket/ratelimit_test.go bitbucket/provider.go go.mod go.sum
git rm --cached bitbucket/retry_transport.go bitbucket/retry_transport_test.go
git commit -m "OI-0: wait for the Bitbucket rate limit window instead of guessing a backoff"
```

---

### Task 4: Диагностика 429

**Files:**
- Modify: `bitbucket/error.go`
- Modify: `bitbucket/client.go`
- Test: `bitbucket/error_test.go` (создать)

**Interfaces:**
- Consumes: `resetDelay` из Task 3
- Produces: `func rateLimitHint(resp *http.Response) string` — пустая строка, если ответ не 429

- [ ] **Step 1: Написать падающий тест**

```go
func TestRateLimitHint(t *testing.T) {
	cases := []struct {
		name string
		resp *http.Response
		want string
	}{
		{
			name: "429 with reset and limit",
			resp: &http.Response{
				StatusCode: http.StatusTooManyRequests,
				Header: http.Header{
					"X-Ratelimit-Reset": {"156"},
					"X-Ratelimit-Limit": {"60, 60;w=3600"},
				},
			},
			want: "rate limit exceeded, resets in 156s (limit 60, 60;w=3600); reduce -parallelism or re-run after the window resets",
		},
		{
			name: "429 without headers",
			resp: &http.Response{StatusCode: http.StatusTooManyRequests, Header: http.Header{}},
			want: "rate limit exceeded; reduce -parallelism or re-run after the window resets",
		},
		{
			name: "not a rate limit response",
			resp: &http.Response{StatusCode: http.StatusNotFound, Header: http.Header{}},
			want: "",
		},
		{
			name: "nil response",
			resp: nil,
			want: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := rateLimitHint(tc.resp); got != tc.want {
				t.Fatalf("rateLimitHint() = %q, want %q", got, tc.want)
			}
		})
	}
}
```

- [ ] **Step 2: Запустить — падает**

Run: `go test ./bitbucket -run TestRateLimitHint`
Expected: FAIL, `undefined: rateLimitHint`

- [ ] **Step 3: Реализовать в `bitbucket/error.go`**

```go
// rateLimitHint turns a 429 response into an actionable message. Bitbucket
// answers with text/plain "Rate limit for this resource has been exceeded",
// which says nothing about how long to wait; the headers do.
func rateLimitHint(resp *http.Response) string {
	if resp == nil || resp.StatusCode != http.StatusTooManyRequests {
		return ""
	}

	msg := "rate limit exceeded"
	if d, ok := resetDelay(resp.Header, time.Now()); ok {
		msg += fmt.Sprintf(", resets in %ds", int(d.Round(time.Second).Seconds()))
		if limit := resp.Header.Get("X-RateLimit-Limit"); limit != "" {
			msg += fmt.Sprintf(" (limit %s)", limit)
		}
	}

	return msg + "; reduce -parallelism or re-run after the window resets"
}
```

- [ ] **Step 4: Подключить в оба слоя ошибок**

В `handleClientError` (`bitbucket/error.go`), перед разбором swagger-ошибки:

```go
	if hint := rateLimitHint(httpResponse); hint != "" {
		return fmt.Errorf("%s: %s", httpResponse.Status, hint)
	}
```

В `Client.Do` (`bitbucket/client.go`), внутри ветки `resp.StatusCode >= 400`, после чтения тела:

```go
		if hint := rateLimitHint(resp); hint != "" {
			apiError.APIError.Message = hint
		}
```

- [ ] **Step 5: Запустить**

Run: `go test ./bitbucket -run TestRateLimitHint -v && go test -race ./...`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add bitbucket/error.go bitbucket/error_test.go bitbucket/client.go
git commit -m "OI-0: report reset time and remedy on rate limit errors"
```

---

### Task 5: Переписать разбор FlexBool

**Files:**
- Modify: `bitbucket/flex_bool.go`
- Modify: `bitbucket/flex_bool_test.go`

**Interfaces:**
- Produces: `type FlexBool bool` с `UnmarshalJSON` и `Bool()` (сигнатуры не меняются)

- [ ] **Step 1: Дописать тесты на дыры**

```go
func TestFlexBoolUnmarshalEdgeCases(t *testing.T) {
	cases := []struct {
		name     string
		input    string
		expected bool
		wantErr  bool
	}{
		{"escaped string true", `{"val": "true"}`, true, false},
		{"string with spaces", `{"val": " true "}`, true, false},
		{"number two is not a bool", `{"val": 2}`, false, true},
		{"negative number", `{"val": -1}`, false, true},
		{"float", `{"val": 1.5}`, false, true},
		{"string t", `{"val": "t"}`, true, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var wrapper struct {
				Val FlexBool `json:"val"`
			}
			err := json.Unmarshal([]byte(tc.input), &wrapper)
			if (err != nil) != tc.wantErr {
				t.Fatalf("expected error: %v, got: %v", tc.wantErr, err)
			}
			if !tc.wantErr && wrapper.Val.Bool() != tc.expected {
				t.Fatalf("expected %v, got %v", tc.expected, wrapper.Val.Bool())
			}
		})
	}
}

func TestFlexBoolMarshalRoundTrip(t *testing.T) {
	type payload struct {
		Val *FlexBool `json:"val,omitempty"`
	}

	yes := FlexBool(true)
	data, err := json.Marshal(payload{Val: &yes})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(data) != `{"val":true}` {
		t.Fatalf("marshal = %s, want {\"val\":true}", data)
	}

	if data, err = json.Marshal(payload{}); err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(data) != `{}` {
		t.Fatalf("marshal of absent value = %s, want {}", data)
	}
}

func TestFlexBoolAbsentFieldStaysNil(t *testing.T) {
	var wrapper struct {
		Val *FlexBool `json:"val,omitempty"`
	}
	if err := json.Unmarshal([]byte(`{}`), &wrapper); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if wrapper.Val != nil {
		t.Fatalf("expected nil, got %v", *wrapper.Val)
	}
}
```

- [ ] **Step 2: Запустить — падает на escaped string и на числах**

Run: `go test ./bitbucket -run TestFlexBool -v`
Expected: FAIL на `escaped string true`, `number two is not a bool`, `negative number`, `float`

- [ ] **Step 3: Заменить `UnmarshalJSON`**

```go
// UnmarshalJSON accepts the shapes Bitbucket actually returns for boolean
// fields: a JSON boolean, a quoted boolean ("true", "FALSE", "1"), 0 or 1, or
// null. See upstream issue #234.
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

	// Numbers are deliberately narrowed to 0 and 1 rather than treating any
	// non-zero value as true, so an unexpected payload fails loudly.
	var n json.Number
	if err := json.Unmarshal(data, &n); err == nil {
		i, err := n.Int64()
		if err != nil || (i != 0 && i != 1) {
			return fmt.Errorf("cannot unmarshal number %s into FlexBool: want 0 or 1", n)
		}
		*fb = FlexBool(i == 1)

		return nil
	}

	// A JSON null only reaches here for a non-pointer field; encoding/json
	// nils the pointer without calling this method for *FlexBool.
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		*fb = false
		return nil
	}

	return fmt.Errorf("cannot unmarshal %s into FlexBool", data)
}
```

Импорты файла: `bytes`, `encoding/json`, `fmt`, `strconv`, `strings`.

- [ ] **Step 4: Запустить**

Run: `go test ./bitbucket -run TestFlexBool -v`
Expected: PASS во всех подтестах, включая существующие из первой итерации

- [ ] **Step 5: Commit**

```bash
git add bitbucket/flex_bool.go bitbucket/flex_bool_test.go
git commit -m "OI-0: parse FlexBool through encoding/json and narrow numbers to 0/1"
```

---

### Task 6: Контракт users и groups в branch restriction

**Files:**
- Create: `bitbucket/workspace_members.go`
- Create: `bitbucket/workspace_members_test.go`
- Modify: `bitbucket/provider.go` (поле в `Clients`)
- Modify: `bitbucket/resource_branch_restriction.go`
- Modify: `bitbucket/resource_branch_restriction_test.go`
- Modify: `docs/resources/branch_restriction.md`

**Interfaces:**
- Produces:
  - `type workspaceMemberCache struct{ ... }`, `func newWorkspaceMemberCache() *workspaceMemberCache`
  - `func (c *workspaceMemberCache) uuidsByDisplayName(pc ProviderConfig, workspace string) (map[string]string, error)`
  - `func (c *workspaceMemberCache) resolve(pc ProviderConfig, workspace string, names *schema.Set) ([]bitbucket.Account, error)`
  - `func flattenBranchRestrictionUsers(accounts []bitbucket.Account) []string`
  - `func flattenBranchRestrictionGroups(groups []bitbucket.Group) []interface{}`
  - `Clients` получает поле `members *workspaceMemberCache`

- [ ] **Step 1: Написать тесты на flatten**

Заменить существующие `TestFlattenBranchRestrictionUsers` и `TestFlattenBranchRestrictionGroups`:

```go
func TestFlattenBranchRestrictionUsers(t *testing.T) {
	accounts := []bitbucket.Account{
		{Uuid: "{11111111-1111-1111-1111-111111111111}", DisplayName: "Jane Doe"},
		{Uuid: "{22222222-2222-2222-2222-222222222222}"}, // no display name
		{DisplayName: "John Smith"},
	}

	result := flattenBranchRestrictionUsers(accounts)
	expected := []string{
		"Jane Doe",
		"{22222222-2222-2222-2222-222222222222}",
		"John Smith",
	}

	if len(result) != len(expected) {
		t.Fatalf("expected length %d, got %d", len(expected), len(result))
	}
	for i := range expected {
		if result[i] != expected[i] {
			t.Errorf("at index %d: expected %q, got %q", i, expected[i], result[i])
		}
	}
}

func TestFlattenBranchRestrictionGroups(t *testing.T) {
	groups := []bitbucket.Group{
		// The workspace is the reliable source for the slug the schema wants.
		{Slug: "devops", Workspace: &bitbucket.Workspace{Slug: "noogadev"}, Owner: &bitbucket.Account{DisplayName: "Nooga Dev"}},
		{Slug: "backend", FullSlug: "noogadev:backend"},
		{Slug: "legacy", Owner: &bitbucket.Account{Username: "noogadev"}},
		{Slug: "orphan"},
	}

	result := flattenBranchRestrictionGroups(groups)
	expected := []map[string]interface{}{
		{"owner": "noogadev", "slug": "devops"},
		{"owner": "noogadev", "slug": "backend"},
		{"owner": "noogadev", "slug": "legacy"},
		{"owner": "", "slug": "orphan"},
	}

	if len(result) != len(expected) {
		t.Fatalf("expected %d groups, got %d", len(expected), len(result))
	}
	for i, want := range expected {
		got := result[i].(map[string]interface{})
		if got["owner"] != want["owner"] || got["slug"] != want["slug"] {
			t.Errorf("at index %d: got %+v, want %+v", i, got, want)
		}
	}
}
```

- [ ] **Step 2: Запустить — падает**

Run: `go test ./bitbucket -run TestFlattenBranchRestriction -v`
Expected: FAIL — users отдаёт UUID вместо display_name; groups отдаёт "Nooga Dev" и "" вместо "noogadev"

- [ ] **Step 3: Переписать обе flatten-функции**

```go
// flattenBranchRestrictionUsers stores the display name Bitbucket returns,
// which is the identifier the `users` argument accepts. Bitbucket deprecated
// `username` and no longer returns it, so the UUID is the only fallback when
// an account has no display name.
func flattenBranchRestrictionUsers(accounts []bitbucket.Account) []string {
	users := make([]string, 0, len(accounts))

	for _, acc := range accounts {
		switch {
		case acc.DisplayName != "":
			users = append(users, acc.DisplayName)
		case acc.Uuid != "":
			users = append(users, acc.Uuid)
		}
	}

	return users
}

// flattenBranchRestrictionGroups resolves the workspace slug the `owner`
// argument expects. The group's `owner` account carries a human readable
// display name, not a slug, so the workspace object is preferred.
func flattenBranchRestrictionGroups(groups []bitbucket.Group) []interface{} {
	out := make([]interface{}, 0, len(groups))

	for _, g := range groups {
		owner := ""
		switch {
		case g.Workspace != nil && g.Workspace.Slug != "":
			owner = g.Workspace.Slug
		case g.FullSlug != "":
			owner, _, _ = strings.Cut(g.FullSlug, ":")
		case g.Owner != nil && g.Owner.Username != "":
			owner = g.Owner.Username
		}

		out = append(out, map[string]interface{}{
			"owner": owner,
			"slug":  g.Slug,
		})
	}

	return out
}
```

- [ ] **Step 4: Запустить**

Run: `go test ./bitbucket -run TestFlattenBranchRestriction -v`
Expected: PASS ×2

- [ ] **Step 5: Написать тесты на кэш мемберов**

```go
func TestWorkspaceMemberCachePaginatesAndCaches(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "application/json")

		if r.URL.Query().Get("page") == "2" {
			_, _ = io.WriteString(w, `{"page":2,"values":[
				{"user":{"uuid":"{u2}","display_name":"John Smith"}}
			]}`)
			return
		}
		_, _ = io.WriteString(w, `{"page":1,"next":"?page=2","values":[
			{"user":{"uuid":"{u1}","display_name":"Jane Doe"}}
		]}`)
	}))
	defer srv.Close()

	pc := testProviderConfig(t, srv.URL)
	cache := newWorkspaceMemberCache()

	index, err := cache.uuidsByDisplayName(pc, "noogadev")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if index["Jane Doe"] != "{u1}" || index["John Smith"] != "{u2}" {
		t.Fatalf("index = %+v, want both pages", index)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("calls = %d, want 2 (both pages fetched)", got)
	}

	if _, err = cache.uuidsByDisplayName(pc, "noogadev"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("calls = %d, want 2 (second lookup must be cached)", got)
	}
}

func TestWorkspaceMemberCacheRejectsDuplicateDisplayNames(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"page":1,"values":[
			{"user":{"uuid":"{u1}","display_name":"Jane Doe"}},
			{"user":{"uuid":"{u2}","display_name":"Jane Doe"}}
		]}`)
	}))
	defer srv.Close()

	pc := testProviderConfig(t, srv.URL)

	_, err := newWorkspaceMemberCache().uuidsByDisplayName(pc, "noogadev")
	if err == nil {
		t.Fatal("expected an error for ambiguous display names")
	}
	if !strings.Contains(err.Error(), "Jane Doe") {
		t.Fatalf("error should name the ambiguous member, got %q", err)
	}
}

func TestWorkspaceMemberCacheReportsUnknownName(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"page":1,"values":[
			{"user":{"uuid":"{u1}","display_name":"Jane Doe"}}
		]}`)
	}))
	defer srv.Close()

	pc := testProviderConfig(t, srv.URL)
	names := schema.NewSet(schema.HashString, []interface{}{"Nobody Here"})

	_, err := newWorkspaceMemberCache().resolve(pc, "noogadev", names)
	if err == nil {
		t.Fatal("expected an error for an unknown display name")
	}
	if !strings.Contains(err.Error(), "Nobody Here") {
		t.Fatalf("error should name the missing member, got %q", err)
	}
}

// testProviderConfig points the generated client at a test server.
func testProviderConfig(t *testing.T, baseURL string) ProviderConfig {
	t.Helper()

	conf := bitbucket.NewConfiguration()
	conf.BasePath = baseURL
	conf.HTTPClient = &http.Client{}

	return ProviderConfig{
		ApiClient:   bitbucket.NewAPIClient(conf),
		AuthContext: context.Background(),
	}
}
```

- [ ] **Step 6: Запустить — падает компиляцией**

Run: `go test ./bitbucket -run TestWorkspaceMemberCache`
Expected: FAIL, `undefined: newWorkspaceMemberCache`

- [ ] **Step 7: Реализовать `bitbucket/workspace_members.go`**

```go
package bitbucket

import (
	"fmt"
	"sync"

	"github.com/DrFaust92/bitbucket-go-client"
	"github.com/antihax/optional"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

// workspaceMemberCache memoizes the display name to UUID index of a workspace.
//
// Resources address users by display name but the API needs a UUID, and the
// only way to map one to the other is to page through every workspace member.
// A plan touching many branch restrictions would otherwise repeat that walk
// per resource, which is exactly the traffic that triggers rate limiting.
type workspaceMemberCache struct {
	mu      sync.Mutex
	indexes map[string]map[string]string
}

func newWorkspaceMemberCache() *workspaceMemberCache {
	return &workspaceMemberCache{indexes: map[string]map[string]string{}}
}

// uuidsByDisplayName returns the workspace's index, fetching it once. The lock
// is held across the fetch so concurrent resources wait for one walk rather
// than starting their own.
func (c *workspaceMemberCache) uuidsByDisplayName(pc ProviderConfig, workspace string) (map[string]string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if index, ok := c.indexes[workspace]; ok {
		return index, nil
	}

	index := map[string]string{}
	options := bitbucket.WorkspacesApiWorkspacesWorkspaceMembersGetOpts{}

	for {
		members, res, err := pc.ApiClient.WorkspacesApi.WorkspacesWorkspaceMembersGet(pc.AuthContext, workspace, &options)
		if err := handleClientError(res, err); err != nil {
			return nil, fmt.Errorf("listing members of workspace %q: %w", workspace, err)
		}

		for _, member := range members.Values {
			if member.User == nil || member.User.DisplayName == "" {
				continue
			}

			name := member.User.DisplayName
			if existing, ok := index[name]; ok && existing != member.User.Uuid {
				return nil, fmt.Errorf(
					"workspace %q has more than one member named %q (%s and %s); use the account UUID instead",
					workspace, name, existing, member.User.Uuid)
			}

			index[name] = member.User.Uuid
		}

		if members.Next == "" {
			break
		}
		options.Page = optional.NewInt32(members.Page + 1)
	}

	c.indexes[workspace] = index

	return index, nil
}

// resolve maps configured user identifiers to accounts the API accepts. A UUID
// is passed through unchanged so operators are not forced onto display names.
func (c *workspaceMemberCache) resolve(pc ProviderConfig, workspace string, names *schema.Set) ([]bitbucket.Account, error) {
	if names == nil || names.Len() == 0 {
		return nil, nil
	}

	var index map[string]string

	accounts := make([]bitbucket.Account, 0, names.Len())
	for _, item := range names.List() {
		name := item.(string)

		if isAccountUUID(name) {
			accounts = append(accounts, bitbucket.Account{Uuid: name})
			continue
		}

		if index == nil {
			var err error
			if index, err = c.uuidsByDisplayName(pc, workspace); err != nil {
				return nil, err
			}
		}

		uuid, ok := index[name]
		if !ok {
			return nil, fmt.Errorf("no member of workspace %q has the display name %q", workspace, name)
		}

		accounts = append(accounts, bitbucket.Account{Uuid: uuid})
	}

	return accounts, nil
}

// isAccountUUID reports whether a configured value is already a Bitbucket
// account UUID, which the API always writes in braces.
func isAccountUUID(s string) bool {
	return len(s) > 2 && s[0] == '{' && s[len(s)-1] == '}'
}
```

- [ ] **Step 8: Запустить**

Run: `go test ./bitbucket -run TestWorkspaceMemberCache -race -v`
Expected: PASS ×3

- [ ] **Step 9: Подключить кэш к провайдеру**

В `bitbucket/provider.go`:

```go
type Clients struct {
	genClient  ProviderConfig
	httpClient Client
	members    *workspaceMemberCache
}
```

и в `providerConfigure`:

```go
	clients := Clients{
		genClient:  apiClient,
		httpClient: *client,
		members:    newWorkspaceMemberCache(),
	}
```

- [ ] **Step 10: Использовать резолв в create и update**

В `bitbucket/resource_branch_restriction.go` изменить сигнатуру и оба вызова:

```go
func createBranchRestriction(d *schema.ResourceData, users []bitbucket.Account) *bitbucket.Branchrestriction {
```

Удалить из тела функции блок, строивший `users` из `d.Get("users")`, и оставить использование параметра. В `resourceBranchRestrictionsCreate` и `resourceBranchRestrictionsUpdate` перед вызовом:

```go
	users, err := m.(Clients).members.resolve(c, d.Get("owner").(string), d.Get("users").(*schema.Set))
	if err != nil {
		return diag.FromErr(err)
	}
	branchRestriction := createBranchRestriction(d, users)
```

- [ ] **Step 11: Проверять ошибки `d.Set` в Read**

```go
	if err := d.Set("users", flattenBranchRestrictionUsers(brRes.Users)); err != nil {
		return diag.FromErr(err)
	}
	if err := d.Set("groups", flattenBranchRestrictionGroups(brRes.Groups)); err != nil {
		return diag.FromErr(err)
	}
```

И в `resourceBranchingModelsRead` для `default_branch_deletion`.

- [ ] **Step 12: Обновить документацию**

В `docs/resources/branch_restriction.md`:

```markdown
* OAuth2 Scopes: `repository:admin`; using `users` additionally requires `account`
* API token permissions: `read:repository:bitbucket` and `admin:repository:bitbucket`; using `users` additionally requires `read:workspace:bitbucket`
```

Пример:

```hcl
  users = [ "My Display Name" ]
```

Описание аргумента:

```markdown
* `users` - (Optional) A list of users to use. Each entry is either an account UUID (`{...}`) or the display name of a workspace member, which is resolved to its UUID when the restriction is written. State always stores the display name Bitbucket returns.
```

- [ ] **Step 13: Полная проверка**

Run: `gofmt -l ./bitbucket && go vet ./... && go test -race -count=1 ./...`
Expected: всё зелёное

- [ ] **Step 14: Commit**

```bash
git add bitbucket/workspace_members.go bitbucket/workspace_members_test.go bitbucket/provider.go bitbucket/resource_branch_restriction.go bitbucket/resource_branch_restriction_test.go bitbucket/resource_branching_model.go docs/resources/branch_restriction.md
git commit -m "OI-0: address branch restriction users by display name and groups by workspace slug"
```

---

### Task 7: Пагинация deployment variables (upstream #254)

**Files:**
- Modify: `bitbucket/resource_deployment_variable.go`
- Test: `bitbucket/resource_deployment_variable_test.go`

**Interfaces:**
- Produces: `func findDeploymentVariable(c ProviderConfig, workspace, repoSlug, deployment, uuid string) (*bitbucket.DeploymentVariable, error)` — возвращает `(nil, nil)`, если переменной нет

- [ ] **Step 1: Написать падающий тест**

```go
func TestFindDeploymentVariableSecondPage(t *testing.T) {
	var pages int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&pages, 1)
		w.Header().Set("Content-Type", "application/json")

		if r.URL.Query().Get("page") == "2" {
			_, _ = io.WriteString(w, `{"page":2,"size":11,"values":[
				{"uuid":"{wanted}","key":"LATE","value":"v","secured":false}
			]}`)
			return
		}
		_, _ = io.WriteString(w, `{"page":1,"size":11,"next":"?page=2","values":[
			{"uuid":"{other}","key":"EARLY","value":"v","secured":false}
		]}`)
	}))
	defer srv.Close()

	got, err := findDeploymentVariable(testProviderConfig(t, srv.URL), "ws", "repo", "{deploy}", "{wanted}")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil {
		t.Fatal("variable on the second page was not found")
	}
	if got.Key != "LATE" {
		t.Fatalf("key = %q, want LATE", got.Key)
	}
	if atomic.LoadInt32(&pages) != 2 {
		t.Fatalf("pages fetched = %d, want 2", pages)
	}
}

func TestFindDeploymentVariableMissing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"page":1,"size":1,"values":[{"uuid":"{other}","key":"X"}]}`)
	}))
	defer srv.Close()

	got, err := findDeploymentVariable(testProviderConfig(t, srv.URL), "ws", "repo", "{deploy}", "{missing}")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil, got %+v", got)
	}
}
```

- [ ] **Step 2: Запустить — падает**

Run: `go test ./bitbucket -run TestFindDeploymentVariable`
Expected: FAIL, `undefined: findDeploymentVariable`

- [ ] **Step 3: Реализовать поиск с пагинацией**

В `bitbucket/resource_deployment_variable.go`:

```go
// findDeploymentVariable walks every page of a deployment's variables.
// Bitbucket returns 10 per page, so a single unpaginated call silently reports
// anything past the first page as deleted (upstream issue #254).
func findDeploymentVariable(c ProviderConfig, workspace, repoSlug, deployment, uuid string) (*bitbucket.DeploymentVariable, error) {
	options := bitbucket.PipelinesApiGetDeploymentVariablesOpts{}

	for {
		vars, res, err := c.ApiClient.PipelinesApi.GetDeploymentVariables(c.AuthContext, workspace, repoSlug, deployment, &options)
		if err := handleClientError(res, err); err != nil {
			return nil, err
		}

		for _, v := range vars.Values {
			if v.Uuid == uuid {
				return &v, nil
			}
		}

		if vars.Next == "" {
			return nil, nil
		}
		options.Page = optional.NewInt32(vars.Page + 1)
	}
}
```

Проверить точное имя opts-типа и наличие полей `Next`/`Page` перед написанием:

```bash
grep -n "GetDeploymentVariablesOpts" -A 6 "$(go env GOMODCACHE)/github.com/"'!dr!faust92'"/bitbucket-go-client@v0.11.0/api_pipelines.go" | head -20
grep -n "type PaginatedDeploymentVariable" -A 10 "$(go env GOMODCACHE)/github.com/"'!dr!faust92'"/bitbucket-go-client@v0.11.0/"model_paginated_deployment_variable*.go
```

Если у ответа нет поля `Next`, использовать `Page`/`Size`/`Pagelen` и останавливаться, когда собрано `Size` элементов.

- [ ] **Step 4: Переписать Read через новую функцию**

```go
	deployVar, err := findDeploymentVariable(c, workspace, repoSlug, deployment, d.Id())
	if err != nil {
		return diag.FromErr(err)
	}
	if deployVar == nil {
		log.Printf("[WARN] Deployment Variable (%s) not found, removing from state", d.Id())
		d.SetId("")
		return nil
	}
```

Удалить прежние блоки `if rvRes.Size < 1` и ручной цикл поиска. Ошибки `d.Set` в этой функции обернуть в `diag.FromErr`.

- [ ] **Step 5: Запустить**

Run: `go test ./bitbucket -run TestFindDeploymentVariable -v`
Expected: PASS ×2

- [ ] **Step 6: Полная проверка**

Run: `gofmt -l ./bitbucket && go vet ./... && go test -race -count=1 ./...`
Expected: всё зелёное

- [ ] **Step 7: Commit**

```bash
git add bitbucket/resource_deployment_variable.go bitbucket/resource_deployment_variable_test.go
git commit -m "OI-0: paginate deployment variable lookups so late variables are not dropped"
```

---

### Task 8: Документация

**Files:**
- Modify: `CHANGELOG.md`
- Modify: `docs/resources/project_branching_model.md`
- Modify: `docs/superpowers/agents.md`

- [ ] **Step 1: Записать breaking changes в CHANGELOG.md**

Открыть файл, посмотреть формат существующих записей и добавить запись в том же стиле. Содержание:

- `bitbucket_branch_restriction`: `users` теперь адресует членов воркспейса по display name или UUID; в state пишется display name. Конфигурации, использовавшие username, нужно обновить.
- `bitbucket_branch_restriction`: `groups` наконец попадают в state (раньше `d.Set` молча падал и обнулял set), поэтому первый plan после обновления покажет diff.
- `bitbucket_deployment_variable`: переменные за первой страницей больше не удаляются из state.
- Rate limiting: 429 ожидается по `X-RateLimit-Reset`, не дольше 120 секунд, после чего возвращается ошибка с временем сброса.

- [ ] **Step 2: Поправить #224 в документации**

В `docs/resources/project_branching_model.md` пометить блок `development` как Required — схема действительно его требует.

- [ ] **Step 3: Исправить неточности в agents.md**

- «exponential backoff» → описание фактического механизма (ожидание по `X-RateLimit-Reset` с общим шлагбаумом).
- «up to 10 attempts» → `rateLimitMaxAttempts = 3`.
- Убрать утверждение «100% test coverage».

- [ ] **Step 4: Проверить markdown-линтеры, которые гоняет CI**

Run: `npx -y markdownlint-cli --config .markdownlint.yml docs`
Expected: без ошибок

- [ ] **Step 5: Commit**

```bash
git add CHANGELOG.md docs/
git commit -m "OI-0: document rate limit behaviour and branch restriction contract changes"
```

---

## Self-Review

**Покрытие спеки:** `rateLimitGate` и `rateLimitTransport` — Task 3; `resetDelay` — Task 3; диагностика 429 — Task 4; контракт users и кэш мемберов — Task 6; `groups.owner` — Task 6; FlexBool — Task 5; nil-deref — Task 2; `d.Set` — Tasks 6 и 7; пагинация #254 — Task 7; гигиена сборки — Task 1; docs и CHANGELOG — Tasks 6 и 8. Пунктов спеки без задачи нет.

**Согласованность имён:** `newRateLimitGate`, `closeUntil`, `deadline`, `wait`, `resetDelay`, `rewindBody`, `jitterUpTo`, `rateLimitHint`, `newWorkspaceMemberCache`, `uuidsByDisplayName`, `resolve`, `isAccountUUID`, `findDeploymentVariable`, `testProviderConfig` — используются одинаково во всех задачах. `testProviderConfig` определяется в Task 6 (Step 5) и переиспользуется в Task 7.

**Риск:** имена opts-типа и полей пагинации в `GetDeploymentVariables` не проверены на момент написания плана — Task 7 Step 3 содержит команды проверки и запасной вариант.
