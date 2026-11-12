package bitbucket

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/DrFaust92/bitbucket-go-client"
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

type notFoundTransport struct{}

func (notFoundTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusNotFound,
		Status:     "404 Not Found",
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"type":"error","error":{"message":"Repository not found"}}`)),
		Request:    r,
	}, nil
}

// clientsWithTransport routes both the generated and the raw client through one transport.
func clientsWithTransport(rt http.RoundTripper) Clients {
	httpClient := &http.Client{Transport: rt}
	conf := bitbucket.NewConfiguration()
	conf.HTTPClient = httpClient

	return Clients{
		genClient:  ProviderConfig{ApiClient: bitbucket.NewAPIClient(conf), AuthContext: context.Background()},
		httpClient: Client{HTTPClient: httpClient},
		members:    newWorkspaceMemberCache(),
	}
}

func TestDefaultReviewersReadRemovesResourceOnNotFound(t *testing.T) {
	clients := clientsWithTransport(notFoundTransport{})

	reads := map[string]struct {
		resource *schema.Resource
		id       string
	}{
		"repository": {resourceDefaultReviewers(), "ws/repo/reviewers"},
		"project":    {resourceProjectDefaultReviewers(), "ws/PRJ"},
	}

	for name, read := range reads {
		t.Run(name, func(t *testing.T) {
			d := read.resource.TestResourceData()
			d.SetId(read.id)

			if diags := read.resource.ReadWithoutTimeout(context.Background(), d, clients); diags.HasError() {
				t.Fatalf("expected no error, got %v", diags)
			}
			if d.Id() != "" {
				t.Fatal("expected the resource to be removed from state")
			}
		})
	}
}
