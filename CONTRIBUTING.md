# Contributing / Development

This document is for contributors who want to build, test, and modify the provider.

For end-user usage instructions, see:

- `README.docker.md` (Docker workflow)
- `README.make.md` (local Make install workflow)

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

