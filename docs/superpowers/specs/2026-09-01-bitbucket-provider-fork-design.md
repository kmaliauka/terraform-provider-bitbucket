# Design Document: Bitbucket Terraform Provider Fork (noogadev)

## 1. Overview and Objectives

This document specifies the technical design, architecture, and testing strategy for patching the `terraform-provider-bitbucket` (originating from `DrFaust92/terraform-provider-bitbucket` at `v2.52.0`) to resolve critical upstream bugs that prevent stable OpenTofu planning and execution in noogadev infrastructure.

The patched provider will initially be compiled, verified, and tested locally using OpenTofu `dev_overrides` before being prepared for organizational release and publication.

---

## 2. Problem Statements

### 2.1. Issue 1: Branching Model Unmarshaling Crash (`default_branch_deletion`)
* **Upstream Reference**: Issue #234, PR #247
* **Root Cause**: Bitbucket Cloud API in the `/branching-model` and `/branching-model/settings` endpoints began returning the `default_branch_deletion` property inconsistently—either as a JSON string (`"false"`, `"true"`), a JSON boolean (`false`, `true`), or `null`.
* **Impact**: Go's native `json.Unmarshal` fails with:
  `json: cannot unmarshal string into Go struct field BranchingModel.default_branch_deletion of type bool`
  This crashes `tofu plan` or `tofu apply` whenever any project or repository branching model is managed.

### 2.2. Issue 2: Burst 429 Too Many Requests During Plan / Apply
* **Upstream Reference**: Issue #255, PR #255
* **Root Cause**: The upstream provider client directly executes requests without retry or rate-limiting backoff. In environments with multiple repositories, branches, and branch restrictions (e.g. 10+ repos x 4 branches x 12 restrictions), OpenTofu launches hundreds of concurrent API calls, triggering Bitbucket Cloud API burst rate limiters (HTTP 429).
* **Impact**: `tofu plan` fails with intermittent or persistent `429 Too Many Requests` errors on branch restriction reads, forcing artificial CLI throttling (`-parallelism=1` or `-parallelism=4`).

### 2.3. Issue 3: Complex Account Object Serialization in Branch Restrictions
* **Upstream Reference**: Issue #252, PR #252
* **Root Cause**: When reading branch restrictions, the Bitbucket API returns full `bitbucket.Account` objects instead of simple UUID strings. Upstream schema expects a set of string UUIDs.
* **Impact**: Type conversion errors in Terraform state or 404 errors during updates if display names are sent where UUIDs are expected.

---

## 3. Technical Architecture & Components

```
+-------------------------------------------------------------------------+
|                       OpenTofu / Terraform Engine                       |
+------------------------------------+------------------------------------+
                                     |
                                     v
+-------------------------------------------------------------------------+
|                  terraform-provider-bitbucket (noogadev)                |
|                                                                         |
|  +---------------------------+       +-------------------------------+  |
|  |       Branching Model     |       |       Branch Restrictions     |  |
|  |  +---------------------+  |       |  +-------------------------+  |  |
|  |  | FlexBool Unmarshaler|  |       |  | User UUID/Object Parser |  |  |
|  |  +---------------------+  |       |  +-------------------------+  |  |
|  +-------------+-------------+       +---------------+---------------+  |
|                |                                     |                  |
|                v                                     v                  |
|  +-------------------------------------------------------------------+  |
|  |                   Retrying HTTP Transport                         |  |
|  |     (Intercepts 429, Parses Retry-After, Exponential Backoff)     |  |
|  +-----------------------------------+-------------------------------+  |
+--------------------------------------|----------------------------------+
                                       | HTTPS
                                       v
                     +-----------------------------------+
                     |        Bitbucket Cloud API        |
                     +-----------------------------------+
```

### 3.1. Component 1: `FlexBool` (`bitbucket/flex_bool.go`)
* **Purpose**: Flexible boolean deserializer capable of handling diverse JSON representations.
* **Type definition**:
  ```go
  type FlexBool bool
  ```
* **Supported JSON inputs**:
  - `true`, `false` (boolean)
  - `"true"`, `"false"` (case-insensitive string)
  - `"1"`, `"0"`, `1`, `0` (numeric / numeric string)
  - `null` (evaluates to `false`)
  - `""` (empty string evaluates to `false`)
* **Methods implemented**:
  - `UnmarshalJSON(data []byte) error`
  - `Bool() bool`
  - `Value() (driver.Value, error)`

### 3.2. Component 2: `RetryTransport` (`bitbucket/retry_transport.go`)
* **Purpose**: HTTP `RoundTripper` wrapper that intercepts HTTP 429 Too Many Requests responses and applies exponential backoff with jitter.
* **Behavior**:
  1. Executes the downstream `http.RoundTripper`.
  2. If status code is `429 Too Many Requests`:
     - Checks `Retry-After` response header (seconds or RFC1123 date).
     - If not specified, calculates delay using exponential backoff:
       $$delay = \min(base \times 2^{attempt} + \text{jitter}, \text{maxDelay})$$
       Where $base = 1.0\text{s}$, $maxDelay = 30.0\text{s}$, jitter $\in [0, 500\text{ms}]$.
     - Retries up to `MaxRetries` (default: 7).
     - Respects request `context.Context` cancellation / timeout.
  3. Returns response once non-429 status code is received or retries are exhausted.

### 3.3. Component 3: Provider Client Integration (`bitbucket/provider.go`)
* Replaces the standard HTTP client transport in `bitbucket.NewAPIClient` with `RetryTransport`.
* Ensures both direct API client calls and SDK calls share the rate-limiting protection.

### 3.4. Component 4: Branching Model & Branch Restriction Updates
* Update `bitbucket/resource_bitbucket_project_branching_model.go` and `bitbucket/resource_bitbucket_branching_model.go` to use `FlexBool` for `default_branch_deletion`.
* Update `bitbucket/resource_bitbucket_branch_restriction.go` to safely unpack account representations and normalize UUIDs.

---

## 4. Testing Strategy (Go Best Practices)

### 4.1. Unit Tests for `FlexBool` (`bitbucket/flex_bool_test.go`)
* Table-driven testing covering:
  - Valid boolean primitives (`true`, `false`)
  - Valid string representations (`"true"`, `"false"`, `"TRUE"`, `"FALSE"`)
  - Valid numeric representations (`1`, `0`, `"1"`, `"0"`)
  - Valid empty / null representations (`null`, `""`)
  - Invalid representations (`"invalid"`, `"{}"`, `"[1, 2]"`) returning structured errors.
* Struct embedding and JSON unmarshaling verification on simulated API payloads.

### 4.2. Unit Tests for `RetryTransport` (`bitbucket/retry_transport_test.go`)
* Uses `net/http/httptest.Server` to simulate:
  - Immediate 200 OK (0 retries).
  - Burst 429 responses followed by 200 OK (verifying retry count and success).
  - 429 with explicit `Retry-After` header.
  - Exceeded maximum retries (verifying final 429 is returned).
  - Context cancellation during sleep (verifying immediate abort without hang).

### 4.3. Concurrency & Race Verification
* All test packages executed with race detector enabled:
  ```bash
  go test -v -race ./...
  ```

---

## 5. Local OpenTofu Verification Plan

1. **Build Provider Binary**:
   ```bash
   mkdir -p bin
   go build -o bin/terraform-provider-bitbucket main.go
   ```

2. **Configure OpenTofu Dev Overrides**:
   In `~/.tofurc`:
   ```hcl
   provider_installation {
     dev_overrides {
       "DrFaust92/bitbucket" = "/Users/kirillmalyavko/Terraform/terraform-provider-bitbucket/bin"
     }
     direct {}
   }
   ```

3. **End-to-End Verification**:
   Execute `tofu plan` in `/Users/kirillmalyavko/Terraform/org-infra-bitbucket` without throttling:
   - Verify `bitbucket_project_branching_model` plans cleanly without unmarshal errors.
   - Verify all 480+ branch restriction reads complete cleanly without 429 rate limit failures.
