# GPU PaaS Specs and Steering

This directory contains spec-driven development artifacts and AI steering documents for implementing the **terraform-provider-gpupaas** Terraform/OpenTofu provider.

The provider is a thin integration layer over the public Go SDK. It must not duplicate API or transport logic.

| Item | Value |
|---|---|
| Provider type name | `gpupaas` |
| Registry source | `gpupaas-ai/gpupaas` |
| Default endpoint | `https://console.gpupaas.ai` |
| SDK module | `github.com/gpupaas-ai/gpupaas-go` |

## Layout

```text
specs/
├── README.md                                      # This file
├── terraform_provider_gpupaas_cursor_prompt.md    # Authoritative implementation prompt
└── terraform-provider/                            # Spec workflow + AI steering
    ├── requirements.md
    ├── design.md
    ├── tasks.md
    └── steering/
        ├── README.md
        ├── project-overview.md
        ├── architecture-patterns.md
        └── coding-standards.md
```

SDK specs and implementation live in the separate **gpupaas-go** repository:

- [gpupaas-go README — Appendix: Resource reference](https://github.com/gpupaas-ai/gpupaas-go/blob/main/README.md#appendix-resource-reference)
- Go types: `github.com/gpupaas-ai/gpupaas-go/apis/v1alpha1`
- Typed clients: `github.com/gpupaas-ai/gpupaas-go/clientset/typed/v1alpha1`

## Implementation order

Implement **gpupaas-go first**, then **terraform-provider-gpupaas**. When the SDK adds a new `Kind`, add matching Terraform resources and/or data sources in the provider.

```text
gpupaas-go (SDK) — source of truth for kinds, schemas, and API behavior
    ↑
    ├── paasctl (CLI, separate repo)
    └── terraform-provider-gpupaas (this repo)
```

Suggested provider implementation order (see `terraform-provider/tasks.md`):

1. Provider skeleton and SDK client wiring
2. `gpupaas_project`, `gpupaas_workspace`
3. `gpupaas_workspace_collaborator`
4. `gpupaas_virtual_machine` (resource + data source)
5. `gpupaas_storage`, `gpupaas_security_group`, `gpupaas_ssh_key`
6. Tests, examples, docs, acceptance tests

**Do not maintain a fixed resource list in these specs.** Derive Terraform type names from SDK kinds using snake_case with the `gpupaas_` prefix:

```text
Project               → gpupaas_project
WorkspaceCollaborator → gpupaas_workspace_collaborator
VirtualMachine        → gpupaas_virtual_machine
SshKey                → gpupaas_ssh_key
```

Add a managed resource when the SDK typed client supports create/update/delete; add a data source when get/list is the primary operation.

## How to use these docs

For the Terraform provider, read in order:

1. [`terraform_provider_gpupaas_cursor_prompt.md`](./terraform_provider_gpupaas_cursor_prompt.md) — full implementation prompt and acceptance criteria
2. [`terraform-provider/requirements.md`](./terraform-provider/requirements.md) — user stories and acceptance criteria
3. [`terraform-provider/design.md`](./terraform-provider/design.md) — architecture, interfaces, resource mapping
4. [`terraform-provider/tasks.md`](./terraform-provider/tasks.md) — ordered implementation checklist
5. [`terraform-provider/steering/`](./terraform-provider/steering/) — persistent AI context

## Resource model

All GPU PaaS objects share the Kubernetes-inspired envelope from gpupaas-go:

```yaml
apiVersion: gpupaas.ai/v1alpha1
kind: Workspace
metadata:
  name: dev
  project: demo
spec: { ... }      # desired state — Terraform arguments
status: { ... }    # observed state — Terraform computed attributes
```

The provider maps Terraform HCL to SDK types internally. Do not require users to set `apiVersion` or `kind` in HCL.

### Import IDs

Implement `ImportState` for every managed resource. Build import IDs from SDK metadata scope:

| Scope | Import ID format | Example |
|---|---|---|
| Project (cluster-scoped) | `<name>` | `demo` |
| Project-scoped | `<project>/<name>` | `demo/my-ssh-key` |
| Workspace-scoped | `<project>/<workspace>/<name>` | `demo/dev/alice@example.com` |

VirtualMachine uses two segments when `metadata.workspace` is empty and three when workspace is set.

```bash
terraform import gpupaas_project.demo demo
terraform import gpupaas_workspace.dev demo/dev
# Project-scoped:  terraform import gpupaas_<kind>.<label> <project>/<name>
# Workspace-scoped: terraform import gpupaas_<kind>.<label> <project>/<workspace>/<name>
```

## Reference architecture

The Kubernetes-inspired CLI/SDK model (see `paasctl/specs/kubectl_style_cli_architecture_prompt.md`):

- Declarative resources with `apiVersion`, `kind`, `metadata`, `spec`, `status`
- Reusable Go client library (`gpupaas-go`) as the primary product
- Thin consumers (CLI, Terraform provider) layered on the SDK
- Typed clients, runtime scheme, apply/delete semantics

The Terraform provider uses **Terraform Plugin Framework** (not legacy SDK v2) and must work with both HashiCorp Terraform and OpenTofu via the standard plugin protocol.

## Acceptance criteria

Provider implementation is complete when:

```bash
go mod tidy
go fmt ./...
go test ./...
go build -o bin/terraform-provider-gpupaas .
```

Plus local dev with a provider override and smoke-test HCL:

```hcl
terraform {
  required_providers {
    gpupaas = {
      source = "gpupaas-ai/gpupaas"
    }
  }
}

provider "gpupaas" {}

resource "gpupaas_project" "demo" {
  name         = "demo"
  display_name = "Demo Project"
  description  = "Created by Terraform"
}

resource "gpupaas_workspace" "dev" {
  project     = gpupaas_project.demo.name
  name        = "dev"
  description = "Development workspace"
}
```

Extend with additional `gpupaas_*` resources per the [gpupaas-go README appendix](https://github.com/gpupaas-ai/gpupaas-go/blob/main/README.md#appendix-resource-reference). Import must work for every managed resource exposed by the provider.

## Related repositories

| Repository | Role |
|---|---|
| [gpupaas-go](https://github.com/gpupaas-ai/gpupaas-go) | Public Go SDK — resource catalog and API client |
| [terraform-provider-gpupaas](https://github.com/gpupaas-ai/terraform-provider-gpupaas) | Terraform/OpenTofu provider (this repo) |
| [paasctl](https://github.com/gpupaas-ai/paasctl) | CLI consumer of gpupaas-go |
| [RafaySystems/paasctl](https://github.com/RafaySystems/paasctl) | Reference kubectl-style CLI implementation |
