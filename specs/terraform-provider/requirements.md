# Requirements: terraform-provider-gpupaas

## Introduction

`github.com/gpupaas-ai/terraform-provider-gpupaas` is the official Terraform/OpenTofu provider for GPU PaaS (`gpupaas`). It exposes declarative infrastructure resources and data sources and uses `github.com/gpupaas-ai/gpupaas-go` as the sole API client layer.

Provider name: `gpupaas`  
Registry source: `gpupaas-ai/gpupaas`

**Do not maintain a fixed resource list in these specs.** The SDK is the source of truth for which GPU PaaS objects exist and how they are shaped:

- Resource catalog: [gpupaas-go README — Appendix: Resource reference](https://github.com/gpupaas-ai/gpupaas-go/blob/main/README.md#appendix-resource-reference)
- Go types and kinds: `github.com/gpupaas-ai/gpupaas-go/apis/v1alpha1` (`Kind*` constants, `types.go`, `register.go`)
- Typed clients: `github.com/gpupaas-ai/gpupaas-go/clientset/typed/v1alpha1`

Whenever the SDK adds a new `Kind`, add matching Terraform resources and/or data sources in this provider.

## Glossary

- **Plugin Framework**: HashiCorp Terraform Plugin Framework (not legacy SDK v2)
- **Resource**: Managed Terraform entity with create/read/update/delete/import lifecycle
- **Data source**: Read-only Terraform data lookup
- **State ID**: Terraform resource identifier stored in state
- **Clientset**: SDK typed client factory from `gpupaas-go`
- **Kind**: SDK resource type name (e.g. `Project`, `SshKey`)

## Requirements

### Requirement 1: Framework and compatibility

**User Story:** As a platform user, I want one provider that works with Terraform and OpenTofu so that I can choose either tool.

#### Acceptance Criteria

1. THE provider SHALL be implemented with Terraform Plugin Framework
2. THE provider SHALL work with HashiCorp Terraform CLI and OpenTofu CLI via the standard plugin protocol
3. THE provider SHALL NOT import OpenTofu-specific libraries unless required
4. THE provider SHALL support registry-style installation and local dev overrides

### Requirement 2: SDK-only API access

**User Story:** As a maintainer, I want a thin provider layer so that API behavior is defined once in the SDK.

#### Acceptance Criteria

1. THE provider SHALL use `github.com/gpupaas-ai/gpupaas-go` for all GPU PaaS API operations
2. THE provider SHALL NOT implement raw HTTP calls except through the SDK
3. THE provider SHALL NOT duplicate SDK API types except for Terraform state models
4. THE provider SHALL create a `clientset.Clientset` from `gpupaas.Config` during provider configure

### Requirement 3: Provider configuration

**User Story:** As a user, I want to configure endpoint and token via HCL or environment variables.

#### Acceptance Criteria

1. THE provider SHALL support attributes: `endpoint` (optional), `token` (optional, sensitive), `user_agent` (optional)
2. WHEN `endpoint` is unset, THE provider SHALL default to `https://console.gpupaas.ai`
3. WHEN env vars are set, THE provider SHALL fall back to `GPUPAAS_ENDPOINT` and `GPUPAAS_TOKEN`
4. THE provider SHALL normalize endpoint by removing trailing slash
5. THE provider SHALL NOT log or print tokens
6. WHEN token is missing, THE provider SHALL NOT fail configure unless an operation requires authentication

### Requirement 4: Resource naming and catalog

**User Story:** As a user, I want Terraform type names that mirror SDK kinds so that documentation stays consistent.

#### Acceptance Criteria

1. THE provider SHALL derive Terraform type names from SDK kinds using snake_case with the `gpupaas_` prefix
2. Examples: `Project` → `gpupaas_project`, `WorkspaceCollaborator` → `gpupaas_workspace_collaborator`, `SshKey` → `gpupaas_ssh_key`
3. THE provider SHALL add a managed resource when the SDK typed client supports create/update/delete for that kind
4. THE provider SHALL add a matching data source when the SDK supports get/list for that kind
5. Map each resource schema from the SDK README section for that kind: `metadata`, `spec`, and computed `status`

**Initial SDK kinds (v1alpha1) to cover:**

| SDK Kind | Terraform resource | Terraform data source | Scope notes |
|---|---|---|---|
| `Project` | `gpupaas_project` | `gpupaas_project` | Cluster-scoped |
| `Workspace` | `gpupaas_workspace` | `gpupaas_workspace` | Project-scoped |
| `WorkspaceCollaborator` | `gpupaas_workspace_collaborator` | optional | Workspace-scoped |
| `VirtualMachine` | `gpupaas_virtual_machine` | `gpupaas_virtual_machine` | Project or workspace |
| `Storage` | `gpupaas_storage` | optional | Project or workspace |
| `SecurityGroup` | `gpupaas_security_group` | optional | Project or workspace |
| `SshKey` | `gpupaas_ssh_key` | optional | Project or workspace |

### Requirement 5: Managed resources

**User Story:** As a user, I want Terraform resources for GPU PaaS objects so that I can manage infrastructure as code.

#### Acceptance Criteria

1. EACH managed resource SHALL implement Create, Read, Update, Delete, and ImportState
2. EACH resource SHALL map Terraform fields to SDK `metadata` and `spec`; SDK `status` to computed Terraform fields
3. Project-scoped resources SHALL use Terraform attribute `project` mapped to SDK `metadata.project`
4. Workspace-scoped resources SHALL use Terraform attributes `project` and `workspace` mapped to SDK `metadata.project` and `metadata.workspace`
5. EACH resource SHALL support optional `labels` and `annotations` maps where applicable
6. THE provider SHALL NOT expose `apiVersion` and `kind` as required Terraform arguments; set internally to `gpupaas.ai/v1alpha1` and the appropriate kind

### Requirement 6: Data sources

**User Story:** As a user, I want data sources to read existing resources into Terraform plans.

#### Acceptance Criteria

1. THE provider SHALL implement data sources for kinds where read/list is the primary use case (minimum: `gpupaas_project`, `gpupaas_workspace`, `gpupaas_virtual_machine`)
2. EACH data source SHALL implement Read only
3. Data source schemas SHALL align with corresponding resource schemas for shared attributes

### Requirement 7: Import

**User Story:** As a user, I want to import existing GPU PaaS resources into Terraform state.

#### Acceptance Criteria

1. THE provider SHALL implement `ImportState` for every managed resource
2. Import IDs SHALL follow SDK metadata scope (see gpupaas-go README “Shared metadata conventions”):

| Scope | Required metadata | Import ID format | Example |
|---|---|---|---|
| Project (cluster-scoped) | `metadata.name` | `<name>` | `demo` |
| Project-scoped | `metadata.project`, `metadata.name` | `<project>/<name>` | `demo/my-ssh-key` |
| Workspace-scoped | `metadata.project`, `metadata.workspace`, `metadata.name` | `<project>/<workspace>/<name>` | `demo/dev/alice@example.com` |

3. For **VirtualMachine**, use a two-segment ID when `metadata.workspace` is empty (project-scoped VM) and a three-segment ID when workspace is set
4. WHEN import ID is empty or malformed, THE provider SHALL return clear diagnostics
5. Document kind-specific import rules on each resource’s docs page

### Requirement 8: Error handling and state

**User Story:** As an operator, I want predictable behavior on API errors so that Terraform state stays correct.

#### Acceptance Criteria

1. WHEN Read returns NotFound, THE provider SHALL remove the resource from Terraform state
2. WHEN Delete returns NotFound, THE provider SHALL treat deletion as successful
3. WHEN Unauthorized or Forbidden, THE provider SHALL return diagnostics with remediation guidance
4. Diagnostics SHALL include resource type, project, workspace (when applicable), and name; SHALL NOT include tokens
5. THE provider SHALL use SDK helpers: `gpupaas.IsNotFound`, `IsConflict`, `IsUnauthorized`, `IsForbidden`

### Requirement 9: Testing

**User Story:** As a contributor, I want unit tests without a live API and optional acceptance tests.

#### Acceptance Criteria

1. Unit tests SHALL run with `go test ./...` without credentials
2. Acceptance tests SHALL be gated behind `TF_ACC=1` and `GPUPAAS_ENDPOINT` / `GPUPAAS_TOKEN`
3. Tests SHALL cover provider config, model conversion, import parsing, and NotFound read behavior where mockable
4. Acceptance tests SHALL cover CRUD + import for core resources and data source reads

### Requirement 10: Documentation and examples

**User Story:** As a user, I want README, examples, and registry-ready docs.

#### Acceptance Criteria

1. README SHALL document: purpose, Terraform and OpenTofu compatibility, registry install, local dev override, provider config, env vars, example project/workspace config, import examples, dev/test workflow, relationship to `gpupaas-go`, `paasctl` (if applicable), and `gpupaas.ai`
2. THE repo SHALL include `examples/` for provider, resources, and data sources
3. THE repo SHALL include `docs/` pages for index, all resources, and all data sources
4. Examples SHALL be accurate enough for registry publication

## Out of scope

- CLI implementation in this repo
- Direct HTTP client in provider
- Kubernetes client-go
- Legacy Plugin SDK v2
- Duplicating SDK wire/API logic

## Acceptance criteria

```bash
go mod tidy
go fmt ./...
go test ./...
go build -o bin/terraform-provider-gpupaas .
```

The provider SHALL support local development with Terraform or OpenTofu using a dev override.

Minimal smoke-test configuration:

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

Import examples (patterns — substitute names from config):

```bash
terraform import gpupaas_project.demo demo
terraform import gpupaas_workspace.dev demo/dev
# Project-scoped:  terraform import gpupaas_<kind>.<label> <project>/<name>
# Workspace-scoped: terraform import gpupaas_<kind>.<label> <project>/<workspace>/<name>
```

## Dependency order

**gpupaas-go MUST be implemented and published (or replace-directive linked) before this provider.**

The final deliverable SHALL expose Terraform resources and data sources for every SDK `Kind` documented in the [gpupaas-go README appendix](https://github.com/gpupaas-ai/gpupaas-go/blob/main/README.md#appendix-resource-reference).
