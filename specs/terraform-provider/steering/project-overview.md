---
inclusion: auto
---

# terraform-provider-gpupaas — Project Overview

## Purpose

Official Terraform/OpenTofu provider for GPU PaaS (`gpupaas`). Exposes managed resources and data sources that map to the same declarative model as `gpupaas-go`: `apiVersion`, `kind`, `metadata`, `spec`, and `status`.

**Resource catalog is SDK-driven** — do not hard-code a fixed list. See [gpupaas-go README appendix](https://github.com/gpupaas-ai/gpupaas-go/blob/main/README.md#appendix-resource-reference).

## Identity

| Field | Value |
|---|---|
| Provider type name | `gpupaas` |
| Registry source | `gpupaas-ai/gpupaas` |
| Module | `github.com/gpupaas-ai/terraform-provider-gpupaas` |
| Go version | 1.22+ |
| Framework | Terraform Plugin Framework (NOT SDK v2) |

## Compatibility

Works with:

- HashiCorp Terraform CLI
- OpenTofu CLI
- Terraform Registry / OpenTofu Registry installation
- Local dev overrides (`provider_installation.dev_overrides`)

Uses standard Terraform plugin protocol — no OpenTofu-specific libraries.

## Architecture principle

**Thin provider, fat SDK.**

```text
terraform-provider-gpupaas  →  gpupaas-go  →  gpupaas.ai API
```

The provider MUST NOT implement HTTP or duplicate API types. All API calls go through `github.com/gpupaas-ai/gpupaas-go`.

Reference: `paasctl/specs/kubectl_style_cli_architecture_prompt.md` (thin CLI/consumer, reusable library).

## Resource naming

Derive Terraform type names from SDK kinds (`apis/v1alpha1` `Kind*` constants):

```text
Project               → gpupaas_project
Workspace             → gpupaas_workspace
WorkspaceCollaborator → gpupaas_workspace_collaborator
VirtualMachine        → gpupaas_virtual_machine
Storage               → gpupaas_storage
SecurityGroup         → gpupaas_security_group
SshKey                → gpupaas_ssh_key
```

Add a managed resource when the SDK typed client supports create/update/delete; add a data source when get/list is the primary operation.

## Resources and data sources (initial v1alpha1)

| Terraform name | SDK kind | Scope | Import ID |
|---|---|---|---|
| `gpupaas_project` | Project | cluster | `<name>` |
| `gpupaas_workspace` | Workspace | project | `<project>/<name>` |
| `gpupaas_workspace_collaborator` | WorkspaceCollaborator | workspace | `<project>/<workspace>/<name>` |
| `gpupaas_virtual_machine` | VirtualMachine | project or workspace | `<project>/<name>` or `<project>/<workspace>/<name>` |
| `gpupaas_storage` | Storage | project or workspace | same as VM |
| `gpupaas_security_group` | SecurityGroup | project or workspace | same as VM |
| `gpupaas_ssh_key` | SshKey | project or workspace | same as VM |

Data sources (minimum): `gpupaas_project`, `gpupaas_workspace`, `gpupaas_virtual_machine`.

## Provider configuration

```hcl
provider "gpupaas" {
  endpoint = "https://console.gpupaas.ai"
  token    = var.gpupaas_token
}
```

| Attribute | Env fallback | Default |
|---|---|---|
| `endpoint` | `GPUPAAS_ENDPOINT` | `https://console.gpupaas.ai` |
| `token` | `GPUPAAS_TOKEN` | — (sensitive) |
| `user_agent` | — | provider default |

## Spec location

```text
terraform-provider-gpupaas/specs/terraform-provider/
├── requirements.md
├── design.md
├── tasks.md
└── steering/
```

Read `requirements.md` → `design.md` → `tasks.md` before implementing.

## Dependency order

1. Implement `gpupaas-go` first (including dev resources: Storage, SecurityGroup, SshKey, VirtualMachine)
2. Implement this provider second
3. CLI (`paasctl`) is a separate consumer

## Common commands

```bash
go mod tidy
go fmt ./...
go test ./...
go build -o bin/terraform-provider-gpupaas .
make install-local   # local plugin override path
TF_ACC=1 go test ./... -v -count=1   # acceptance tests
```

## Source prompt

Original implementation prompt: `specs/terraform_provider_gpupaas_cursor_prompt.md`
