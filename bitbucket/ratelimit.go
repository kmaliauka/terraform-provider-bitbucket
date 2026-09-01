package bitbucket

import (
	"context"
	"fmt"
	"io"
	"log"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// rateLimitMaxAttempts is the total number of attempts per request,
	// including the first one.
	rateLimitMaxAttempts = 5

	// rateLimitMaxWait bounds how long a single request may block on a closed
	// rate limit window. Bitbucket windows are up to an hour long, and waiting
	// one out is indistinguishable from a hang, so past this point the 429 is
	// surfaced as a diagnostic instead.
	rateLimitMaxWait = 120 * time.Second

	// rateLimitBaseDelay is the first backoff step. Measured against the live
	// API, an authenticated 429 carries no X-RateLimit-Reset, so this escalating
	// backoff is the normal path rather than a fallback: waits run 5s, 10s, 20s,
	// 40s, which is bounded by rateLimitMaxWait at every step.
	rateLimitBaseDelay = 5 * time.Second

	// rateLimitNearLimitCooldown throttles the whole client for a moment when
	// Bitbucket warns the quota is nearly spent. Slowing down on the warning is
	// cheaper than discovering the wall and waiting it out.
	rateLimitNearLimitCooldown = time.Second

	// rateLimitDrainLimit caps how much of a discarded 429 body is read back
	// to make the connection reusable.
	rateLimitDrainLimit = 4096

	// jitterCap bounds the random spread added to a wait.
	jitterCap = 500 * time.Millisecond

	// epochThreshold tells a delta-seconds value from a unix timestamp.
	// Bitbucket sends delta-seconds, but the header is not contractual.
	epochThreshold = 1_000_000_000
)

// resetDelay reports how long to wait before retrying a rate limited request,
// when the response says so.
//
// Measured against the live API: an unauthenticated 429 carries
// X-RateLimit-Reset with the seconds left in the window, but an authenticated
// one — the path this provider actually uses — carries no reset and no
// remaining counter, only X-RateLimit-Limit and X-RateLimit-NearLimit. So this
// usually reports false and the caller backs off on its own schedule.
// Retry-After is honored too, for a proxy sitting in front of the API.
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

// rateLimitGate serializes rate limit waiting across every in-flight request.
//
// Terraform applies resources in parallel, so without a shared gate each of the
// N concurrent requests spends an attempt of its own rediscovering the same
// closed window, and all N resume on the same millisecond when it reopens.
// Here the first request to see a 429 records the deadline and the rest simply
// wait for it.
type rateLimitGate struct {
	mu    sync.Mutex
	until time.Time

	now   func() time.Time
	after func(time.Duration) <-chan time.Time
}

func newRateLimitGate() *rateLimitGate {
	return &rateLimitGate{now: time.Now, after: time.After}
}

// closeUntil closes the gate for at least d. A nearer deadline never shortens a
// further one.
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
// sleeping once, because another goroutine may extend the deadline while this
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

// rateLimitTransport retries HTTP 429 responses, waiting as long as the
// response asks for when it says so and backing off on an escalating schedule
// otherwise. Every wait is taken on the shared gate, so one request discovering
// the limit slows the whole client rather than each request finding it alone.
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
		if err != nil {
			return resp, err
		}

		if resp.StatusCode != http.StatusTooManyRequests {
			if nearRateLimit(resp.Header) {
				t.gate.closeUntil(rateLimitNearLimitCooldown)
			}

			return resp, nil
		}

		// A spent 429 is returned rather than swallowed: the caller knows the
		// endpoint and turns it into a diagnostic.
		if attempt >= t.maxAttempts {
			log.Printf("[WARN] Bitbucket rate limit on %s %s, giving up after %d attempts", req.Method, req.URL, attempt)
			return resp, nil
		}

		delay, ok := resetDelay(resp.Header, t.gate.now())
		if !ok {
			delay = rateLimitBaseDelay << (attempt - 1)
		}

		if delay > t.maxWait {
			log.Printf("[WARN] Bitbucket rate limit on %s %s resets in %s, past the %s cap", req.Method, req.URL, delay, t.maxWait)
			return resp, nil
		}

		// Draining before closing lets the connection be reused.
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, rateLimitDrainLimit))
		_ = resp.Body.Close()

		delay += t.jitter(delay)
		log.Printf("[INFO] Bitbucket rate limit on %s %s, waiting %s before attempt %d/%d", req.Method, req.URL, delay, attempt+1, t.maxAttempts)
		t.gate.closeUntil(delay)
	}
}

// nearRateLimit reports whether Bitbucket warned that the quota is nearly
// spent. It is the only proactive signal the API gives: authenticated
// responses carry X-RateLimit-Limit and X-RateLimit-NearLimit but no
// remaining or reset counter.
func nearRateLimit(h http.Header) bool {
	return strings.EqualFold(h.Get("X-RateLimit-NearLimit"), "true")
}

// rewindBody restores a consumed request body so the request can be replayed.
// http.NewRequest populates GetBody for the *bytes.Buffer payloads that both
// the internal client and the generated swagger client build.
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
	if d > jitterCap {
		d = jitterCap
	}
	if d <= 0 {
		return 0
	}

	return time.Duration(rand.Int64N(int64(d)))
}
