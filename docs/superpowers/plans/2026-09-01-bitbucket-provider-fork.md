# Bitbucket Terraform Provider Fork Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement critical bugfixes in `terraform-provider-bitbucket` (FlexBool unmarshaling, 429 burst retry backoff, and branch restriction user object handling), verify with unit tests under Go best practices, compile a local Darwin ARM64 binary, and validate end-to-end against `org-infra-bitbucket` via OpenTofu `dev_overrides`.

**Architecture:**
- Create `FlexBool` custom type implementing `json.Unmarshaler` to seamlessly parse strings, booleans, and nulls returned by Bitbucket Cloud API.
- Create `RetryTransport` implementing `http.RoundTripper` to intercept HTTP 429 responses and apply exponential backoff with jitter and `Retry-After` header parsing.
- Wire `RetryTransport` into both direct `Client.HTTPClient` and Swagger/SDK `bitbucket.Configuration.HTTPClient`.
- Fix branch restriction user account serialization so that full account objects returned by Bitbucket API are flattened to string UUIDs without state crashes.
- Compile local provider binary to `bin/terraform-provider-bitbucket` and test against `org-infra-bitbucket` without parallelism throttling.

**Tech Stack:**
- Go 1.26.5
- HashiCorp Terraform Plugin SDK v2
- OpenTofu 1.10.x
- Standard Go testing (`testing`, `net/http/httptest`)

## Global Constraints
- Target branch: `feature/noogadev-fixes` in `/Users/kirillmalyavko/Terraform/terraform-provider-bitbucket`.
- All Go code must adhere to `gofmt` and standard Go conventions.
- All test suites must run cleanly with `-race`.
- Commits must use `OI-0:` prefix per workspace conventions, with NO AI attribution (`Co-Authored-By:` prohibited).

---

### Task 1: Implement `FlexBool` with Comprehensive Table-Driven Tests

**Files:**
- Create: `bitbucket/flex_bool.go`
- Create: `bitbucket/flex_bool_test.go`

**Interfaces:**
- Produces:
  ```go
  type FlexBool bool
  func (fb *FlexBool) UnmarshalJSON(data []byte) error
  func (fb *FlexBool) Bool() bool
  ```

- [ ] **Step 1: Write failing table-driven tests for `FlexBool`**

Create `bitbucket/flex_bool_test.go`:
```go
package bitbucket

import (
	"encoding/json"
	"testing"
)

func TestFlexBoolUnmarshal(t *testing.T) {
	cases := []struct {
		name     string
		input    string
		expected bool
		wantErr  bool
	}{
		{"native true", `{"val": true}`, true, false},
		{"native false", `{"val": false}`, false, false},
		{"string true", `{"val": "true"}`, true, false},
		{"string false", `{"val": "false"}`, false, false},
		{"string uppercase true", `{"val": "TRUE"}`, true, false},
		{"string uppercase false", `{"val": "FALSE"}`, false, false},
		{"string 1", `{"val": "1"}`, true, false},
		{"string 0", `{"val": "0"}`, false, false},
		{"numeric 1", `{"val": 1}`, true, false},
		{"numeric 0", `{"val": 0}`, false, false},
		{"null", `{"val": null}`, false, false},
		{"empty string", `{"val": ""}`, false, false},
		{"invalid string", `{"val": "invalid"}`, false, true},
		{"invalid array", `{"val": []}`, false, true},
		{"invalid object", `{"val": {}}`, false, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var wrapper struct {
				Val FlexBool `json:"val"`
			}
			err := json.Unmarshal([]byte(tc.input), &wrapper)
			if (err != nil) != tc.wantErr {
				t.Fatalf("expected error: %v, got: %v", tc.wantErr, err)
			}
			if !tc.wantErr && wrapper.Val.Bool() != tc.expected {
				t.Fatalf("expected %v, got %v", tc.expected, wrapper.Val.Bool())
			}
		})
	}
}

func TestFlexBoolPointer(t *testing.T) {
	type wrapper struct {
		Val *FlexBool `json:"val,omitempty"`
	}

	var w wrapper
	if err := json.Unmarshal([]byte(`{"val": "false"}`), &w); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if w.Val == nil {
		t.Fatal("expected pointer to be non-nil")
	}
	if w.Val.Bool() != false {
		t.Fatalf("expected false, got %v", w.Val.Bool())
	}
}
```

- [ ] **Step 2: Run test to verify failure**

Run:
```bash
cd /Users/kirillmalyavko/Terraform/terraform-provider-bitbucket && go test -v ./bitbucket -run TestFlexBool
```
Expected: FAIL with compilation error `undefined: FlexBool`.

- [ ] **Step 3: Implement `FlexBool`**

Create `bitbucket/flex_bool.go`:
```go
package bitbucket

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
)

// FlexBool is a boolean that can be deserialized from JSON booleans, strings ("true", "false", "1", "0"), numbers, or null.
type FlexBool bool

// Bool returns the underlying bool primitive.
func (fb *FlexBool) Bool() bool {
	if fb == nil {
		return false
	}
	return bool(*fb)
}

// UnmarshalJSON unmarshals data into a FlexBool.
func (fb *FlexBool) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		*fb = false
		return nil
	}

	// Direct bool literal
	if bytes.Equal(trimmed, []byte("true")) {
		*fb = true
		return nil
	}
	if bytes.Equal(trimmed, []byte("false")) {
		*fb = false
		return nil
	}

	// String literal
	if len(trimmed) >= 2 && trimmed[0] == '"' && trimmed[len(trimmed)-1] == '"' {
		s := strings.TrimSpace(string(trimmed[1 : len(trimmed)-1]))
		if s == "" {
			*fb = false
			return nil
		}
		b, err := strconv.ParseBool(s)
		if err != nil {
			return fmt.Errorf("cannot unmarshal %q into FlexBool: %w", s, err)
		}
		*fb = FlexBool(b)
		return nil
	}

	// Number literal
	if b, err := strconv.ParseBool(string(trimmed)); err == nil {
		*fb = FlexBool(b)
		return nil
	}

	return fmt.Errorf("cannot unmarshal %s into FlexBool", string(trimmed))
}
```

- [ ] **Step 4: Run test to verify it passes**

Run:
```bash
cd /Users/kirillmalyavko/Terraform/terraform-provider-bitbucket && go test -v -race ./bitbucket -run TestFlexBool
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd /Users/kirillmalyavko/Terraform/terraform-provider-bitbucket
git add bitbucket/flex_bool.go bitbucket/flex_bool_test.go
git commit -m "OI-0: add FlexBool type and comprehensive unit tests"
```

---

### Task 2: Wire `FlexBool` into `BranchingModel` and `ProjectBranchingModel`

**Files:**
- Modify: `bitbucket/resource_branching_model.go`
- Modify: `bitbucket/resource_project_branching_model.go`

**Interfaces:**
- Consumes: `FlexBool` from Task 1.

- [ ] **Step 1: Write regression test for BranchingModel JSON decoding with string boolean**

Create test case in `bitbucket/resource_branching_model_test.go`:
```go
func TestBranchingModelUnmarshalStringDefaultBranchDeletion(t *testing.T) {
	rawJSON := `{
		"development": {"name": "dev", "use_mainbranch": false},
		"production": {"name": "master", "use_mainbranch": false},
		"default_branch_deletion": "false"
	}`

	var bm BranchingModel
	if err := json.Unmarshal([]byte(rawJSON), &bm); err != nil {
		t.Fatalf("failed to unmarshal BranchingModel with string default_branch_deletion: %v", err)
	}

	if bm.DefaultBranchDeletion == nil {
		t.Fatal("expected DefaultBranchDeletion to be non-nil")
	}
	if bm.DefaultBranchDeletion.Bool() != false {
		t.Fatalf("expected false, got %v", bm.DefaultBranchDeletion.Bool())
	}
}
```

- [ ] **Step 2: Update `BranchingModel` struct and usage in `resource_branching_model.go`**

In `bitbucket/resource_branching_model.go`:
Change:
```go
DefaultBranchDeletion *bool `json:"default_branch_deletion,omitempty"`
```
To:
```go
DefaultBranchDeletion *FlexBool `json:"default_branch_deletion,omitempty"`
```
And in `d.Set("default_branch_deletion", ...)`:
```go
if branchingModel.DefaultBranchDeletion != nil {
    d.Set("default_branch_deletion", branchingModel.DefaultBranchDeletion.Bool())
} else {
    d.Set("default_branch_deletion", nil)
}
```
And in request serialization where `default_branch_deletion` is constructed:
```go
if v, ok := d.GetOkExists("default_branch_deletion"); ok {
    val := FlexBool(v.(bool))
    branchingModel.DefaultBranchDeletion = &val
}
```

- [ ] **Step 3: Run branching model tests**

Run:
```bash
cd /Users/kirillmalyavko/Terraform/terraform-provider-bitbucket && go test -v -race ./bitbucket -run "TestBranchingModel"
```
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
cd /Users/kirillmalyavko/Terraform/terraform-provider-bitbucket
git add bitbucket/resource_branching_model.go bitbucket/resource_branching_model_test.go
git commit -m "OI-0: handle string and bool default_branch_deletion in BranchingModel"
```

---

### Task 3: Implement `RetryTransport` for Rate Limiting (HTTP 429)

**Files:**
- Create: `bitbucket/retry_transport.go`
- Create: `bitbucket/retry_transport_test.go`

**Interfaces:**
- Produces:
  ```go
  type RetryTransport struct {
      Base http.RoundTripper
      MaxRetries int
      BaseDelay time.Duration
      MaxDelay time.Duration
  }
  func NewRetryTransport(base http.RoundTripper) *RetryTransport
  func (t *RetryTransport) RoundTrip(req *http.Request) (*http.Response, error)
  ```

- [ ] **Step 1: Write tests for `RetryTransport` using `httptest.Server`**

Create `bitbucket/retry_transport_test.go`:
```go
package bitbucket

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestRetryTransportSuccessWithoutRetry(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer ts.Close()

	client := &http.Client{
		Transport: NewRetryTransport(http.DefaultTransport),
	}

	resp, err := client.Get(ts.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestRetryTransportRetriesOn429(t *testing.T) {
	var attempts int32

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := atomic.AddInt32(&attempts, 1)
		if count < 3 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	transport := NewRetryTransport(http.DefaultTransport)
	transport.BaseDelay = 5 * time.Millisecond
	transport.MaxDelay = 20 * time.Millisecond

	client := &http.Client{Transport: transport}
	resp, err := client.Get(ts.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK after retries, got %d", resp.StatusCode)
	}
	if atomic.LoadInt32(&attempts) != 3 {
		t.Fatalf("expected 3 attempts, got %d", attempts)
	}
}

func TestRetryTransportContextCancellation(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	transport := NewRetryTransport(http.DefaultTransport)
	transport.BaseDelay = 50 * time.Millisecond

	client := &http.Client{Transport: transport}
	req, _ := http.NewRequestWithContext(ctx, "GET", ts.URL, nil)

	_, err := client.Do(req)
	if err == nil {
		t.Fatal("expected context deadline error, got nil")
	}
}
```

- [ ] **Step 2: Run test to verify failure**

Run:
```bash
cd /Users/kirillmalyavko/Terraform/terraform-provider-bitbucket && go test -v ./bitbucket -run TestRetryTransport
```
Expected: FAIL with `undefined: NewRetryTransport`.

- [ ] **Step 3: Implement `RetryTransport`**

Create `bitbucket/retry_transport.go`:
```go
package bitbucket

import (
	"bytes"
	"io"
	"log"
	"math/rand"
	"net/http"
	"strconv"
	"time"
)

// RetryTransport wraps an http.RoundTripper and retries requests that receive HTTP 429 Too Many Requests.
type RetryTransport struct {
	Base       http.RoundTripper
	MaxRetries int
	BaseDelay  time.Duration
	MaxDelay   time.Duration
}

// NewRetryTransport creates a new RetryTransport with sensible defaults.
func NewRetryTransport(base http.RoundTripper) *RetryTransport {
	if base == nil {
		base = http.DefaultTransport
	}
	return &RetryTransport{
		Base:       base,
		MaxRetries: 7,
		BaseDelay:  500 * time.Millisecond,
		MaxDelay:   20 * time.Second,
	}
}

func (t *RetryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Buffer request body in memory if present to enable retries
	var bodyBytes []byte
	if req.Body != nil {
		var err error
		bodyBytes, err = io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		_ = req.Body.Close()
	}

	var resp *http.Response
	var err error

	for attempt := 0; attempt <= t.MaxRetries; attempt++ {
		if req.Body != nil {
			req.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		}

		resp, err = t.Base.RoundTrip(req)
		if err != nil {
			return resp, err
		}

		if resp.StatusCode != http.StatusTooManyRequests {
			return resp, nil
		}

		if attempt == t.MaxRetries {
			log.Printf("[WARN] HTTP 429 received from Bitbucket API, exhausted all %d retries for %s %s", t.MaxRetries, req.Method, req.URL)
			return resp, nil
		}

		// Calculate backoff delay
		delay := t.getDelay(resp, attempt)
		log.Printf("[INFO] HTTP 429 received from Bitbucket API. Retrying %s %s in %v (attempt %d/%d)", req.Method, req.URL, delay, attempt+1, t.MaxRetries)

		// Close 429 response body before retrying
		_ = resp.Body.Close()

		select {
		case <-req.Context().Done():
			return nil, req.Context().Err()
		case <-time.After(delay):
		}
	}

	return resp, err
}

func (t *RetryTransport) getDelay(resp *http.Response, attempt int) time.Duration {
	if resp != nil {
		if retryAfter := resp.Header.Get("Retry-After"); retryAfter != "" {
			if seconds, err := strconv.Atoi(retryAfter); err == nil && seconds > 0 {
				return time.Duration(seconds) * time.Second
			}
		}
	}

	multiplier := 1 << attempt
	delay := t.BaseDelay * time.Duration(multiplier)
	if delay > t.MaxDelay {
		delay = t.MaxDelay
	}

	// Add random jitter [0, 250ms]
	jitter := time.Duration(rand.Int63n(int64(250 * time.Millisecond)))
	return delay + jitter
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run:
```bash
cd /Users/kirillmalyavko/Terraform/terraform-provider-bitbucket && go test -v -race ./bitbucket -run TestRetryTransport
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd /Users/kirillmalyavko/Terraform/terraform-provider-bitbucket
git add bitbucket/retry_transport.go bitbucket/retry_transport_test.go
git commit -m "OI-0: add RetryTransport for HTTP 429 backoff"
```

---

### Task 4: Integrate `RetryTransport` into Provider Clients

**Files:**
- Modify: `bitbucket/provider.go:120-180`

**Interfaces:**
- Consumes: `NewRetryTransport` from Task 3.

- [ ] **Step 1: Update `providerConfigure` in `bitbucket/provider.go`**

In `bitbucket/provider.go`:
Wrap `client.HTTPClient.Transport` and `conf.HTTPClient.Transport` with `NewRetryTransport`:
```go
	retryTransport := NewRetryTransport(http.DefaultTransport)
	client := &Client{
		HTTPClient: &http.Client{
			Transport: retryTransport,
		},
	}
...
	conf := bitbucket.NewConfiguration()
	conf.HTTPClient = &http.Client{
		Transport: retryTransport,
	}
```

- [ ] **Step 2: Run all bitbucket package unit tests**

Run:
```bash
cd /Users/kirillmalyavko/Terraform/terraform-provider-bitbucket && go test -v -race ./bitbucket
```
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
cd /Users/kirillmalyavko/Terraform/terraform-provider-bitbucket
git add bitbucket/provider.go
git commit -m "OI-0: wire RetryTransport into provider HTTP clients"
```

---

### Task 5: Fix User Handling in Branch Restrictions

**Files:**
- Modify: `bitbucket/resource_branch_restriction.go`
- Test: `bitbucket/resource_branch_restriction_test.go`

**Interfaces:**
- Ensures `bitbucket.Account` maps cleanly to string UUID in `flattenUsers`.

- [ ] **Step 1: Check `flattenUsers` implementation in `resource_branch_restriction.go`**

Inspect and update `flattenUsers` so that if Bitbucket API returns an account object, its `UUID` string is extracted without type assertion failure.

- [ ] **Step 2: Run branch restriction unit tests**

Run:
```bash
cd /Users/kirillmalyavko/Terraform/terraform-provider-bitbucket && go test -v -race ./bitbucket -run "TestBranchRestriction"
```
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
cd /Users/kirillmalyavko/Terraform/terraform-provider-bitbucket
git add bitbucket/resource_branch_restriction.go bitbucket/resource_branch_restriction_test.go
git commit -m "OI-0: fix user account flattening in branch restrictions"
```

---

### Task 6: Compile Binary, Configure OpenTofu `dev_overrides`, and Validate Locally

**Files:**
- Output: `bin/terraform-provider-bitbucket`
- Config: `~/.tofurc`

- [ ] **Step 1: Compile provider binary for Darwin ARM64**

Run:
```bash
cd /Users/kirillmalyavko/Terraform/terraform-provider-bitbucket
mkdir -p bin
go build -o bin/terraform-provider-bitbucket main.go
```
Verify binary executable exists and is runnable.

- [ ] **Step 2: Configure OpenTofu `dev_overrides` in `~/.tofurc`**

Add `dev_overrides` block targeting `/Users/kirillmalyavko/Terraform/terraform-provider-bitbucket/bin`:
```hcl
provider_installation {
  dev_overrides {
    "DrFaust92/bitbucket" = "/Users/kirillmalyavko/Terraform/terraform-provider-bitbucket/bin"
  }
  direct {}
}
```

- [ ] **Step 3: Test `tofu plan` on `org-infra-bitbucket`**

Run:
```bash
cd /Users/kirillmalyavko/Terraform/org-infra-bitbucket
tofu plan -no-color
```
Verify:
1. OpenTofu prints `Overrides for provider "DrFaust92/bitbucket" are present in ~/.tofurc`.
2. No unmarshaling errors on branching model.
3. No 429 Too Many Requests errors.
4. Plan completes successfully.
