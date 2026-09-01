package bitbucket

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/acctest"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/resource"
	"github.com/hashicorp/terraform-plugin-sdk/v2/terraform"
)

func TestAccBitbucketDeploymentVariable_basic(t *testing.T) {
	rName := acctest.RandomWithPrefix("tf-test")
	owner := os.Getenv("BITBUCKET_TEAM")
	resourceName := "bitbucket_deployment_variable.test"

	resource.Test(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckBitbucketDeploymentVariableDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccBitbucketDeploymentVariableConfig(owner, rName, "test", false),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckBitbucketDeploymentVariableExists(resourceName),
					resource.TestCheckResourceAttrPair(resourceName, "deployment", "bitbucket_deployment.test", "id"),
					resource.TestCheckResourceAttr(resourceName, "key", "test"),
					resource.TestCheckResourceAttr(resourceName, "value", "test"),
					resource.TestCheckResourceAttr(resourceName, "secured", "false"),
				),
			},
			{
				ResourceName:      resourceName,
				ImportState:       true,
				ImportStateIdFunc: testAccBitbucketDeploymentVariableImportStateIdFunc(resourceName),
				ImportStateVerify: true,
			},
			{
				Config: testAccBitbucketDeploymentVariableConfig(owner, rName, "test-2", false),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckBitbucketDeploymentVariableExists(resourceName),
					resource.TestCheckResourceAttrPair(resourceName, "deployment", "bitbucket_deployment.test", "id"),
					resource.TestCheckResourceAttr(resourceName, "key", "test"),
					resource.TestCheckResourceAttr(resourceName, "value", "test-2"),
					resource.TestCheckResourceAttr(resourceName, "secured", "false"),
				),
			},
		},
	})
}

func TestAccBitbucketDeploymentVariable_manyVars(t *testing.T) {
	rName := acctest.RandomWithPrefix("tf-test")
	owner := os.Getenv("BITBUCKET_TEAM")
	// resourceName := "bitbucket_deployment_variable.test[0]"

	resource.Test(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckBitbucketDeploymentVariableDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccBitbucketDeploymentVariableManyConfig(owner, rName, "test", false),
			},
		},
	})
}

func TestAccBitbucketDeploymentVariable_secure(t *testing.T) {
	rName := acctest.RandomWithPrefix("tf-test")
	owner := os.Getenv("BITBUCKET_TEAM")
	resourceName := "bitbucket_deployment_variable.test"

	resource.Test(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		Providers:    testAccProviders,
		CheckDestroy: testAccCheckBitbucketDeploymentVariableDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccBitbucketDeploymentVariableConfig(owner, rName, "test", true),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckBitbucketDeploymentVariableExists(resourceName),
					resource.TestCheckResourceAttrPair(resourceName, "deployment", "bitbucket_deployment.test", "id"),
					resource.TestCheckResourceAttr(resourceName, "key", "test"),
					resource.TestCheckResourceAttr(resourceName, "value", "test"),
					resource.TestCheckResourceAttr(resourceName, "secured", "true"),
				),
			},
			{
				ResourceName:            resourceName,
				ImportState:             true,
				ImportStateIdFunc:       testAccBitbucketDeploymentVariableImportStateIdFunc(resourceName),
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"value"},
			},
			{
				Config: testAccBitbucketDeploymentVariableConfig(owner, rName, "test", false),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckBitbucketDeploymentVariableExists(resourceName),
					resource.TestCheckResourceAttrPair(resourceName, "deployment", "bitbucket_deployment.test", "id"),
					resource.TestCheckResourceAttr(resourceName, "key", "test"),
					resource.TestCheckResourceAttr(resourceName, "value", "test"),
					resource.TestCheckResourceAttr(resourceName, "secured", "false"),
				),
			},
			{
				Config: testAccBitbucketDeploymentVariableConfig(owner, rName, "test", true),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckBitbucketDeploymentVariableExists(resourceName),
					resource.TestCheckResourceAttrPair(resourceName, "deployment", "bitbucket_deployment.test", "id"),
					resource.TestCheckResourceAttr(resourceName, "key", "test"),
					resource.TestCheckResourceAttr(resourceName, "value", "test"),
					resource.TestCheckResourceAttr(resourceName, "secured", "true"),
				),
			},
		},
	})
}

func testAccCheckBitbucketDeploymentVariableDestroy(s *terraform.State) error {
	client := testAccProvider.Meta().(Clients).genClient
	pipeApi := client.ApiClient.PipelinesApi
	for _, rs := range s.RootModule().Resources {
		if rs.Type != "bitbucket_deployment_variable" {
			continue
		}

		repository, deployment := parseDeploymentId(rs.Primary.Attributes["deployment"])
		workspace, repoSlug, err := deployVarId(repository)
		if err != nil {
			return err
		}

		_, res, err := pipeApi.GetDeploymentVariables(client.AuthContext, workspace, repoSlug, deployment, nil)

		if err == nil {
			return fmt.Errorf("The resource was found should have errored")
		}

		if res.StatusCode != http.StatusNotFound {
			return fmt.Errorf("Deployment Variable still exists")
		}
	}
	return nil
}

func testAccCheckBitbucketDeploymentVariableExists(n string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		_, ok := s.RootModule().Resources[n]

		if !ok {
			return fmt.Errorf("Not found %s", n)
		}

		return nil
	}
}

func testAccBitbucketDeploymentVariableConfig(owner, rName, val string, secure bool) string {
	return fmt.Sprintf(`
resource "bitbucket_repository" "test" {
  owner = %[1]q
  name  = %[2]q
}

resource "bitbucket_deployment" "test" {
  name       = %[2]q
  stage      = "Test"
  repository = bitbucket_repository.test.id
}

resource "bitbucket_deployment_variable" "test" {
  key        = "test"
  value      = %[3]q
  deployment = bitbucket_deployment.test.id
  secured    = %[4]t
}
`, owner, rName, val, secure)
}

func testAccBitbucketDeploymentVariableManyConfig(owner, rName, val string, secure bool) string {
	return fmt.Sprintf(`
resource "bitbucket_repository" "test" {
  owner = %[1]q
  name  = %[2]q
}

resource "bitbucket_deployment" "test" {
  name       = %[2]q
  stage      = "Test"
  repository = bitbucket_repository.test.id
}

resource "bitbucket_deployment_variable" "test" {
  count = 50

  key        = "test${count.index}"
  value      = %[3]q
  deployment = bitbucket_deployment.test.id
  secured    = %[4]t
}
`, owner, rName, val, secure)
}

func testAccBitbucketDeploymentVariableImportStateIdFunc(resourceName string) resource.ImportStateIdFunc {
	return func(s *terraform.State) (string, error) {
		rs, ok := s.RootModule().Resources[resourceName]
		if !ok {
			return "", fmt.Errorf("Not found: %s", resourceName)
		}
		return fmt.Sprintf("%s/%s", rs.Primary.Attributes["deployment"], rs.Primary.ID), nil
	}
}

func TestFindDeploymentVariableSecondPage(t *testing.T) {
	var pages atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pages.Add(1)
		w.Header().Set("Content-Type", "application/json")

		if r.URL.Query().Get("page") == "2" {
			_, _ = io.WriteString(w, `{"page":2,"size":11,"values":[
				{"uuid":"{wanted}","key":"LATE","value":"v","secured":false}
			]}`)

			return
		}

		_, _ = io.WriteString(w, `{"page":1,"size":11,"next":"?page=2","values":[
			{"uuid":"{other}","key":"EARLY","value":"v","secured":false}
		]}`)
	}))
	defer srv.Close()

	got, err := findDeploymentVariable(testProviderConfig(t, srv.URL), "ws", "repo", "{deploy}", "{wanted}")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil {
		t.Fatal("a variable on the second page was reported as missing")
	}
	if got.Key != "LATE" {
		t.Fatalf("key = %q, want LATE", got.Key)
	}
	if pages.Load() != 2 {
		t.Fatalf("pages fetched = %d, want 2", pages.Load())
	}
}

func TestFindDeploymentVariableMissing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"page":1,"size":1,"values":[{"uuid":"{other}","key":"X"}]}`)
	}))
	defer srv.Close()

	got, err := findDeploymentVariable(testProviderConfig(t, srv.URL), "ws", "repo", "{deploy}", "{missing}")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil, got %+v", got)
	}
}
