package bitbucket

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"testing"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/acctest"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/resource"
	"github.com/hashicorp/terraform-plugin-sdk/v2/terraform"
)

func TestBranchingModelUnmarshalStringDefaultBranchDeletion(t *testing.T) {
	cases := []struct {
		name        string
		rawJSON     string
		expected    bool
		expectedNil bool
	}{
		{
			name: "string false",
			rawJSON: `{
				"development": {"name": "dev", "use_mainbranch": false},
				"production": {"name": "master", "use_mainbranch": false},
				"default_branch_deletion": "false"
			}`,
			expected: false,
		},
		{
			name: "string true",
			rawJSON: `{
				"development": {"name": "dev", "use_mainbranch": false},
				"production": {"name": "master", "use_mainbranch": false},
				"default_branch_deletion": "true"
			}`,
			expected: true,
		},
		{
			name: "native bool false",
			rawJSON: `{
				"development": {"name": "dev", "use_mainbranch": false},
				"production": {"name": "master", "use_mainbranch": false},
				"default_branch_deletion": false
			}`,
			expected: false,
		},
		{
			name: "native bool true",
			rawJSON: `{
				"development": {"name": "dev", "use_mainbranch": false},
				"production": {"name": "master", "use_mainbranch": false},
				"default_branch_deletion": true
			}`,
			expected: true,
		},
		{
			name: "null",
			rawJSON: `{
				"development": {"name": "dev", "use_mainbranch": false},
				"production": {"name": "master", "use_mainbranch": false},
				"default_branch_deletion": null
			}`,
			expectedNil: true,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			var bm BranchingModel
			if err := json.Unmarshal([]byte(tc.rawJSON), &bm); err != nil {
				t.Fatalf("failed to unmarshal: %v", err)
			}
			if tc.expectedNil {
				if bm.DefaultBranchDeletion != nil {
					t.Fatalf("expected DefaultBranchDeletion to be nil, got: %v", bm.DefaultBranchDeletion.Bool())
				}
				return
			}
			if bm.DefaultBranchDeletion == nil {
				t.Fatal("expected DefaultBranchDeletion to be non-nil")
			}
			if bm.DefaultBranchDeletion.Bool() != tc.expected {
				t.Fatalf("expected %v, got %v", tc.expected, bm.DefaultBranchDeletion.Bool())
			}
		})
	}
}

func TestAccBitbucketBranchingModel_basic(t *testing.T) {
	var branchRestriction BranchingModel
	rName := acctest.RandomWithPrefix("tf-test")
	testUser := os.Getenv("BITBUCKET_TEAM")
	resourceName := "bitbucket_branching_model.test"

	resource.Test(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckBitbucketBranchingModelDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccBitbucketBranchingModelConfig(testUser, rName),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckBitbucketBranchingModelExists(resourceName, &branchRestriction),
					resource.TestCheckResourceAttrPair(resourceName, "repository", "bitbucket_repository.test", "name"),
					resource.TestCheckResourceAttr(resourceName, "development.#", "1"),
					resource.TestCheckResourceAttr(resourceName, "development.0.use_mainbranch", "true"),
				),
			},
			{
				ResourceName:      resourceName,
				ImportState:       true,
				ImportStateVerify: true,
			},
		},
	})
}

func TestAccBitbucketBranchingModel_production(t *testing.T) {
	var branchRestriction BranchingModel
	rName := acctest.RandomWithPrefix("tf-test")
	testUser := os.Getenv("BITBUCKET_TEAM")
	resourceName := "bitbucket_branching_model.test"

	resource.Test(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckBitbucketBranchingModelDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccBitbucketBranchingModelProdConfig(testUser, rName),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckBitbucketBranchingModelExists(resourceName, &branchRestriction),
					resource.TestCheckResourceAttrPair(resourceName, "repository", "bitbucket_repository.test", "name"),
					resource.TestCheckResourceAttr(resourceName, "development.#", "1"),
					resource.TestCheckResourceAttr(resourceName, "development.0.use_mainbranch", "true"),
					resource.TestCheckResourceAttr(resourceName, "production.#", "1"),
					resource.TestCheckResourceAttr(resourceName, "production.0.use_mainbranch", "true"),
					resource.TestCheckResourceAttr(resourceName, "production.0.enabled", "true"),
				),
			},
			{
				ResourceName:      resourceName,
				ImportState:       true,
				ImportStateVerify: true,
			},
		},
	})
}

func TestAccBitbucketBranchingModel_branchTypes(t *testing.T) {
	var branchRestriction BranchingModel
	rName := acctest.RandomWithPrefix("tf-test")
	testUser := os.Getenv("BITBUCKET_TEAM")
	resourceName := "bitbucket_branching_model.test"

	resource.Test(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckBitbucketBranchingModelDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccBitbucketBranchingModelBranchTypesConfig1(testUser, rName),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckBitbucketBranchingModelExists(resourceName, &branchRestriction),
					resource.TestCheckResourceAttrPair(resourceName, "repository", "bitbucket_repository.test", "name"),
					resource.TestCheckResourceAttr(resourceName, "development.#", "1"),
					resource.TestCheckResourceAttr(resourceName, "development.0.use_mainbranch", "true"),
					resource.TestCheckTypeSetElemNestedAttrs(resourceName, "branch_type.*", map[string]string{
						"kind":   "feature",
						"prefix": "test/",
					}),
				),
			},
			{
				ResourceName:      resourceName,
				ImportState:       true,
				ImportStateVerify: true,
			},
		},
	})
}

func testAccBitbucketBranchingModelConfig(testUser, rName string) string {
	return fmt.Sprintf(`
resource "bitbucket_repository" "test" {
  owner = %[1]q
  name  = %[2]q
}
resource "bitbucket_branching_model" "test" {
  owner      = %[1]q
  repository = bitbucket_repository.test.name

  development {
    use_mainbranch = true
  }
}
`, testUser, rName)
}

func testAccBitbucketBranchingModelProdConfig(testUser, rName string) string {
	return fmt.Sprintf(`
resource "bitbucket_repository" "test" {
  owner = %[1]q
  name  = %[2]q
}
resource "bitbucket_branching_model" "test" {
  owner      = %[1]q
  repository = bitbucket_repository.test.name

  development {
    use_mainbranch = true
  }

  production {
    use_mainbranch = true
	enabled        = true
  }
}
`, testUser, rName)
}

func testAccBitbucketBranchingModelBranchTypesConfig1(testUser, rName string) string {
	return fmt.Sprintf(`
resource "bitbucket_repository" "test" {
  owner = %[1]q
  name  = %[2]q
}
resource "bitbucket_branching_model" "test" {
  owner      = %[1]q
  repository = bitbucket_repository.test.name

  development {
    use_mainbranch = true
  }

  branch_type {
    enabled = true
	kind    = "feature"
	prefix  = "test/"
  }

  branch_type {
    enabled = true
	kind    = "hotfix"
	prefix  = "hotfix/"
  }
 
  branch_type {
    enabled = true
	kind    = "release"
	prefix  = "release/"
  }
 
  branch_type {
    enabled = true
	kind    = "bugfix"
	prefix  = "bugfix/"
  }   
}
`, testUser, rName)
}

func testAccCheckBitbucketBranchingModelDestroy(s *terraform.State) error {
	client := testAccProvider.Meta().(Clients).httpClient
	for _, rs := range s.RootModule().Resources {
		if rs.Type != "bitbucket_branching_model" {
			continue
		}
		response, err := client.Get(fmt.Sprintf("2.0/repositories/%s/%s/branching-model", rs.Primary.Attributes["owner"], rs.Primary.Attributes["repository"]))

		if err == nil {
			return fmt.Errorf("The resource was found should have errored")
		}

		if response.StatusCode != http.StatusNotFound {
			return fmt.Errorf("Branching Model still exists")
		}
	}

	return nil
}

func testAccCheckBitbucketBranchingModelExists(n string, branchRestriction *BranchingModel) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[n]
		if !ok {
			return fmt.Errorf("Not found %s", n)
		}
		if rs.Primary.ID == "" {
			return fmt.Errorf("No BranchingModel ID is set")
		}
		return nil
	}
}
