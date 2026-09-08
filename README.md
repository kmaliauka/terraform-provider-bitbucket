# Bitbucket Terraform Provider

A fork of [DrFaust92/terraform-provider-bitbucket](https://github.com/DrFaust92/terraform-provider-bitbucket),
published as `kmaliauka/bitbucket` in the OpenTofu registry.

The upstream provider is in maintenance mode, and several of the fixes below sit
in pull requests that have not been merged. This fork exists to carry them, and
is kept close enough to upstream that it can be rebased or retired if they land.

## Using the provider

```hcl
terraform {
  required_providers {
    bitbucket = {
      source  = "kmaliauka/bitbucket"
      version = "2.53.1"
    }
  }
}

# Atlassian API token with your account email. App passwords stopped working on
# 2026-06-09; a Bitbucket workspace access token goes in oauth_token instead.
provider "bitbucket" {
  username = "gob@bluth.example.com"
  password = "ATATT3x..."
}

resource "bitbucket_repository" "infrastructure" {
  owner = "myteam"
  name  = "terraform-code"
}

resource "bitbucket_project" "infrastructure" {
  owner = "myteam" # must be a team
  name  = "terraform-project"
  key   = "TERRAFORMPROJ"
}
```

Every resource and data source is documented under [`docs/`](docs/).

## What this fork changes

**Rate limiting.** A 429 is retried on a gate shared by every in-flight request,
so parallel resources wait once instead of each rediscovering the limit. Waiting
is capped, after which the error names the reset time rather than hanging.

**Branch restrictions keep their identity.** Bitbucket generates the numeric id
of a restriction but enforces uniqueness on kind plus branch pattern. A
restriction recreated in the UI comes back under a new id; the provider now
adopts it instead of planning a create that fails with `409 already exists`.

**`bitbucket_workspace_members` exposes `account_id`.** This is the identifier
the Atlassian Administration API also reports, which makes it possible to join a
Bitbucket user to their Atlassian account without matching on display names.

**Fixes carried from open upstream pull requests.** `default_branch_deletion`
returned as a string ([#234](https://github.com/DrFaust92/terraform-provider-bitbucket/issues/234)),
branch restriction users and groups never reaching state
([#252](https://github.com/DrFaust92/terraform-provider-bitbucket/pull/252)),
rate limit retries ([#255](https://github.com/DrFaust92/terraform-provider-bitbucket/pull/255)),
a nil dereference on transport errors
([#211](https://github.com/DrFaust92/terraform-provider-bitbucket/issues/211)),
and deployment variables past the first page being dropped from state
([#254](https://github.com/DrFaust92/terraform-provider-bitbucket/issues/254)).

See [CHANGELOG.md](CHANGELOG.md) for the full list.

## Requirements

- [OpenTofu](https://opentofu.org/docs/intro/install/) 1.10+ or Terraform 1.x
- [Go](https://go.dev/doc/install) 1.25 to build the provider

## Building

```sh
git clone git@github.com:kmaliauka/terraform-provider-bitbucket.git
cd terraform-provider-bitbucket
make build
```

To run against a local build without publishing, point the CLI at the binary:

```hcl
# ~/.tofurc  or  ~/.terraformrc
provider_installation {
  dev_overrides {
    "kmaliauka/bitbucket" = "/path/to/terraform-provider-bitbucket/bin"
  }
  direct {}
}
```

## Testing

```sh
make test     # unit tests
make testacc  # acceptance tests: creates real resources and needs TF_ACC=1
```

Acceptance tests talk to a real Bitbucket workspace and can cost money.

## Releasing

Tagging `v*` builds, signs and publishes a release. See [RELEASING.md](RELEASING.md).
