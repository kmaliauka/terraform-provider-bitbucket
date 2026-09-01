package bitbucket

import (
	"net/http"
	"testing"
)

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
			name: "429 with reset only",
			resp: &http.Response{
				StatusCode: http.StatusTooManyRequests,
				Header:     http.Header{"X-Ratelimit-Reset": {"30"}},
			},
			want: "rate limit exceeded, resets in 30s; reduce -parallelism or re-run after the window resets",
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

func TestHandleClientErrorExplainsRateLimit(t *testing.T) {
	resp := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Status:     "429 Too Many Requests",
		Header:     http.Header{"X-Ratelimit-Reset": {"156"}},
	}

	err := handleClientError(resp, nil)
	if err == nil {
		t.Fatal("expected an error for a 429 response")
	}

	want := "429 Too Many Requests: rate limit exceeded, resets in 156s; reduce -parallelism or re-run after the window resets"
	if err.Error() != want {
		t.Fatalf("error = %q, want %q", err, want)
	}
}
