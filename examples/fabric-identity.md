# Fabric identity (endpoint, fabric, tenant) — set once, reuse forever

Terraform loads `*.auto.tfvars` automatically. Put your **three repeating values** in one file:

`fabric.identity.auto.tfvars` (gitignored) — copy from `fabric.identity.auto.tfvars.example` once.

```bash
cp fabric.identity.auto.tfvars.example fabric.identity.auto.tfvars
# Edit api_endpoint, fabric_name, tenant_name
```

**Precedence (highest first):** `-var` / `-var-file` → `TF_VAR_*` env → `terraform.tfvars` → `*.auto.tfvars` (lexical order).  
So you can **override** endpoint, fabric, or tenant for a single run without editing the file:

```bash
terraform apply -var='api_endpoint=http://other:8787'
```

---

## Commands (Docker, `examples/` as `/workspace`)

### First-time setup (create identity file)

From repo root:

```bash
cp examples/fabric.identity.auto.tfvars.example examples/fabric.identity.auto.tfvars
# edit examples/fabric.identity.auto.tfvars
```

### After that — no `api_endpoint` / `fabric_name` / `tenant_name` on the command line

```bash
docker run --rm --network host \
  -v "$PWD/examples:/workspace" -w /workspace \
  --entrypoint terraform terraform-fabricapi:latest \
  apply -auto-approve
```

### Optional overrides only when you need them

```bash
docker run --rm --network host \
  -v "$PWD/examples:/workspace" -w /workspace \
  --entrypoint terraform terraform-fabricapi:latest \
  apply -auto-approve \
  -var='api_endpoint=http://10.20.13.14:8787' \
  -var='fabric_name=OtherFabric' \
  -var='tenant_name=other-tenant'
```

### Without `fabric.identity.auto.tfvars` (all identity on CLI)

```bash
docker run --rm --network host \
  -v "$PWD/examples:/workspace" -w /workspace \
  --entrypoint terraform terraform-fabricapi:latest \
  apply -auto-approve \
  -var='api_endpoint=http://10.20.13.14:8787' \
  -var='fabric_name=Terraform_test01' \
  -var='tenant_name=test1' \
  -var='max_gpus_allowed=8'
```

`max_gpus_allowed` and other knobs stay in `terraform.tfvars` or defaults in `variables.tf` unless you pass `-var` for those too.
