# Contributing to Terraform-provider-Fabricapi

Thanks for your interest in contributing! For end-user usage instructions, see:

- `README.docker.md` (Docker workflow)
- `README.make.md` (local Make install workflow)

## Reporting Bugs

Please use the [Bug Report template](./.github/ISSUE_TEMPLATE/bug_report.md) and include:
- Steps to reproduce
- Expected vs. actual behavior
- Environment details (OS, version, relevant config)

For **security vulnerabilities**, do not open a public issue — email security@aviznetworks.com instead.

## Suggesting Features

Please use the [Feature Request template](./.github/ISSUE_TEMPLATE/feature_request.md)
and describe the problem you're trying to solve, not just the solution — it helps
us evaluate alternatives.

## Development Workflow 

## Prerequisites

- Go (version compatible with `go.mod`)
- Terraform (recommended **>= 1.5**)
- `make`
- Docker (optional, only for Docker workflow testing)

## Build locally

```bash
make build
```

## Install locally (for local Terraform runs)

```bash
make install
```

## Run unit/compile checks

```bash
go test ./...
```

or:

```bash
make test
```

## Manual testing with examples (interactive)

The recommended workflow is the decoupled examples:

- `examples/decoupled/01-tenant`
- `examples/decoupled/02-servers`
- `examples/decoupled/03-vpcpeering`
- `examples/decoupled/04-gpu-allocations`
- `examples/decoupled/05-available-servers`
- `examples/decoupled/06-vf-interfaces`
- `examples/decoupled/07-vf-assign`

Set required environment variables for connectivity/auth (see user READMEs), then:

```bash
terraform -chdir=examples/decoupled/01-tenant init -upgrade
terraform -chdir=examples/decoupled/01-tenant apply
```

Repeat similarly for the other roots.

## Testing against a real API

If you are running against a real Fabric API:

- Ensure your endpoint/auth env vars are correct.
- Start with **sync** mode first (`prefer=respond-sync`) and confirm CRUD flows.
- Then validate async/webhooks scenarios using the examples under `examples/decoupled/`.

## Notes

- Terraform may cache provider checksums in `.terraform.lock.hcl` per example root. If you rebuild the provider binary and Terraform rejects it, delete the lock file in that example root and re-run `terraform init`.

## Commit Messages

Write commit messages that explain *why* a change was made, not just what changed. Reference the related issue number when one exists (e.g. `Fixes #42`).

## Submitting a Pull Request

1. Ensure `make check` (or the language equivalent) passes.
2. Push your branch and open a PR against `master`. GitHub will pre-fill the [PR template](./.github/PULL_REQUEST_TEMPLATE.md) — fill it in rather than deleting it.
3. Describe what changed, why, and how you tested it. Note any impact on the other language SDK.
4. One of the maintainers will review and may request changes.

## Pull Request Process

1. Fill out the PR template completely — incomplete PRs may be asked for more info before review.
2. Link the issue your PR resolves (e.g. `Closes #123`).
3. Keep PRs small and focused; large PRs take longer to review and are more likely
  to be asked to split.
4. A maintainer will review, request changes if needed, and merge once approved.

## Versioning

This repository follows [Semantic Versioning (SemVer)](https://semver.org/) and maintains its version history independently of other platform releases. 
See the [Versioning Policy](./README.md#versioning-policy) in the README for details.
