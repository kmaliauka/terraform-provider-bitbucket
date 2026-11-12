package bitbucket

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
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

func TestReadReturnsTransportError(t *testing.T) {
	clients := Clients{httpClient: Client{
		HTTPClient: &http.Client{Transport: errRoundTripper{err: errors.New("connection reset by peer")}},
	}}

	reads := map[string]struct {
		resource *schema.Resource
		id       string
	}{
		"hook":                        {resourceHook(), "{1}"},
		"workspace hook":              {resourceWorkspaceHook(), "{1}"},
		"project group permission":    {resourceProjectGroupPermission(), "workspace:KEY:group"},
		"project user permission":     {resourceProjectUserPermission(), "workspace:KEY:{1}"},
		"repository group permission": {resourceRepositoryGroupPermission(), "workspace:repo:group"},
		"repository user permission":  {resourceRepositoryUserPermission(), "workspace:repo:{1}"},
	}

	for name, read := range reads {
		t.Run(name, func(t *testing.T) {
			d := schema.TestResourceDataRaw(t, read.resource.Schema, map[string]interface{}{})
			d.SetId(read.id)

			if diags := read.resource.ReadWithoutTimeout(context.Background(), d, clients); !diags.HasError() {
				t.Fatal("expected an error, got none")
			}
		})
	}
}
