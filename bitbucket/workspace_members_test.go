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

	"github.com/DrFaust92/bitbucket-go-client"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

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

func TestWorkspaceMemberCachePaginatesAndCaches(t *testing.T) {
	rt := &cursorPagesTransport{pages: map[string]string{
		"/2.0/workspaces/noogadev/members":               `{"values":[{"user":{"uuid":"{u1}","display_name":"Jane Doe"}}],"next":"https://api.bitbucket.org/2.0/workspaces/noogadev/members?page=Y3Vyc29y"}`,
		"/2.0/workspaces/noogadev/members?page=Y3Vyc29y": `{"values":[{"user":{"uuid":"{u2}","display_name":"John Smith"}}]}`,
	}}

	clients := clientsWithTransport(rt)
	cache := newWorkspaceMemberCache()

	index, err := cache.uuidsByDisplayName(clients, "noogadev")
	if err != nil {
		t.Fatalf("unexpected error: %v (requests: %v)", err, rt.seen)
	}
	if index["Jane Doe"] != "{u1}" || index["John Smith"] != "{u2}" {
		t.Fatalf("index = %+v, want members from both pages", index)
	}
	if got := len(rt.seen); got != 2 {
		t.Fatalf("requests = %v, want 2 (both pages fetched)", rt.seen)
	}

	if _, err = cache.uuidsByDisplayName(clients, "noogadev"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := len(rt.seen); got != 2 {
		t.Fatalf("requests = %v, want 2 (the second lookup must come from the cache)", rt.seen)
	}
}

func TestWorkspaceMemberCacheFetchesOnceUnderConcurrency(t *testing.T) {
	var calls atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"page":1,"values":[{"user":{"uuid":"{u1}","display_name":"Jane Doe"}}]}`)
	}))
	defer srv.Close()

	pc := Clients{genClient: testProviderConfig(t, srv.URL)}
	cache := newWorkspaceMemberCache()

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			if _, err := cache.uuidsByDisplayName(pc, "noogadev"); err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	wg.Wait()

	if got := calls.Load(); got != 1 {
		t.Fatalf("calls = %d, want 1: concurrent resources must share one walk", got)
	}
}

func TestWorkspaceMemberCacheRejectsOnlyTheSharedDisplayName(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"page":1,"values":[
			{"user":{"uuid":"{u1}","display_name":"Jane Doe"}},
			{"user":{"uuid":"{u2}","display_name":"Jane Doe"}},
			{"user":{"uuid":"{u3}","display_name":"John Roe"}}
		]}`)
	}))
	defer srv.Close()

	cache := newWorkspaceMemberCache()
	pc := Clients{genClient: testProviderConfig(t, srv.URL)}

	accounts, err := cache.resolve(pc, "noogadev", schema.NewSet(schema.HashString, []interface{}{"John Roe"}))
	if err != nil {
		t.Fatalf("a unique name must resolve next to a shared one: %v", err)
	}
	if len(accounts) != 1 || accounts[0].Uuid != "{u3}" {
		t.Fatalf("accounts = %v, want {u3}", accounts)
	}

	_, err = cache.resolve(pc, "noogadev", schema.NewSet(schema.HashString, []interface{}{"Jane Doe"}))
	if err == nil {
		t.Fatal("expected an error for a shared display name")
	}
	for _, want := range []string{"Jane Doe", "{u1}", "{u2}"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error should name %s, got %q", want, err)
		}
	}
}

func TestWorkspaceMemberCacheResolvesDisplayNames(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"page":1,"values":[{"user":{"uuid":"{u1}","display_name":"Jane Doe"}}]}`)
	}))
	defer srv.Close()

	names := schema.NewSet(schema.HashString, []interface{}{"Jane Doe"})

	accounts, err := newWorkspaceMemberCache().resolve(Clients{genClient: testProviderConfig(t, srv.URL)}, "noogadev", names)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(accounts) != 1 || accounts[0].Uuid != "{u1}" {
		t.Fatalf("accounts = %+v, want one account with UUID {u1}", accounts)
	}
}

func TestWorkspaceMemberCacheResolvePassesUUIDsThrough(t *testing.T) {
	// A UUID needs no lookup, so a configuration using them must not cost a
	// single request to the members endpoint.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("resolve must not call the API when every user is already a UUID")
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	names := schema.NewSet(schema.HashString, []interface{}{"{11111111-1111-1111-1111-111111111111}"})

	accounts, err := newWorkspaceMemberCache().resolve(Clients{genClient: testProviderConfig(t, srv.URL)}, "noogadev", names)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(accounts) != 1 || accounts[0].Uuid != "{11111111-1111-1111-1111-111111111111}" {
		t.Fatalf("accounts = %+v, want the UUID passed through", accounts)
	}
}

func TestWorkspaceMemberCacheReportsUnknownName(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"page":1,"values":[{"user":{"uuid":"{u1}","display_name":"Jane Doe"}}]}`)
	}))
	defer srv.Close()

	names := schema.NewSet(schema.HashString, []interface{}{"Nobody Here"})

	_, err := newWorkspaceMemberCache().resolve(Clients{genClient: testProviderConfig(t, srv.URL)}, "noogadev", names)
	if err == nil {
		t.Fatal("expected an error for an unknown display name")
	}
	if !strings.Contains(err.Error(), "Nobody Here") {
		t.Fatalf("error should name the missing member, got %q", err)
	}
}

func TestWorkspaceMemberCacheResolveEmptySet(t *testing.T) {
	accounts, err := newWorkspaceMemberCache().resolve(Clients{}, "noogadev", schema.NewSet(schema.HashString, nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(accounts) != 0 {
		t.Fatalf("accounts = %+v, want none", accounts)
	}
}
