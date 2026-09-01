package bitbucket

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

// testNow is the frozen clock every rate limit test runs against.
var testNow = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

func TestResetDelay(t *testing.T) {
	cases := []struct {
		name   string
		header http.Header
		want   time.Duration
		wantOK bool
	}{
		// Bitbucket Cloud answers 429 with X-RateLimit-Reset in delta-seconds
		// and no Retry-After at all.
		{"reset delta seconds", http.Header{"X-Ratelimit-Reset": {"156"}}, 156 * time.Second, true},
		{"reset unix timestamp", http.Header{"X-Ratelimit-Reset": {"1788264060"}}, 60 * time.Second, true},
		{"reset zero", http.Header{"X-Ratelimit-Reset": {"0"}}, 0, false},
		{"reset negative", http.Header{"X-Ratelimit-Reset": {"-5"}}, 0, false},
		{"reset garbage", http.Header{"X-Ratelimit-Reset": {"soon"}}, 0, false},
		{"reset timestamp in the past", http.Header{"X-Ratelimit-Reset": {"1788177660"}}, 0, false},
		{"retry-after seconds", http.Header{"Retry-After": {"30"}}, 30 * time.Second, true},
		{"retry-after http date", http.Header{"Retry-After": {"Tue, 01 Sep 2026 12:00:45 GMT"}}, 45 * time.Second, true},
		{"retry-after past date", http.Header{"Retry-After": {"Tue, 01 Sep 2026 11:59:00 GMT"}}, 0, false},
		{"reset wins over retry-after", http.Header{"X-Ratelimit-Reset": {"10"}, "Retry-After": {"99"}}, 10 * time.Second, true},
		{"retry-after used when reset is unusable", http.Header{"X-Ratelimit-Reset": {"soon"}, "Retry-After": {"7"}}, 7 * time.Second, true},
		{"no headers", http.Header{}, 0, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := resetDelay(tc.header, testNow)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if ok && got != tc.want {
				t.Fatalf("delay = %v, want %v", got, tc.want)
			}
		})
	}
}

// fakeClock drives the gate without any test actually sleeping.
type fakeClock struct {
	mu    sync.Mutex
	now   time.Time
	slept []time.Duration
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: testNow}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	c.slept = append(c.slept, d)
	c.now = c.now.Add(d)
	fired := c.now
	c.mu.Unlock()

	ch := make(chan time.Time, 1)
	ch <- fired

	return ch
}

func (c *fakeClock) sleeps() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]time.Duration(nil), c.slept...)
}

func newFakeGate(c *fakeClock) *rateLimitGate {
	return &rateLimitGate{now: c.Now, after: c.After}
}

func TestRateLimitGateWaitsUntilDeadline(t *testing.T) {
	clock := newFakeClock()
	g := newFakeGate(clock)

	if err := g.wait(context.Background()); err != nil {
		t.Fatalf("open gate should not block: %v", err)
	}
	if got := clock.sleeps(); len(got) != 0 {
		t.Fatalf("open gate slept %v", got)
	}

	g.closeUntil(30 * time.Second)
	if err := g.wait(context.Background()); err != nil {
		t.Fatalf("wait: %v", err)
	}

	got := clock.sleeps()
	if len(got) != 1 || got[0] != 30*time.Second {
		t.Fatalf("slept %v, want [30s]", got)
	}
}

func TestRateLimitGateCloseUntilNeverShortens(t *testing.T) {
	clock := newFakeClock()
	g := newFakeGate(clock)

	g.closeUntil(60 * time.Second)
	g.closeUntil(5 * time.Second)

	if got := g.deadline().Sub(testNow); got != 60*time.Second {
		t.Fatalf("deadline in %v, want 60s", got)
	}
}

func TestRateLimitGateHonorsContext(t *testing.T) {
	g := &rateLimitGate{
		now:   time.Now,
		after: func(time.Duration) <-chan time.Time { return make(chan time.Time) }, // never fires
	}
	g.closeUntil(time.Hour)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := g.wait(ctx); err != context.Canceled {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

// rateLimitTestServer answers 429 with the given reset header `fails` times,
// then 200, recording every request body it receives.
type rateLimitTestServer struct {
	*httptest.Server

	hits   atomic.Int32
	mu     sync.Mutex
	bodies []string
}

func newRateLimitTestServer(t *testing.T, fails int32, reset string) *rateLimitTestServer {
	t.Helper()

	s := &rateLimitTestServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		s.bodies = append(s.bodies, string(body))
		s.mu.Unlock()

		if s.hits.Add(1) <= fails {
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
	t.Cleanup(s.Close)

	return s
}

func (s *rateLimitTestServer) requestBodies() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.bodies...)
}

func newTestTransport(clock *fakeClock) *rateLimitTransport {
	return &rateLimitTransport{
		base:        http.DefaultTransport,
		gate:        newFakeGate(clock),
		maxAttempts: rateLimitMaxAttempts,
		maxWait:     rateLimitMaxWait,
		jitter:      func(time.Duration) time.Duration { return 0 },
	}
}

func TestRateLimitTransportRetriesUsingResetHeader(t *testing.T) {
	srv := newRateLimitTestServer(t, 1, "30")
	clock := newFakeClock()
	client := &http.Client{Transport: newTestTransport(clock)}

	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := srv.hits.Load(); got != 2 {
		t.Fatalf("hits = %d, want 2", got)
	}

	slept := clock.sleeps()
	if len(slept) != 1 || slept[0] != 30*time.Second {
		t.Fatalf("slept %v, want [30s] taken from X-RateLimit-Reset", slept)
	}
}

func TestRateLimitTransportFallsBackToBackoffWithoutHeaders(t *testing.T) {
	srv := newRateLimitTestServer(t, 1, "")
	clock := newFakeClock()
	client := &http.Client{Transport: newTestTransport(clock)}

	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	slept := clock.sleeps()
	if len(slept) != 1 || slept[0] != rateLimitBaseDelay {
		t.Fatalf("slept %v, want [%v]", slept, rateLimitBaseDelay)
	}
}

func TestRateLimitTransportSurfaces429WhenAttemptsExhausted(t *testing.T) {
	srv := newRateLimitTestServer(t, 99, "5")
	client := &http.Client{Transport: newTestTransport(newFakeClock())}

	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatalf("expected the 429 response, got error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", resp.StatusCode)
	}
	if got := srv.hits.Load(); got != rateLimitMaxAttempts {
		t.Fatalf("hits = %d, want %d", got, rateLimitMaxAttempts)
	}

	// The caller needs the body to build a diagnostic, so it must still be readable.
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading the surfaced body: %v", err)
	}
	if !strings.Contains(string(body), "Rate limit") {
		t.Fatalf("body = %q, want the API message", body)
	}
}

func TestRateLimitTransportRefusesToWaitLongerThanMaxWait(t *testing.T) {
	srv := newRateLimitTestServer(t, 99, "3600")
	clock := newFakeClock()
	client := &http.Client{Transport: newTestTransport(clock)}

	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", resp.StatusCode)
	}
	if got := srv.hits.Load(); got != 1 {
		t.Fatalf("hits = %d, want 1: a window past the cap must not be waited out", got)
	}
	if got := clock.sleeps(); len(got) != 0 {
		t.Fatalf("slept %v, want no waiting", got)
	}
}

func TestRateLimitTransportReplaysRequestBody(t *testing.T) {
	srv := newRateLimitTestServer(t, 1, "10")
	client := &http.Client{Transport: newTestTransport(newFakeClock())}

	resp, err := client.Post(srv.URL, "application/json", strings.NewReader(`{"name":"x"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()

	if got := srv.hits.Load(); got != 2 {
		t.Fatalf("hits = %d, want 2", got)
	}

	bodies := srv.requestBodies()
	if len(bodies) != 2 {
		t.Fatalf("recorded %d bodies, want 2", len(bodies))
	}
	for i, b := range bodies {
		if b != `{"name":"x"}` {
			t.Errorf("attempt %d body = %q, want the original payload", i+1, b)
		}
	}
}

func TestRateLimitTransportSharesTheWindowAcrossGoroutines(t *testing.T) {
	// Every request would be rate limited on its first try. With a shared gate
	// only the first goroutine pays for the discovery; the rest wait for the
	// window it recorded instead of each spending an attempt of their own.
	var hits atomic.Int32
	var limited atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		if limited.Add(1) == 1 {
			w.Header().Set("X-RateLimit-Reset", "20")
			w.WriteHeader(http.StatusTooManyRequests)

			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := &http.Client{Transport: newTestTransport(newFakeClock())}

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

	if got := hits.Load(); got != 11 {
		t.Fatalf("hits = %d, want 11 (10 requests plus exactly one retry)", got)
	}
}

func TestRateLimitTransportHonorsContextCancellation(t *testing.T) {
	srv := newRateLimitTestServer(t, 99, "60")

	tr := &rateLimitTransport{
		base:        http.DefaultTransport,
		gate:        &rateLimitGate{now: time.Now, after: func(time.Duration) <-chan time.Time { return make(chan time.Time) }},
		maxAttempts: rateLimitMaxAttempts,
		maxWait:     rateLimitMaxWait,
		jitter:      func(time.Duration) time.Duration { return 0 },
	}
	client := &http.Client{Transport: tr}

	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatalf("building request: %v", err)
	}

	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	if _, err := client.Do(req); err == nil {
		t.Fatal("expected an error once the context is cancelled")
	}
}

func TestJitterUpToStaysWithinBounds(t *testing.T) {
	for _, d := range []time.Duration{0, -time.Second, 100 * time.Millisecond, time.Hour} {
		got := jitterUpTo(d)
		if got < 0 {
			t.Fatalf("jitterUpTo(%v) = %v, want non-negative", d, got)
		}
		if got >= jitterCap {
			t.Fatalf("jitterUpTo(%v) = %v, want less than %v", d, got, jitterCap)
		}
		if d > 0 && d < jitterCap && got >= d {
			t.Fatalf("jitterUpTo(%v) = %v, want less than the delay itself", d, got)
		}
	}
}

func TestNewHTTPClientIsRateLimitAware(t *testing.T) {
	client := newHTTPClient()

	tr, ok := client.Transport.(*rateLimitTransport)
	if !ok {
		t.Fatalf("transport = %T, want *rateLimitTransport", client.Transport)
	}
	if tr.gate == nil {
		t.Fatal("transport has no gate, so concurrent requests would not share a window")
	}
	if tr.maxAttempts != rateLimitMaxAttempts {
		t.Errorf("maxAttempts = %d, want %d", tr.maxAttempts, rateLimitMaxAttempts)
	}
	if tr.maxWait != rateLimitMaxWait {
		t.Errorf("maxWait = %v, want %v", tr.maxWait, rateLimitMaxWait)
	}
	if tr.jitter == nil {
		t.Error("transport has no jitter, so requests would all resume together")
	}
}

func TestProviderConfigureUsesRateLimitAwareClient(t *testing.T) {
	// The generated client's configuration is unexported, so only the internal
	// client is observable here. That both are built from the same
	// newHTTPClient() call is what makes them share a window; the sharing
	// itself is covered by TestRateLimitTransportSharesTheWindowAcrossGoroutines.
	raw := map[string]interface{}{"username": "user", "password": "pass"}

	meta, err := providerConfigure(schema.TestResourceDataRaw(t, Provider().Schema, raw))
	if err != nil {
		t.Fatalf("configuring provider: %v", err)
	}

	clients := meta.(Clients)

	if _, ok := clients.httpClient.HTTPClient.Transport.(*rateLimitTransport); !ok {
		t.Fatalf("internal transport = %T, want *rateLimitTransport", clients.httpClient.HTTPClient.Transport)
	}
	if clients.members == nil {
		t.Fatal("provider has no workspace member cache, so every resource would re-page the members endpoint")
	}
}

func TestNewHTTPClientGivesEachProviderItsOwnGate(t *testing.T) {
	first := newHTTPClient().Transport.(*rateLimitTransport)
	second := newHTTPClient().Transport.(*rateLimitTransport)

	if first.gate == second.gate {
		t.Fatal("two provider instances share a gate; a limit on one workspace would stall the other")
	}
}

func TestRateLimitTransportThrottlesOnNearLimitWarning(t *testing.T) {
	// Authenticated Bitbucket responses carry no remaining or reset counter,
	// only this warning, so it is the one chance to slow down before the wall.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-RateLimit-Limit", "1000")
		w.Header().Set("X-RateLimit-NearLimit", "true")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	clock := newFakeClock()
	client := &http.Client{Transport: newTestTransport(clock)}

	for i := 0; i < 2; i++ {
		resp, err := client.Get(srv.URL)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		resp.Body.Close()
	}

	// The first response closes the gate; the second request waits on it.
	slept := clock.sleeps()
	if len(slept) != 1 || slept[0] != rateLimitNearLimitCooldown {
		t.Fatalf("slept %v, want [%v]", slept, rateLimitNearLimitCooldown)
	}
}

func TestRateLimitTransportDoesNotThrottleWithoutWarning(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-RateLimit-NearLimit", "false")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	clock := newFakeClock()
	client := &http.Client{Transport: newTestTransport(clock)}

	for i := 0; i < 2; i++ {
		resp, err := client.Get(srv.URL)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		resp.Body.Close()
	}

	if got := clock.sleeps(); len(got) != 0 {
		t.Fatalf("slept %v, want no throttling", got)
	}
}

func TestRateLimitTransportEscalatesBackoffWithoutHeaders(t *testing.T) {
	srv := newRateLimitTestServer(t, 99, "")
	clock := newFakeClock()
	client := &http.Client{Transport: newTestTransport(clock)}

	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()

	want := []time.Duration{
		rateLimitBaseDelay,
		rateLimitBaseDelay * 2,
		rateLimitBaseDelay * 4,
		rateLimitBaseDelay * 8,
	}

	got := clock.sleeps()
	if len(got) != len(want) {
		t.Fatalf("slept %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("wait %d = %v, want %v", i+1, got[i], want[i])
		}
	}
}
