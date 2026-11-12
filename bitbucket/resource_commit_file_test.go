package bitbucket

import (
	"fmt"
	"os"
	"testing"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/acctest"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/resource"
)

func testAccBitbucketCommitFileConfig(owner, rName string) string {
	return fmt.Sprintf(`
resource "bitbucket_repository" "test" {
  owner = %[1]q
  name  = %[2]q
}

resource "bitbucket_commit_file" "test" {
	filename       = "README.md"
	content        = "abc"
	repo_slug      = bitbucket_repository.test.name
	workspace      = bitbucket_repository.test.owner
	commit_author  = "Unit test <unit@test.local>"
	branch         = "main"
	commit_message = "test"
  }
`, owner, rName)
}

func TestAccBitbucketCommitFile_basic(t *testing.T) {
	rName := acctest.RandomWithPrefix("tf-test")
	owner := os.Getenv("BITBUCKET_TEAM")
	resourceName := "bitbucket_commit_file.test"

	resource.Test(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckBitbucketDefaultReviewersDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccBitbucketCommitFileConfig(owner, rName),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckBitbucketDefaultReviewersExists(resourceName),
					resource.TestCheckResourceAttrPair(resourceName, "repository", "bitbucket_commit_file.test", "name"),
					resource.TestCheckResourceAttr(resourceName, "content", "abc"),
					resource.TestCheckResourceAttr(resourceName, "branch", "main"),
				),
			},
		},
	})
}
