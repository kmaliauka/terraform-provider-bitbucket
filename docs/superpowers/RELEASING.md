# Releasing this fork

Fork of `DrFaust92/terraform-provider-bitbucket`, published from
`github.com/kmaliauka/terraform-provider-bitbucket`.

## Versioning

The fork releases as `2.53.0`, `2.53.1`, and so on: upstream is at `2.52.0`, so
this reads as "one minor ahead of the base it was cut from" and leaves the whole
`2.52.x` line to upstream.

Do **not** use a prerelease tag such as `2.52.0-noogadev.1`. It is valid semver,
but OpenTofu will not select a prerelease under an ordinary constraint like
`~> 2.52` — only an exact pin resolves it, which is a trap the first time
someone writes a range.

The namespace does the disambiguation anyway: `kmaliauka/bitbucket` cannot
collide with `DrFaust92/bitbucket`, so the version number is free to be an
ordinary one.

Registry namespace `kmaliauka/bitbucket` is derived from the GitHub owner and the
repository name, which must stay `terraform-provider-bitbucket`.

Versions are immutable once indexed. The registry will not remove a release for a
routine mistake — the fix is always to publish the next version.

## Signing key

A dedicated key is used, so it can be revoked without touching the personal
`kirill.malyavko` key.

```
Primary key fingerprint: 7EE05CE731D4179541C4691313B16D6BB04A749F
Signing subkey (used for actual signatures): 854311E0AC987B56FB90DB6BCF9CD37C3537A547
UID:  Kirill Malyavko (terraform-provider-bitbucket release signing) <malyavkoki@gmail.com>
Type: RSA 4096, no expiry, no passphrase
```

`GPG_FINGERPRINT` for goreleaser is the **primary** fingerprint — `gpg --local-user`
resolves it to the right signing subkey automatically. The subkey ID is only
relevant when verifying a release by hand (`gpg --verify` reports it, not the
primary).

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
git tag -a v2.53.0 -m "noogadev fork: rate limiting, state contracts, workspace member account_id"
git push origin v2.53.0
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

The OpenTofu registry has no web console. Both submissions are GitHub issues in
[opentofu/registry](https://github.com/opentofu/registry), and both templates say
the issue **must** be filed through the issue form UI in a browser — the
automation reads the structured form fields, so `gh issue create` does not work.

1. [Submit new Provider](https://github.com/opentofu/registry/issues/new?template=provider.yml) —
   repository field: `kmaliauka/terraform-provider-bitbucket`.
2. [Submit new Provider Signing Key](https://github.com/opentofu/registry/issues/new?template=provider_key.yml) —
   namespace `kmaliauka`, provider name `bitbucket`, and the contents of
   `public.asc` in the key field. Tick the public-membership and DCO boxes.

Automation validates the submission and opens a pull request; maintainers merge
it without further review when it matches the inclusion policy. Indexing a new
version can take up to 30 minutes.

After that:

```hcl
bitbucket = {
  source  = "kmaliauka/bitbucket"
  version = "2.53.0"
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
