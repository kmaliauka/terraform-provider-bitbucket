package bitbucket

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/DrFaust92/bitbucket-go-client"
	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/acctest"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/resource"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/v2/terraform"
)

func TestAccBitbucketBranchRestriction_basic(t *testing.T) {
	rName := acctest.RandomWithPrefix("tf-test")
	testUser := os.Getenv("BITBUCKET_TEAM")
	resourceName := "bitbucket_branch_restriction.test"

	resource.Test(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckBitbucketBranchRestrictionDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccBitbucketBranchRestrictionConfig(testUser, rName),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckBitbucketBranchRestrictionExists(resourceName),
					resource.TestCheckResourceAttrPair(resourceName, "repository", "bitbucket_repository.test", "name"),
					resource.TestCheckResourceAttr(resourceName, "kind", "force"),
					resource.TestCheckResourceAttr(resourceName, "pattern", "master"),
					resource.TestCheckResourceAttr(resourceName, "branch_match_kind", "glob"),
				),
			},
			{
				ResourceName:      resourceName,
				ImportState:       true,
				ImportStateIdFunc: testAccCheckBitbucketBranchRestrictionImportStateIdFunc(resourceName),
				ImportStateVerify: true,
			},
		},
	})
}

func TestAccBitbucketBranchRestriction_model(t *testing.T) {
	rName := acctest.RandomWithPrefix("tf-test")
	testUser := os.Getenv("BITBUCKET_TEAM")
	resourceName := "bitbucket_branch_restriction.test"

	resource.Test(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckBitbucketBranchRestrictionDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccBitbucketBranchRestrictionModelConfig(testUser, rName),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckBitbucketBranchRestrictionExists(resourceName),
					resource.TestCheckResourceAttrPair(resourceName, "repository", "bitbucket_repository.test", "name"),
					resource.TestCheckResourceAttr(resourceName, "kind", "force"),
					resource.TestCheckResourceAttr(resourceName, "pattern", ""),
					resource.TestCheckResourceAttr(resourceName, "branch_match_kind", "branching_model"),
					resource.TestCheckResourceAttr(resourceName, "branch_type", "production"),
				),
			},
			{
				ResourceName:      resourceName,
				ImportState:       true,
				ImportStateIdFunc: testAccCheckBitbucketBranchRestrictionImportStateIdFunc(resourceName),
				ImportStateVerify: true,
			},
		},
	})
}

func testAccBitbucketBranchRestrictionConfig(testUser, rName string) string {
	return fmt.Sprintf(`
resource "bitbucket_repository" "test" {
  owner = %[1]q
  name  = %[2]q
}
resource "bitbucket_branch_restriction" "test" {
  owner      = %[1]q
  repository = bitbucket_repository.test.name
  kind       = "force"
  pattern    = "master"
}
`, testUser, rName)
}

func testAccBitbucketBranchRestrictionModelConfig(testUser, rName string) string {
	return fmt.Sprintf(`
resource "bitbucket_repository" "test" {
  owner = %[1]q
  name  = %[2]q
}
resource "bitbucket_branch_restriction" "test" {
  owner             = %[1]q
  repository        = bitbucket_repository.test.name
  kind              = "force"
  branch_match_kind = "branching_model"
  branch_type       = "production"
}
`, testUser, rName)
}

func testAccCheckBitbucketBranchRestrictionDestroy(s *terraform.State) error {
	client := testAccProvider.Meta().(Clients).genClient
	brApi := client.ApiClient.BranchRestrictionsApi

	for _, rs := range s.RootModule().Resources {
		if rs.Type != "bitbucket_branch_restriction" {
			continue
		}

		_, res, err := brApi.RepositoriesWorkspaceRepoSlugBranchRestrictionsIdGet(client.AuthContext,
			url.PathEscape(rs.Primary.ID),
			rs.Primary.Attributes["repository"], rs.Primary.Attributes["owner"])

		if err == nil {
			return fmt.Errorf("The resource was found should have errored")
		}

		if res.StatusCode != http.StatusNotFound {
			return fmt.Errorf("BranchRestriction still exists")
		}
	}

	return nil
}

func testAccCheckBitbucketBranchRestrictionExists(n string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[n]
		if !ok {
			return fmt.Errorf("Not found %s", n)
		}
		if rs.Primary.ID == "" {
			return fmt.Errorf("No BranchRestriction ID is set")
		}
		return nil
	}
}

func testAccCheckBitbucketBranchRestrictionImportStateIdFunc(resourceName string) resource.ImportStateIdFunc {
	return func(s *terraform.State) (string, error) {
		rs, ok := s.RootModule().Resources[resourceName]
		if !ok {
			return "", fmt.Errorf("Not found: %s", resourceName)
		}
		return fmt.Sprintf("%s/%s/%s", rs.Primary.Attributes["owner"], rs.Primary.Attributes["repository"], rs.Primary.ID), nil
	}
}

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
		// The workspace is the reliable source for the slug the schema wants;
		// the owner account carries a display name, not a slug.
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

func TestBranchRestrictionKeyFromAPI(t *testing.T) {
	cases := []struct {
		name string
		in   bitbucket.Branchrestriction
		want branchRestrictionKey
	}{
		{
			name: "glob keeps the pattern and ignores branch type",
			in:   bitbucket.Branchrestriction{Kind: "push", BranchMatchKind: "glob", Pattern: "master"},
			want: branchRestrictionKey{Kind: "push", BranchMatchKind: "glob", Pattern: "master"},
		},
		{
			name: "branching model keeps the branch type and ignores the pattern",
			in:   bitbucket.Branchrestriction{Kind: "push", BranchMatchKind: "branching_model", BranchType: "development", Pattern: "ignored"},
			want: branchRestrictionKey{Kind: "push", BranchMatchKind: "branching_model", BranchType: "development"},
		},
		{
			name: "an empty match kind defaults to glob, the API default",
			in:   bitbucket.Branchrestriction{Kind: "delete", Pattern: "*"},
			want: branchRestrictionKey{Kind: "delete", BranchMatchKind: "glob", Pattern: "*"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := branchRestrictionKeyFromAPI(tc.in); got != tc.want {
				t.Fatalf("key = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestBranchRestrictionKeyDistinguishesMatchKinds(t *testing.T) {
	// Bitbucket allows the same kind twice as long as the match differs, so a
	// key that ignored branch_match_kind would adopt the wrong restriction.
	glob := branchRestrictionKeyFromAPI(bitbucket.Branchrestriction{
		Kind: "push", BranchMatchKind: "glob", Pattern: "development",
	})
	model := branchRestrictionKeyFromAPI(bitbucket.Branchrestriction{
		Kind: "push", BranchMatchKind: "branching_model", BranchType: "development",
	})

	if glob == model {
		t.Fatal("a glob pattern and a branching model type must not compare equal")
	}
}

func TestFindBranchRestrictionByKeySecondPage(t *testing.T) {
	var pages atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pages.Add(1)
		w.Header().Set("Content-Type", "application/json")

		if r.URL.Query().Get("page") == "2" {
			_, _ = io.WriteString(w, `{"page":2,"values":[
				{"id":79861086,"kind":"require_approvals_to_merge","branch_match_kind":"branching_model","branch_type":"development","value":1}
			]}`)

			return
		}

		_, _ = io.WriteString(w, `{"page":1,"next":"?page=2","values":[
			{"id":11111111,"kind":"require_approvals_to_merge","branch_match_kind":"glob","pattern":"master","value":1}
		]}`)
	}))
	defer srv.Close()

	key := newBranchRestrictionKey("require_approvals_to_merge", "branching_model", "", "development")

	got, err := findBranchRestrictionByKey(testProviderConfig(t, srv.URL), "ws", "repo", key)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil {
		t.Fatal("a restriction on the second page was not found")
	}
	if got.Id != 79861086 {
		t.Fatalf("id = %d, want 79861086", got.Id)
	}
	if pages.Load() != 2 {
		t.Fatalf("pages fetched = %d, want 2", pages.Load())
	}
}

func TestFindBranchRestrictionByKeyIgnoresSameKindDifferentMatch(t *testing.T) {
	// Same kind, but a glob restriction must never be mistaken for a branching
	// model one: adopting it would rewrite the wrong object.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"page":1,"values":[
			{"id":11111111,"kind":"push","branch_match_kind":"glob","pattern":"development"}
		]}`)
	}))
	defer srv.Close()

	key := newBranchRestrictionKey("push", "branching_model", "", "development")

	got, err := findBranchRestrictionByKey(testProviderConfig(t, srv.URL), "ws", "repo", key)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Fatalf("expected no match, got id %d", got.Id)
	}
}

func TestFindBranchRestrictionByKeyMissing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"page":1,"values":[]}`)
	}))
	defer srv.Close()

	key := newBranchRestrictionKey("push", "glob", "master", "")

	got, err := findBranchRestrictionByKey(testProviderConfig(t, srv.URL), "ws", "repo", key)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil, got %+v", got)
	}
}

func TestBranchRestrictionReadRecoversChangedID(t *testing.T) {
	// Reproduces what happened to org-jenkins-mpl: the restriction was deleted
	// and recreated by hand, so the stored id 404s while the restriction itself
	// is still there under a new one.
	const (
		staleID = "69728586"
		freshID = "79861086"
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if strings.HasSuffix(r.URL.Path, "/branch-restrictions/"+staleID) {
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"type":"error","error":{"message":"Not found"}}`)

			return
		}

		_, _ = io.WriteString(w, `{"page":1,"values":[
			{"id":79861086,"kind":"require_approvals_to_merge","branch_match_kind":"branching_model","branch_type":"development","value":2}
		]}`)
	}))
	defer srv.Close()

	r := resourceBranchRestriction()
	d := schema.TestResourceDataRaw(t, r.Schema, map[string]interface{}{
		"owner":             "ws",
		"repository":        "repo",
		"kind":              "require_approvals_to_merge",
		"branch_match_kind": "branching_model",
		"branch_type":       "development",
	})
	d.SetId(staleID)

	clients := Clients{genClient: testProviderConfig(t, srv.URL), members: newWorkspaceMemberCache()}

	if diags := resourceBranchRestrictionsRead(context.Background(), d, clients); diags.HasError() {
		t.Fatalf("read returned diagnostics: %v", diags)
	}

	if d.Id() != freshID {
		t.Fatalf("id = %q, want the restriction re-adopted under %s", d.Id(), freshID)
	}
	if got := d.Get("value").(int); got != 2 {
		t.Fatalf("value = %d, want 2 read from the recovered restriction", got)
	}
}

func TestBranchRestrictionReadDropsGenuinelyDeleted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if strings.Contains(r.URL.Path, "/branch-restrictions/") {
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"type":"error","error":{"message":"Not found"}}`)

			return
		}

		_, _ = io.WriteString(w, `{"page":1,"values":[]}`)
	}))
	defer srv.Close()

	r := resourceBranchRestriction()
	d := schema.TestResourceDataRaw(t, r.Schema, map[string]interface{}{
		"owner":             "ws",
		"repository":        "repo",
		"kind":              "push",
		"branch_match_kind": "glob",
		"pattern":           "master",
	})
	d.SetId("12345")

	clients := Clients{genClient: testProviderConfig(t, srv.URL), members: newWorkspaceMemberCache()}

	if diags := resourceBranchRestrictionsRead(context.Background(), d, clients); diags.HasError() {
		t.Fatalf("read returned diagnostics: %v", diags)
	}

	if d.Id() != "" {
		t.Fatalf("id = %q, want the resource removed from state", d.Id())
	}
}

func TestExistingBranchRestrictionID(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "the message Bitbucket actually returns",
			err:  errors.New("409 Conflict: a branch restriction with that kind (delete) and pattern (*) already exists (id=74671495)"),
			want: "74671495",
		},
		{
			name: "no id in the message",
			err:  errors.New("409 Conflict: something else"),
			want: "",
		},
		{
			name: "no error at all",
			err:  nil,
			want: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := existingBranchRestrictionID(tc.err); got != tc.want {
				t.Fatalf("id = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestBranchRestrictionDeleteRemovesRecreatedRestriction(t *testing.T) {
	// destroy must not leave a live restriction behind just because its id
	// changed: deleting the stale id 404s, and treating that as success would
	// drop the resource from state while the restriction stays in Bitbucket.
	const (
		staleID = "69728586"
		freshID = "79861086"
	)

	var deleted atomic.Value

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			id := path.Base(r.URL.Path)
			if id == staleID {
				w.WriteHeader(http.StatusNotFound)
				return
			}

			deleted.Store(id)
			w.WriteHeader(http.StatusNoContent)

			return
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"page":1,"values":[
			{"id":79861086,"kind":"require_approvals_to_merge","branch_match_kind":"branching_model","branch_type":"development","value":2}
		]}`)
	}))
	defer srv.Close()

	r := resourceBranchRestriction()
	d := schema.TestResourceDataRaw(t, r.Schema, map[string]interface{}{
		"owner":             "ws",
		"repository":        "repo",
		"kind":              "require_approvals_to_merge",
		"branch_match_kind": "branching_model",
		"branch_type":       "development",
	})
	d.SetId(staleID)

	clients := Clients{genClient: testProviderConfig(t, srv.URL), members: newWorkspaceMemberCache()}

	if diags := resourceBranchRestrictionsDelete(context.Background(), d, clients); diags.HasError() {
		t.Fatalf("delete returned diagnostics: %v", diags)
	}

	if got, _ := deleted.Load().(string); got != freshID {
		t.Fatalf("deleted id = %q, want the recreated restriction %s to be removed", got, freshID)
	}
}

func TestBranchRestrictionDeleteToleratesGenuinelyGone(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNotFound)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"page":1,"values":[]}`)
	}))
	defer srv.Close()

	r := resourceBranchRestriction()
	d := schema.TestResourceDataRaw(t, r.Schema, map[string]interface{}{
		"owner":             "ws",
		"repository":        "repo",
		"kind":              "push",
		"branch_match_kind": "glob",
		"pattern":           "master",
	})
	d.SetId("12345")

	clients := Clients{genClient: testProviderConfig(t, srv.URL), members: newWorkspaceMemberCache()}

	if diags := resourceBranchRestrictionsDelete(context.Background(), d, clients); diags.HasError() {
		t.Fatalf("deleting an already-gone restriction must succeed, got: %v", diags)
	}
}

func TestBranchRestrictionReadWarnsWhenAdopting(t *testing.T) {
	// Adopting a recreated restriction must not be silent: a branch restriction
	// is a security control, and its destruction is worth surfacing even when
	// the replacement matches the configuration.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if strings.HasSuffix(r.URL.Path, "/branch-restrictions/69728586") {
			w.WriteHeader(http.StatusNotFound)
			return
		}

		_, _ = io.WriteString(w, `{"page":1,"values":[
			{"id":79861086,"kind":"require_approvals_to_merge","branch_match_kind":"branching_model","branch_type":"development","value":2}
		]}`)
	}))
	defer srv.Close()

	r := resourceBranchRestriction()
	d := schema.TestResourceDataRaw(t, r.Schema, map[string]interface{}{
		"owner":             "ws",
		"repository":        "repo",
		"kind":              "require_approvals_to_merge",
		"branch_match_kind": "branching_model",
		"branch_type":       "development",
	})
	d.SetId("69728586")

	clients := Clients{genClient: testProviderConfig(t, srv.URL), members: newWorkspaceMemberCache()}

	diags := resourceBranchRestrictionsRead(context.Background(), d, clients)
	if diags.HasError() {
		t.Fatalf("read returned errors: %v", diags)
	}
	if len(diags) != 1 || diags[0].Severity != diag.Warning {
		t.Fatalf("diagnostics = %+v, want exactly one warning", diags)
	}
	if !strings.Contains(diags[0].Detail, "79861086") {
		t.Fatalf("warning should name the adopted id, got %q", diags[0].Detail)
	}
}

func TestBranchRestrictionReadIsQuietWhenNothingChanged(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":12345,"kind":"push","branch_match_kind":"glob","pattern":"master"}`)
	}))
	defer srv.Close()

	r := resourceBranchRestriction()
	d := schema.TestResourceDataRaw(t, r.Schema, map[string]interface{}{
		"owner":             "ws",
		"repository":        "repo",
		"kind":              "push",
		"branch_match_kind": "glob",
		"pattern":           "master",
	})
	d.SetId("12345")

	clients := Clients{genClient: testProviderConfig(t, srv.URL), members: newWorkspaceMemberCache()}

	if diags := resourceBranchRestrictionsRead(context.Background(), d, clients); len(diags) != 0 {
		t.Fatalf("a normal read must produce no diagnostics, got %+v", diags)
	}
}

func TestBranchRestrictionUpdateRecoversChangedID(t *testing.T) {
	// With -refresh=false, or when the restriction is recreated between plan and
	// apply, the id reaching Update is stale. It must recover rather than fail.
	var puts []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			id := path.Base(r.URL.Path)
			puts = append(puts, id)

			if id == "69728586" {
				w.WriteHeader(http.StatusNotFound)
				return
			}

			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"id":79861086,"kind":"require_approvals_to_merge","branch_match_kind":"branching_model","branch_type":"development","value":2}`)

			return
		}

		w.Header().Set("Content-Type", "application/json")

		// The Read that Update finishes with fetches the recovered id directly.
		if strings.HasSuffix(r.URL.Path, "/branch-restrictions/79861086") {
			_, _ = io.WriteString(w, `{"id":79861086,"kind":"require_approvals_to_merge","branch_match_kind":"branching_model","branch_type":"development","value":2}`)
			return
		}

		_, _ = io.WriteString(w, `{"page":1,"values":[
			{"id":79861086,"kind":"require_approvals_to_merge","branch_match_kind":"branching_model","branch_type":"development","value":2}
		]}`)
	}))
	defer srv.Close()

	r := resourceBranchRestriction()
	d := schema.TestResourceDataRaw(t, r.Schema, map[string]interface{}{
		"owner":             "ws",
		"repository":        "repo",
		"kind":              "require_approvals_to_merge",
		"branch_match_kind": "branching_model",
		"branch_type":       "development",
		"value":             2,
	})
	d.SetId("69728586")

	clients := Clients{genClient: testProviderConfig(t, srv.URL), members: newWorkspaceMemberCache()}

	if diags := resourceBranchRestrictionsUpdate(context.Background(), d, clients); diags.HasError() {
		t.Fatalf("update returned errors: %v", diags)
	}

	if len(puts) != 2 || puts[0] != "69728586" || puts[1] != "79861086" {
		t.Fatalf("PUT ids = %v, want the stale id tried then the recovered one", puts)
	}
	if d.Id() != "79861086" {
		t.Fatalf("id = %q, want 79861086", d.Id())
	}
}

func TestBranchRestrictionPriorKeyFallsBackWithoutADiff(t *testing.T) {
	// Update must recover by the key the resource held BEFORE the change:
	// kind, pattern and branch_type are not ForceNew, so a plan can move the
	// resource onto a key another restriction already occupies, and recovering
	// by the planned key would adopt and overwrite that foreign object.
	//
	// branchRestrictionPriorKey reads that prior value with d.GetChange, which
	// needs a diff. schema.TestResourceDataRaw cannot attach one, so the diff
	// path itself is only exercised by acceptance tests; what is checked here is
	// the documented fallback: with no diff, the current values are the prior
	// ones.
	r := resourceBranchRestriction()
	d := schema.TestResourceDataRaw(t, r.Schema, map[string]interface{}{
		"owner":             "ws",
		"repository":        "repo",
		"kind":              "push",
		"branch_match_kind": "glob",
		"pattern":           "master",
	})

	want := newBranchRestrictionKey("push", "glob", "master", "")

	if got := branchRestrictionPriorKey(d); got != want {
		t.Fatalf("prior key = %+v, want %+v", got, want)
	}
}

func TestBranchRestrictionCreateConflictPrefersTheLookupOverTheMessage(t *testing.T) {
	// The id quoted in a 409 is only a hint; nothing guarantees it refers to the
	// restriction this resource describes. The natural-key lookup is
	// authoritative, so when the two disagree the lookup must win.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusConflict)
			_, _ = io.WriteString(w, `{"type":"error","error":{"message":"a branch restriction with that kind (push) and pattern (master) already exists (id=111111)"}}`)

			return
		}

		_, _ = io.WriteString(w, `{"page":1,"values":[
			{"id":222222,"kind":"push","branch_match_kind":"glob","pattern":"master"}
		]}`)
	}))
	defer srv.Close()

	r := resourceBranchRestriction()
	d := schema.TestResourceDataRaw(t, r.Schema, map[string]interface{}{
		"owner":             "ws",
		"repository":        "repo",
		"kind":              "push",
		"branch_match_kind": "glob",
		"pattern":           "master",
	})

	clients := Clients{genClient: testProviderConfig(t, srv.URL), members: newWorkspaceMemberCache()}

	diags := resourceBranchRestrictionsCreate(context.Background(), d, clients)
	if !diags.HasError() {
		t.Fatal("creating a duplicate must fail rather than adopt the existing restriction")
	}

	msg := diags[0].Summary
	if !strings.Contains(msg, "222222") {
		t.Fatalf("error should name the id found by the lookup, got %q", msg)
	}
	if strings.Contains(msg, "111111") {
		t.Fatalf("error quoted the id from the message instead of the lookup: %q", msg)
	}
	if !strings.Contains(msg, "terraform import") {
		t.Fatalf("error should tell the operator to import, got %q", msg)
	}
	if d.Id() != "" {
		t.Fatalf("id = %q, want the resource left unclaimed", d.Id())
	}
}

func TestBranchRestrictionCreateConflictReportsLookupFailure(t *testing.T) {
	// A 429 or a scope problem during the lookup is the real root cause; losing
	// it behind the bare 409 leaves the operator with nothing to act on.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusConflict)
			_, _ = io.WriteString(w, `{"type":"error","error":{"message":"already exists"}}`)

			return
		}

		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"type":"error","error":{"message":"rate limited"}}`)
	}))
	defer srv.Close()

	r := resourceBranchRestriction()
	d := schema.TestResourceDataRaw(t, r.Schema, map[string]interface{}{
		"owner":             "ws",
		"repository":        "repo",
		"kind":              "push",
		"branch_match_kind": "glob",
		"pattern":           "master",
	})

	clients := Clients{genClient: testProviderConfig(t, srv.URL), members: newWorkspaceMemberCache()}

	diags := resourceBranchRestrictionsCreate(context.Background(), d, clients)
	if !diags.HasError() {
		t.Fatal("expected an error")
	}
	if !strings.Contains(diags[0].Summary, "looking the existing one up failed") {
		t.Fatalf("the lookup failure must be reported, got %q", diags[0].Summary)
	}
}

func TestBranchRestrictionUpdateFailsWhenRestrictionIsGone(t *testing.T) {
	// With -refresh=false the restriction can be deleted between plan and apply.
	// Update must say so rather than silently doing nothing.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			w.WriteHeader(http.StatusNotFound)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"page":1,"values":[]}`)
	}))
	defer srv.Close()

	r := resourceBranchRestriction()
	d := schema.TestResourceDataRaw(t, r.Schema, map[string]interface{}{
		"owner":             "ws",
		"repository":        "repo",
		"kind":              "push",
		"branch_match_kind": "glob",
		"pattern":           "master",
	})
	d.SetId("12345")

	clients := Clients{genClient: testProviderConfig(t, srv.URL), members: newWorkspaceMemberCache()}

	diags := resourceBranchRestrictionsUpdate(context.Background(), d, clients)
	if !diags.HasError() {
		t.Fatal("updating a restriction that no longer exists must fail")
	}
	if !strings.Contains(diags[0].Summary, "no longer exists") {
		t.Fatalf("error should say the restriction is gone, got %q", diags[0].Summary)
	}
}

func TestBranchRestrictionCreateConflictWithoutAnyIdSurfacesTheOriginalError(t *testing.T) {
	// Neither the lookup nor the message yields an id: the operator must still
	// get the API's own conflict rather than an empty diagnostic.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusConflict)
			_, _ = io.WriteString(w, `{"type":"error","error":{"message":"already exists but no identifier here"}}`)

			return
		}

		_, _ = io.WriteString(w, `{"page":1,"values":[]}`)
	}))
	defer srv.Close()

	r := resourceBranchRestriction()
	d := schema.TestResourceDataRaw(t, r.Schema, map[string]interface{}{
		"owner":             "ws",
		"repository":        "repo",
		"kind":              "push",
		"branch_match_kind": "glob",
		"pattern":           "master",
	})

	clients := Clients{genClient: testProviderConfig(t, srv.URL), members: newWorkspaceMemberCache()}

	diags := resourceBranchRestrictionsCreate(context.Background(), d, clients)
	if !diags.HasError() {
		t.Fatal("expected the conflict to be reported")
	}
	if !strings.Contains(diags[0].Summary, "no identifier here") {
		t.Fatalf("the API's own message must survive, got %q", diags[0].Summary)
	}
}
