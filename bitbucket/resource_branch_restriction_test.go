package bitbucket

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"testing"

	"github.com/DrFaust92/bitbucket-go-client"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/acctest"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/resource"
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
