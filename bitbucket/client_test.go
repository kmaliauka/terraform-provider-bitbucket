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
