# Releasing this fork

Fork of `DrFaust92/terraform-provider-bitbucket`, published from
`github.com/kmaliauka/terraform-provider-bitbucket`.

## Versioning

Upstream is at `2.52.0`. The fork tags on top of it as `2.52.0-noogadev.N` so
that the base version stays readable and an upstream rebase is obvious in the
tag name.

Registry namespace: `kmaliauka/bitbucket`. It is derived from the GitHub owner
and the repository name, which must stay `terraform-provider-bitbucket`.

## Signing key

A dedicated key is used, so it can be revoked without touching the personal
`kirill.malyavko` key.

```
Fingerprint: 7EE05CE731D4179541C4691313B16D6BB04A749F
UID:         Kirill Malyavko (terraform-provider-bitbucket release signing) <malyavkoki@gmail.com>
Type:        RSA 4096, no expiry, no passphrase
```

Exported to `~/bitbucket-provider-release-key/` (mode 600, outside the repo):

| File | Use |
|---|---|
| `public.asc` | uploaded to the registry as the signing key |
| `private.asc` | contents of the `GPG_PRIVATE_KEY` GitHub secret |
| `fingerprint.txt` | value of `GPG_FINGERPRINT` |

The key has **no passphrase**: the private key is itself the secret, and adding
a passphrase would only mean storing a second secret next to it. If you prefer
one, run `gpg --edit-key 7EE05CE7… passwd` and set `PASSPHRASE` accordingly.

Delete `private.asc` once it is in the GitHub secret; the key stays in the local
keyring either way.

## Release through GitHub Actions

`.github/workflows/release.yml` already runs
`hashicorp/ghaction-terraform-provider-release` on any `v*` tag. It needs two
repository secrets:

```bash
gh secret set GPG_PRIVATE_KEY < ~/bitbucket-provider-release-key/private.asc
gh secret set PASSPHRASE --body ""
```

Then:

```bash
git tag -a v2.52.0-noogadev.1 -m "noogadev fork: rate limiting, state contracts, workspace member account_id"
git push origin v2.52.0-noogadev.1
```

## Release by hand

Same artifacts, no Actions involved. A full cross-platform build takes about ten
minutes.

```bash
export GPG_FINGERPRINT=7EE05CE731D4179541C4691313B16D6BB04A749F
export GITHUB_TOKEN=<token with repo scope>

goreleaser check
goreleaser release --clean
```

To rehearse without publishing:

```bash
goreleaser release --snapshot --clean --skip=sign
```

## Registry publication

1. Sign in to the OpenTofu registry with the GitHub account that owns the fork.
2. Add the provider; the repository must be public and named
   `terraform-provider-bitbucket`.
3. Upload `public.asc` as the signing key.
4. Publish the tagged release.

After that:

```hcl
bitbucket = {
  source  = "kmaliauka/bitbucket"
  version = "2.52.0-noogadev.1"
}
```

Until then consumers use `dev_overrides`, which works on a workstation but not
in CI:

```hcl
provider_installation {
  dev_overrides {
    "DrFaust92/bitbucket" = "/Users/kirillmalyavko/Terraform/terraform-provider-bitbucket/bin"
  }
  direct {}
}
```

## Rebasing onto upstream

```bash
git fetch upstream
git rebase upstream/master
```

`upstream`'s push URL is deliberately set to an invalid string so that a stray
`git push upstream` cannot reach someone else's repository.

Three of the changes in this fork duplicate open upstream pull requests
(#247 FlexBool, #252 branch restriction users, #255 rate limit retries). If any
of them merges, drop the local commit during the rebase rather than resolving a
conflict.
