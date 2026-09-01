package bitbucket

import (
	"bytes"
	"io"
	"log"
	"math/rand"
	"net/http"
	"strconv"
	"time"
)

// RetryTransport wraps an http.RoundTripper and retries requests that receive HTTP 429 Too Many Requests.
type RetryTransport struct {
	Base       http.RoundTripper
	MaxRetries int
	BaseDelay  time.Duration
	MaxDelay   time.Duration
}

// NewRetryTransport creates a new RetryTransport with sensible defaults.
func NewRetryTransport(base http.RoundTripper) *RetryTransport {
	if base == nil {
		base = http.DefaultTransport
	}
	return &RetryTransport{
		Base:       base,
		MaxRetries: 7,
		BaseDelay:  500 * time.Millisecond,
		MaxDelay:   20 * time.Second,
	}
}

// RoundTrip executes a single HTTP transaction and retries on 429 status code.
func (t *RetryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Buffer request body in memory if present to enable retries
	var bodyBytes []byte
	if req.Body != nil {
		var err error
		bodyBytes, err = io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		_ = req.Body.Close()
	}

	var resp *http.Response
	var err error

	for attempt := 0; attempt <= t.MaxRetries; attempt++ {
		// Restore body for every attempt if it was present
		if bodyBytes != nil {
			req.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		}

		resp, err = t.Base.RoundTrip(req)
		if err != nil {
			return resp, err
		}

		if resp.StatusCode != http.StatusTooManyRequests {
			return resp, nil
		}

		if attempt == t.MaxRetries {
			log.Printf("[WARN] HTTP 429 received from Bitbucket API, exhausted all %d retries for %s %s", t.MaxRetries, req.Method, req.URL)
			return resp, nil
		}

		// Calculate backoff delay
		delay := t.getDelay(resp, attempt)
		log.Printf("[INFO] HTTP 429 received from Bitbucket API. Retrying %s %s in %v (attempt %d/%d)", req.Method, req.URL, delay, attempt+1, t.MaxRetries)

		// Drain and close 429 response body before retrying to prevent connection leaks
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()

		select {
		case <-req.Context().Done():
			return nil, req.Context().Err()
		case <-time.After(delay):
		}
	}

	return resp, err
}

func (t *RetryTransport) getDelay(resp *http.Response, attempt int) time.Duration {
	if resp != nil {
		if retryAfter := resp.Header.Get("Retry-After"); retryAfter != "" {
			if seconds, err := strconv.Atoi(retryAfter); err == nil && seconds > 0 {
				return time.Duration(seconds) * time.Second
			}
			if targetTime, err := http.ParseTime(retryAfter); err == nil {
				if diff := time.Until(targetTime); diff > 0 {
					return diff
				}
			}
		}
	}

	multiplier := 1 << attempt
	delay := t.BaseDelay * time.Duration(multiplier)
	if delay > t.MaxDelay {
		delay = t.MaxDelay
	}

	// Add random jitter [0, 250ms]
	jitter := time.Duration(rand.Int63n(int64(250 * time.Millisecond)))
	return delay + jitter
}
