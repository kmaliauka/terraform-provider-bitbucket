## Unreleased (noogadev fork)

### Breaking changes

* `bitbucket_branch_restriction`: `users` now addresses workspace members by display name or by account UUID in braces. Bitbucket deprecated usernames and stopped returning them, so a configuration listing usernames must be updated. Display names are resolved to UUIDs when the restriction is written, and state stores the display name the API reports.
* `bitbucket_branch_restriction`: `groups` are finally written to state. The previous `d.Set` call failed silently and left the set empty, so the first plan after upgrading reports a diff. `owner` is read from the group's workspace slug.

### Bug fixes

* `bitbucket_deployment_variable`: paginate the variable lookup. Bitbucket returns ten variables per page, so any variable past the first page was reported as deleted and recreated on every apply ([#254](https://github.com/DrFaust92/terraform-provider-bitbucket/issues/254)).
* `bitbucket_branching_model` and `bitbucket_project_branching_model`: accept `default_branch_deletion` as a string, a boolean, `0`/`1`, or null ([#234](https://github.com/DrFaust92/terraform-provider-bitbucket/issues/234)).
* Return an error instead of panicking when a request fails at the transport layer. The response was dereferenced without checking the error in `Client.Do` and in seven read functions ([#211](https://github.com/DrFaust92/terraform-provider-bitbucket/issues/211)).
* `bitbucket_branching_model`: no longer clears `default_branch_deletion` when the API omits the field.

### Enhancements

* Rate limiting: a 429 is retried after the delay Bitbucket reports in `X-RateLimit-Reset` rather than a guessed backoff, and the window is shared across every in-flight request so parallel resources wait once instead of each rediscovering it. Waiting is capped at 120 seconds, after which the error names the reset time and the remedy.
* `bitbucket_branch_restriction`: the workspace member index is fetched once per workspace per run instead of once per resource.
* Report `d.Set` failures as diagnostics in the read functions for branch restrictions, branching models, and deployment variables, instead of discarding them.

### Documentation

* `bitbucket_project_branching_model`: `development` is documented as required, matching the schema ([#224](https://github.com/DrFaust92/terraform-provider-bitbucket/issues/224)).

## 1.3.0 (March 15, 2021)

This release contains the changes to upstream repo that were never released.

### Features

* add `bitbucket_deployment` and `bitbucket_deployment_variable` resources [#60](https://github.com/hashicorp/terraform-provider-bitbucket/pull/60)
* add `require_default_reviewer_approvals_to_merge` branch restriction value [#52](https://github.com/hashicorp/terraform-provider-bitbucket/pull/52)

### Bug fixes

* fix issue with omitempty [#49](https://github.com/hashicorp/terraform-provider-bitbucket/pull/49)

### Documentation

* fix ducmentation typo [#54](https://github.com/hashicorp/terraform-provider-bitbucket/pull/54), [#61](https://github.com/hashicorp/terraform-provider-bitbucket/pull/61) and [#65](https://github.com/hashicorp/terraform-provider-bitbucket/pull/65)

## 1.2.0 (January 23, 2020)
* add `bitbucket_project` to create a new project via the API
* add `bitbucket_repository` turn on/off pipelines
* add `bitbucket_repository_variable` to add variables via terraform to your pipelines builds
* add `bitbucket_user` to find a user and use for default reviewers.

## 1.1.0 (June 19, 2019)

### Features

* add `skip_cert_verification` for hooks [#19](https://github.com/terraform-providers/terraform-provider-bitbucket/issues/19)

### Bug fixes

* handle missing hooks [#24](https://github.com/terraform-providers/terraform-provider-bitbucket/issues/24)
* fix default reviewer pagination bug [#28](https://github.com/terraform-providers/terraform-provider-bitbucket/issues/28)

### Dev updates

* add `website` and `website-test` targets [#16](https://github.com/terraform-providers/terraform-provider-bitbucket/issues/16)
* add `website-test` target to Travis [#17](https://github.com/terraform-providers/terraform-provider-bitbucket/issues/17)
* upgrade to go 1.11 [#25](https://github.com/terraform-providers/terraform-provider-bitbucket/issues/25)
* switch to go modules [#27](https://github.com/terraform-providers/terraform-provider-bitbucket/issues/27)
* upgrade to `hashicorp/terraform` v0.12.2 [#34](https://github.com/terraform-providers/terraform-provider-bitbucket/issues/34)

### Documentation

* add note about v1 APIs [#21](https://github.com/terraform-providers/terraform-provider-bitbucket/issues/21)

## 1.0.0 (December 08, 2017)

* resource/bitbucket_repository: Add the ability to define a seperate slug for a repository ([#5](https://github.com/terraform-providers/terraform-provider-bitbucket/issues/5))

## 0.1.0 (June 20, 2017)

NOTES:

* Same functionality as that of Terraform 0.9.8. Repacked as part of [Provider Splitout](https://www.hashicorp.com/blog/upcoming-provider-changes-in-terraform-0-10/)
