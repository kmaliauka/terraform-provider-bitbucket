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
	var calls atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
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
		t.Fatalf("index = %+v, want members from both pages", index)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("calls = %d, want 2 (both pages fetched)", got)
	}

	if _, err = cache.uuidsByDisplayName(pc, "noogadev"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("calls = %d, want 2 (the second lookup must come from the cache)", got)
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

	pc := testProviderConfig(t, srv.URL)
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

func TestWorkspaceMemberCacheRejectsDuplicateDisplayNames(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"page":1,"values":[
			{"user":{"uuid":"{u1}","display_name":"Jane Doe"}},
			{"user":{"uuid":"{u2}","display_name":"Jane Doe"}}
		]}`)
	}))
	defer srv.Close()

	_, err := newWorkspaceMemberCache().uuidsByDisplayName(testProviderConfig(t, srv.URL), "noogadev")
	if err == nil {
		t.Fatal("expected an error for ambiguous display names")
	}
	if !strings.Contains(err.Error(), "Jane Doe") {
		t.Fatalf("error should name the ambiguous member, got %q", err)
	}
}

func TestWorkspaceMemberCacheResolvesDisplayNames(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"page":1,"values":[{"user":{"uuid":"{u1}","display_name":"Jane Doe"}}]}`)
	}))
	defer srv.Close()

	names := schema.NewSet(schema.HashString, []interface{}{"Jane Doe"})

	accounts, err := newWorkspaceMemberCache().resolve(testProviderConfig(t, srv.URL), "noogadev", names)
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

	accounts, err := newWorkspaceMemberCache().resolve(testProviderConfig(t, srv.URL), "noogadev", names)
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

	_, err := newWorkspaceMemberCache().resolve(testProviderConfig(t, srv.URL), "noogadev", names)
	if err == nil {
		t.Fatal("expected an error for an unknown display name")
	}
	if !strings.Contains(err.Error(), "Nobody Here") {
		t.Fatalf("error should name the missing member, got %q", err)
	}
}

func TestWorkspaceMemberCacheResolveEmptySet(t *testing.T) {
	accounts, err := newWorkspaceMemberCache().resolve(ProviderConfig{}, "noogadev", schema.NewSet(schema.HashString, nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(accounts) != 0 {
		t.Fatalf("accounts = %+v, want none", accounts)
	}
}
