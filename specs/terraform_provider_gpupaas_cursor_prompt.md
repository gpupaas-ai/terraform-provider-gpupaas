# Cursor Implementation Prompt: GPU PaaS Terraform/OpenTofu Provider

You are implementing a production-quality Terraform/OpenTofu provider for GPU PaaS.

Repository:

```text
github.com/gpupaas-ai/terraform-provider-gpupaas
```

Related SDK repository:

```text
github.com/gpupaas-ai/gpupaas-go
```

Provider name:

```text
gpupaas
```

Provider source address for examples:

```text
gpupaas-ai/gpupaas
```

Primary goal:

Build a Terraform provider using the Terraform Plugin Framework. The provider must work with both HashiCorp Terraform and OpenTofu. It must use the public Go SDK `github.com/gpupaas-ai/gpupaas-go` as its API client layer and must not duplicate API request/transport logic inside the provider.

The provider should expose GPU PaaS resources that map cleanly to the same declarative resource model used by `gpupaas-go`: `apiVersion`, `kind`, `metadata`, `spec`, and `status`.

---

## Important compatibility requirements

Use the Terraform Plugin Framework, not the legacy Terraform Plugin SDK v2.

The provider must be compatible with:

- HashiCorp Terraform CLI
- OpenTofu CLI
- Terraform Registry-style provider installation
- OpenTofu Registry-style provider installation
- Local development overrides

OpenTofu uses the Terraform plugin protocol, so a provider built with the Terraform Plugin Framework should also work with OpenTofu.

Do not import OpenTofu-specific provider libraries unless required. Implement the provider as a standard Terraform Plugin Framework provider.

---

## Design principles

1. The provider is a thin Terraform/OpenTofu integration layer.
2. The provider must call `github.com/gpupaas-ai/gpupaas-go` for all GPU PaaS API operations.
3. The provider must not implement raw HTTP calls directly except through the SDK.
4. The provider must expose Terraform-friendly resources and data sources.
5. The provider must preserve the declarative resource model used by the SDK.
6. The provider must support import, plan, apply, refresh, update, and destroy flows.
7. The provider must produce useful diagnostics.
8. The provider must never log secrets.
9. The provider must be testable without a real GPU PaaS API by using fake SDK clients or httptest where appropriate.
10. The provider must follow Terraform naming conventions and resource lifecycle expectations.

---

## Expected end-user Terraform usage

The provider should support this configuration:

```hcl
terraform {
  required_providers {
    gpupaas = {
      source  = "gpupaas-ai/gpupaas"
      version = "~> 0.1"
    }
  }
}

provider "gpupaas" {
  endpoint = "https://console.gpupaas.ai"
  token    = var.gpupaas_token
}

resource "gpupaas_project" "demo" {
  name         = "demo"
  display_name = "Demo Project"
  description  = "Created by Terraform"

  labels = {
    env = "dev"
  }
}

resource "gpupaas_workspace" "dev" {
  project     = gpupaas_project.demo.name
  name        = "dev"
  description = "Development workspace"
}
```

---

## Provider configuration

Implement provider-level configuration:

```hcl
provider "gpupaas" {
  endpoint = "https://console.gpupaas.ai"
  token    = var.gpupaas_token
}
```

Provider attributes:

| Attribute | Type | Required | Sensitive | Env var fallback | Default |
|---|---:|---:|---:|---|---|
| `endpoint` | string | optional | no | `GPUPAAS_ENDPOINT` | `https://console.gpupaas.ai` |
| `token` | string | optional | yes | `GPUPAAS_TOKEN` | none |
| `user_agent` | string | optional | no | none | provider default |

Behavior:

- `endpoint` must be normalized to remove trailing slash.
- `token` must be marked sensitive.
- If token is missing, provider configuration should not immediately fail unless an operation requires authentication.
- Use `gpupaas.Config` from `github.com/gpupaas-ai/gpupaas-go`.
- Create a `clientset.Clientset` from the SDK and store it in provider configured data.
- Support environment variable fallback.
- Do not print or log tokens.

---

## Repository layout

Create this repository structure:

```text
.
├── go.mod
├── go.sum
├── README.md
├── LICENSE
├── Makefile
├── main.go
├── internal/
│   ├── provider/
│   │   ├── provider.go
│   │   ├── provider_test.go
│   │   ├── config.go
│   │   ├── diagnostics.go
│   │   ├── validators.go
│   │   └── models.go
│   ├── client/
│   │   ├── client.go
│   │   └── fake.go
│   ├── resources/
│   │   ├── project_resource.go
│   │   ├── project_resource_test.go
│   │   ├── workspace_resource.go
│   │   ├── workspace_resource_test.go
│   │   ├── common.go
│   │   ├── metadata.go
│   │   └── converters.go
│   ├── datasources/
│   │   ├── project_data_source.go
│   │   └── workspace_data_source.go
│   └── acctest/
│       ├── config.go
│       └── provider_test.go
├── examples/
│   ├── provider/
│   │   └── provider.tf
│   ├── resources/
│   │   ├── gpupaas_project/
│   │   │   └── resource.tf
│   │   └── gpupaas_workspace/
│   │       └── resource.tf
│   └── data-sources/
│       ├── gpupaas_project/
│       │   └── data-source.tf
│       └── gpupaas_workspace/
│           └── data-source.tf
├── docs/
│   ├── index.md
│   ├── resources/
│   │   ├── project.md
│   │   └── workspace.md
│   └── data-sources/
│       ├── project.md
│       └── workspace.md
└── templates/
    ├── index.md.tmpl
    ├── resources.md.tmpl
    └── data-sources.md.tmpl
```

Use Go module:

```text
module github.com/gpupaas-ai/terraform-provider-gpupaas
```

Minimum Go version:

```text
go 1.22
```

---

## Required dependencies

Use the Terraform Plugin Framework stack:

```text
github.com/hashicorp/terraform-plugin-framework
github.com/hashicorp/terraform-plugin-framework-validators
github.com/hashicorp/terraform-plugin-go
github.com/hashicorp/terraform-plugin-log
github.com/hashicorp/terraform-plugin-testing
```

Use the GPU PaaS SDK:

```text
github.com/gpupaas-ai/gpupaas-go
```

Use only tagged versions. After implementation, run:

```bash
go mod tidy
```

---

## Provider type implementation

Implement provider metadata:

```go
func (p *GPUProvider) Metadata(ctx context.Context, req provider.MetadataRequest, resp *provider.MetadataResponse) {
    resp.TypeName = "gpupaas"
    resp.Version = p.version
}
```

Implement resources:

```go
func (p *GPUProvider) Resources(ctx context.Context) []func() resource.Resource {
    return []func() resource.Resource{
        resources.NewProjectResource,
        resources.NewWorkspaceResource,
    }
}
```

Implement data sources:

```go
func (p *GPUProvider) DataSources(ctx context.Context) []func() datasource.DataSource {
    return []func() datasource.DataSource{
        datasources.NewProjectDataSource,
        datasources.NewWorkspaceDataSource,
    }
}
```

---

## Resource naming

Derive Terraform type names from SDK kinds in `github.com/gpupaas-ai/gpupaas-go/apis/v1alpha1`. Use snake_case with the `gpupaas_` prefix:

```text
<SDK Kind in PascalCase> → gpupaas_<snake_case>
Project               → gpupaas_project
WorkspaceCollaborator → gpupaas_workspace_collaborator
SshKey                → gpupaas_ssh_key
```

The authoritative list of kinds to implement is the [gpupaas-go README appendix](https://github.com/gpupaas-ai/gpupaas-go/blob/main/README.md#appendix-resource-reference). Add a managed resource when the SDK typed client supports create/update/delete; add a matching data source when the SDK supports get/list.

Import IDs follow metadata scope (see [Import behavior](#import-behavior) and README “Shared metadata conventions”):

```text
Project (cluster-scoped):     <name>
Project-scoped resource:      <project>/<name>
Workspace-scoped resource:    <project>/<workspace>/<name>
VirtualMachine (project):     <project>/<name>
VirtualMachine (workspace):   <project>/<workspace>/<name>
```

Examples:

```bash
terraform import gpupaas_project.demo demo
terraform import gpupaas_workspace.dev demo/dev
terraform import gpupaas_workspace_collaborator.alice demo/dev/alice@example.com
```

---

## Common schema conventions

All resources should include:

```hcl
name        = string
labels      = map(string), optional
annotations = map(string), optional
```

Project-scoped resources should include:

```hcl
project = string
```

Computed status fields should be read-only/computed.

Do not expose `apiVersion` and `kind` as required Terraform arguments unless needed. The provider should map Terraform resources to SDK resource kinds internally.

Map Terraform fields to SDK objects:

- Terraform `name` -> SDK `metadata.name`
- Terraform `project` -> SDK `metadata.namespace` for project-scoped resources
- Terraform `labels` -> SDK `metadata.labels`
- Terraform `annotations` -> SDK `metadata.annotations`
- Terraform resource-specific fields -> SDK `spec`
- SDK `status` -> Terraform computed fields

---

## Resource: gpupaas_project

Terraform schema:

```hcl
resource "gpupaas_project" "example" {
  name         = "demo"
  display_name = "Demo Project"
  description  = "Demo project"

  labels = {
    env = "dev"
  }

  annotations = {
    owner = "platform-team"
  }
}
```

Attributes:

| Name | Type | Required | Optional | Computed | ForceNew | Sensitive |
|---|---|---:|---:|---:|---:|---:|
| `id` | string | no | no | yes | no | no |
| `name` | string | yes | no | no | yes | no |
| `display_name` | string | no | yes | no | no | no |
| `description` | string | no | yes | no | no | no |
| `labels` | map(string) | no | yes | no | no | no |
| `annotations` | map(string) | no | yes | no | no | no |
| `phase` | string | no | no | yes | no | no |

Lifecycle:

- Create: call SDK Projects().Create or apply helper.
- Read: call SDK Projects().Get.
- Update: call SDK Projects().Update.
- Delete: call SDK Projects().Delete.
- Import: parse `<name>`.

State ID:

```text
<name>
```

---

## Resource: gpupaas_workspace

Terraform schema:

```hcl
resource "gpupaas_workspace" "example" {
  project     = "demo"
  name        = "dev"
  description = "Development workspace"
}
```

Attributes:

| Name | Type | Required | Optional | Computed | ForceNew | Sensitive |
|---|---|---:|---:|---:|---:|---:|
| `id` | string | no | no | yes | no | no |
| `project` | string | yes | no | no | yes | no |
| `name` | string | yes | no | no | yes | no |
| `description` | string | no | yes | no | no | no |
| `labels` | map(string) | no | yes | no | no | no |
| `annotations` | map(string) | no | yes | no | no | no |
| `phase` | string | no | no | yes | no | no |

Lifecycle:

- Create: call SDK Workspaces(project).Create.
- Read: call SDK Workspaces(project).Get.
- Update: call SDK Workspaces(project).Update.
- Delete: call SDK Workspaces(project).Delete.
- Import: parse `<project>/<name>`.

State ID:

```text
<project>/<name>
```

---

## Data sources

Implement data sources for reading existing resources.

### data.gpupaas_project

```hcl
data "gpupaas_project" "demo" {
  name = "demo"
}
```

### data.gpupaas_workspace

```hcl
data "gpupaas_workspace" "dev" {
  project = "demo"
  name    = "dev"
}
```

---

## Provider implementation details

Use Framework interfaces:

- `provider.Provider`
- `provider.ProviderWithMetadata`
- `resource.Resource`
- `resource.ResourceWithImportState`
- `resource.ResourceWithConfigure`
- `datasource.DataSource`
- `datasource.DataSourceWithConfigure`

Each resource should implement:

```go
Metadata
Schema
Configure
Create
Read
Update
Delete
ImportState
```

Each data source should implement:

```go
Metadata
Schema
Configure
Read
```

Use typed Framework values:

- `types.String`
- `types.Int64`
- `types.Bool`
- `types.Map`
- `types.List`
- `types.Object`

Avoid raw `map[string]interface{}` in provider state models.

Use `schema.StringAttribute`, `schema.Int64Attribute`, `schema.MapAttribute`, `schema.ListNestedAttribute`, and `schema.SingleNestedAttribute` as appropriate.

---

## Model conversion requirements

Create conversion functions in `internal/resources/converters.go`.

Examples:

```go
func ProjectModelToSDK(ctx context.Context, model ProjectModel) (*v1alpha1.Project, diag.Diagnostics)
func ProjectSDKToModel(ctx context.Context, obj *v1alpha1.Project, model *ProjectModel) diag.Diagnostics

func WorkspaceModelToSDK(ctx context.Context, model WorkspaceModel) (*v1alpha1.Workspace, diag.Diagnostics)
func WorkspaceSDKToModel(ctx context.Context, obj *v1alpha1.Workspace, model *WorkspaceModel) diag.Diagnostics
```

Conversion rules:

- Preserve Terraform unknown/null handling.
- Do not send computed status fields to the API.
- Do not overwrite unknown plan values incorrectly.
- Use diagnostics instead of panics.
- Use SDK API version `gpupaas.ai/v1alpha1`.
- Use SDK kinds: `Project`, `Workspace`.

---

## Error handling requirements

Use SDK helper errors where available:

```go
gpupaas.IsNotFound(err)
gpupaas.IsConflict(err)
gpupaas.IsUnauthorized(err)
gpupaas.IsForbidden(err)
```

Resource read behavior:

- If SDK returns NotFound during Read, remove the resource from Terraform state.
- If SDK returns NotFound during Delete, treat deletion as successful.
- If SDK returns Unauthorized or Forbidden, return a diagnostic with clear remediation.
- Include resource type, project, and name in diagnostics.
- Never include token or authorization headers in diagnostics.

Diagnostic examples:

```text
Unable to read GPU PaaS workspace
project=demo name=dev error=<sdk error>
```

---

## Import behavior

Implement `ImportState` for every resource.

Project import:

```text
<name>
```

Project-scoped resource import:

```text
<project>/<name>
```

Validation:

- Reject empty IDs.
- Reject malformed IDs.
- Return clear diagnostics.

---

## Testing requirements

Create unit tests for:

- Provider schema.
- Provider configuration from explicit attributes.
- Provider configuration from environment variables.
- Project model-to-SDK conversion.
- Project SDK-to-model conversion.
- Workspace model-to-SDK conversion.
- Import ID parsing.
- NotFound read removes state behavior, where possible.

Create acceptance tests using `terraform-plugin-testing`, but guard them behind:

```bash
TF_ACC=1
GPUPAAS_ENDPOINT=...
GPUPAAS_TOKEN=...
```

Acceptance tests should include:

- `gpupaas_project` create/update/import/delete.
- `gpupaas_workspace` create/update/import/delete.
- Data source reads.

Acceptance tests must be skipped unless `TF_ACC=1` and required environment variables are present.

Unit tests must run without real credentials:

```bash
go test ./...
```

---

## Documentation requirements

Create README.md with:

1. Provider purpose.
2. Terraform and OpenTofu compatibility note.
3. Installation from registry.
4. Local development installation override.
5. Provider configuration.
6. Environment variables.
7. Example project/workspace configuration.
8. Import examples.
9. Development workflow.
10. Testing workflow.
11. Relationship to:
    - `github.com/gpupaas-ai/gpupaas-go`
    - `github.com/gpupaas-ai/paasctl`, if applicable later
    - `gpupaas.ai`

Create docs pages for all resources and data sources. The docs must be accurate enough for registry publication.

Use examples in the `examples/` directory that can be consumed by docs generation tools later.

---

## Makefile requirements

Create a Makefile with:

```makefile
.PHONY: build test testacc fmt lint docs install-local

build:
	go build -o bin/terraform-provider-gpupaas .

test:
	go test ./...

testacc:
	TF_ACC=1 go test ./... -v -count=1

fmt:
	go fmt ./...

lint:
	golangci-lint run

install-local:
	mkdir -p ~/.terraform.d/plugins/gpupaas-ai/gpupaas/0.1.0/$$(go env GOOS)_$$(go env GOARCH)
	go build -o ~/.terraform.d/plugins/gpupaas-ai/gpupaas/0.1.0/$$(go env GOOS)_$$(go env GOARCH)/terraform-provider-gpupaas_v0.1.0 .
```

If the install path needs adjustment for the current Terraform/OpenTofu development override mechanism, document that clearly in README.md.

---

## Local development override example

Document this in README.md:

```hcl
provider_installation {
  dev_overrides {
    "gpupaas-ai/gpupaas" = "/absolute/path/to/local/provider/bin"
  }
  direct {}
}
```

Explain that users may need separate CLI config files for Terraform and OpenTofu depending on their deployment setup.

---

## Main entrypoint

Implement `main.go` similar to standard provider framework providers:

```go
package main

import (
    "context"
    "flag"
    "log"

    "github.com/hashicorp/terraform-plugin-framework/providerserver"
    "github.com/gpupaas-ai/terraform-provider-gpupaas/internal/provider"
)

var version = "dev"

func main() {
    var debug bool
    flag.BoolVar(&debug, "debug", false, "set to true to run the provider with support for debuggers")
    flag.Parse()

    opts := providerserver.ServeOpts{
        Address: "registry.terraform.io/gpupaas-ai/gpupaas",
        Debug:   debug,
    }

    err := providerserver.Serve(context.Background(), provider.New(version), opts)
    if err != nil {
        log.Fatal(err.Error())
    }
}
```

---

## Implementation order

Implement in this order:

1. Initialize `go.mod`.
2. Add `main.go` provider server entrypoint.
3. Add provider skeleton with schema and configuration.
4. Add internal client wrapper around `gpupaas-go`.
5. Add common metadata and ID parsing utilities.
6. Add `gpupaas_project` resource.
7. Add `gpupaas_project` data source.
8. Add `gpupaas_workspace` resource and data source.
9. Add `gpupaas_workspace_collaborator` resource.
10. Add `gpupaas_virtual_machine` data source.	
11. Add `gpupaas_storage` resource.
12. Add `gpupaas_security_group` resource.
13. Add `gpupaas_ssh_key` resource.
14. Add conversion tests.
15. Add resource unit tests.
16. Add acceptance test skeletons guarded by `TF_ACC=1`.
17. Add examples.
18. Add docs.
19. Add README.
20. Run formatting, tidy, and tests.

---

## Acceptance criteria

The implementation is acceptable when all of these work:

```bash
go mod tidy
go fmt ./...
go test ./...
go build -o bin/terraform-provider-gpupaas .
```

The provider should also support local development with Terraform or OpenTofu using a dev override.

**Do not maintain a fixed resource list in this prompt.** Treat the SDK as the source of truth for which GPU PaaS objects exist and how they are shaped:

- **Resource catalog:** [gpupaas-go README — Appendix: Resource reference](https://github.com/gpupaas-ai/gpupaas-go/blob/main/README.md#appendix-resource-reference)
- **Go types and kinds:** `github.com/gpupaas-ai/gpupaas-go/apis/v1alpha1` (`Kind*` constants, `types.go`, `register.go`)
- **Typed clients:** `github.com/gpupaas-ai/gpupaas-go/clientset/typed/v1alpha1`

Whenever the SDK adds a new `Kind`, add matching Terraform resources and/or data sources in this provider. Derive Terraform type names from the SDK kind using snake_case with the `gpupaas_` prefix (for example `Project` → `gpupaas_project`, `WorkspaceCollaborator` → `gpupaas_workspace_collaborator`, `SshKey` → `gpupaas_ssh_key`). Map each resource’s schema from the SDK README section for that kind: `metadata`, `spec`, and computed `status` fields.

Implement **ImportState** for every managed resource. Build import IDs from SDK metadata scope (see README “Shared metadata conventions”):

| Scope | Required metadata | Import ID format | Example |
|---|---|---|---|
| Project (cluster-scoped) | `metadata.name` | `<name>` | `demo` |
| Project-scoped | `metadata.project`, `metadata.name` | `<project>/<name>` | `demo/my-ssh-key` |
| Workspace-scoped | `metadata.project`, `metadata.workspace`, `metadata.name` | `<project>/<workspace>/<name>` | `demo/dev/alice@example.com` |

For **VirtualMachine**, use a two-segment ID when `metadata.workspace` is empty (project-scoped VM) and a three-segment ID when workspace is set. Document any kind-specific import rules in that resource’s docs page.

Use this minimal smoke-test configuration for local dev (extend it using YAML/examples from the SDK README for each kind you implement):

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

# Add additional gpupaas_* resources and data sources here following
# the SDK README appendix for each supported Kind.
```

After resources exist in the API, **import must work for every managed resource** exposed by the provider. Examples (patterns only — substitute names from your config):

```bash
terraform import gpupaas_project.demo demo
terraform import gpupaas_workspace.dev demo/dev
# Project-scoped:  terraform import gpupaas_<kind>.<label> <project>/<name>
# Workspace-scoped: terraform import gpupaas_<kind>.<label> <project>/<workspace>/<name>
```

---

## Constraints

- Do not implement a separate HTTP client in this provider.
- Do not duplicate API types from `gpupaas-go` unless absolutely required for Terraform state models.
- Do not store secrets in state unless unavoidable; token must never be stored as a normal non-sensitive attribute.
- Do not panic during provider operations.
- Do not use global mutable SDK clients.
- Do not require a real API for unit tests.
- Do not assume Terraform-only behavior that would break OpenTofu compatibility.
- Do not implement the CLI in this repo.
- Do not use Kubernetes client-go.

---

## Notes from the CLI/SDK steering design

The CLI/SDK architecture is Kubernetes-inspired: typed Go clients, dynamic/unstructured support, declarative specs, resource model using `apiVersion`, `kind`, `metadata`, `spec`, and `status`, and a thin CLI layered on the reusable Go client. This provider must follow the same layering principle: Terraform provider code is a consumer of the SDK, not a separate implementation of API behavior.

---

## Final deliverable

Deliver a complete initial provider implementation for:

```text
github.com/gpupaas-ai/terraform-provider-gpupaas
```

The result must compile, pass unit tests, expose Terraform resources and data sources for every SDK `Kind` documented in [gpupaas-go README](https://github.com/gpupaas-ai/gpupaas-go/blob/main/README.md#appendix-resource-reference), and be ready for local Terraform/OpenTofu development testing.
