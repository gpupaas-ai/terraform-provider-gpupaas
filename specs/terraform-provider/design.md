# Design: terraform-provider-gpupaas

## Overview

The provider is a thin Terraform Plugin Framework adapter over `gpupaas-go`:

```text
Terraform / OpenTofu CLI
        ↓
Plugin Framework (provider, resource, datasource)
        ↓
internal/resources + internal/datasources  (schema, state models, converters)
        ↓
internal/client  (wraps gpupaas-go clientset)
        ↓
gpupaas-go → gpupaas.ai API
```

Reference layering: `paasctl/specs/kubectl_style_cli_architecture_prompt.md` — consumers stay thin; SDK owns API behavior.

**Source of truth for resource shapes:** [gpupaas-go README appendix](https://github.com/gpupaas-ai/gpupaas-go/blob/main/README.md#appendix-resource-reference) and `apis/v1alpha1` types.

## Repository layout

```text
github.com/gpupaas-ai/terraform-provider-gpupaas/
├── go.mod
├── go.sum
├── main.go
├── Makefile
├── README.md
├── LICENSE
├── internal/
│   ├── provider/
│   │   ├── provider.go
│   │   ├── provider_test.go
│   │   ├── config.go
│   │   ├── diagnostics.go
│   │   ├── validators.go
│   │   └── models.go
│   ├── client/
│   │   ├── client.go      # Clientset holder / configure helper
│   │   └── fake.go        # Test doubles
│   ├── resources/
│   │   ├── common.go
│   │   ├── metadata.go
│   │   ├── converters.go
│   │   ├── project_resource.go
│   │   ├── project_resource_test.go
│   │   ├── workspace_resource.go
│   │   ├── workspace_resource_test.go
│   │   ├── workspace_collaborator_resource.go
│   │   ├── virtual_machine_resource.go
│   │   ├── storage_resource.go
│   │   ├── security_group_resource.go
│   │   └── ssh_key_resource.go
│   ├── datasources/
│   │   ├── project_data_source.go
│   │   ├── workspace_data_source.go
│   │   └── virtual_machine_data_source.go
│   └── acctest/
│       ├── config.go
│       └── provider_test.go
├── examples/
│   ├── provider/
│   ├── resources/          # gpupaas_project, gpupaas_workspace, dev resources, …
│   └── data-sources/
├── docs/
│   ├── index.md
│   ├── resources/
│   └── data-sources/
└── templates/
```

Go 1.22+, module `github.com/gpupaas-ai/terraform-provider-gpupaas`.

## Dependencies

```text
github.com/hashicorp/terraform-plugin-framework
github.com/hashicorp/terraform-plugin-framework-validators
github.com/hashicorp/terraform-plugin-go
github.com/hashicorp/terraform-plugin-log
github.com/hashicorp/terraform-plugin-testing   # acceptance tests
github.com/gpupaas-ai/gpupaas-go              # required SDK
```

Use tagged gpupaas-go versions; run `go mod tidy` after changes.

## Provider type

```go
func (p *GPUProvider) Metadata(...) {
    resp.TypeName = "gpupaas"
    resp.Version = p.version
}

func (p *GPUProvider) Resources(...) []func() resource.Resource {
    return []func() resource.Resource{
        resources.NewProjectResource,
        resources.NewWorkspaceResource,
        resources.NewWorkspaceCollaboratorResource,
        resources.NewVirtualMachineResource,
        resources.NewStorageResource,
        resources.NewSecurityGroupResource,
        resources.NewSshKeyResource,
    }
}

func (p *GPUProvider) DataSources(...) []func() datasource.DataSource {
    return []func() datasource.DataSource{
        datasources.NewProjectDataSource,
        datasources.NewWorkspaceDataSource,
        datasources.NewVirtualMachineDataSource,
    }
}
```

Implement:

- `provider.Provider`
- `provider.ProviderWithMetadata`

Store configured `clientset.Interface` in `ResourceConfigure` / `DataSourceConfigure` via provider metadata.

## Provider schema

| Attribute | Type | Required | Sensitive | Env fallback | Default |
|---|---:|---:|---:|---|---|
| `endpoint` | string | optional | no | `GPUPAAS_ENDPOINT` | `https://console.gpupaas.ai` |
| `token` | string | optional | yes | `GPUPAAS_TOKEN` | — |
| `user_agent` | string | optional | no | — | provider default |

Configure flow:

1. Merge HCL attributes with env fallbacks
2. Build `gpupaas.Config`
3. `clientset.NewForConfig(config)`
4. Pass clientset to resources/data sources

## Resource naming

Derive Terraform type names from SDK kinds:

```text
<SDK Kind PascalCase> → gpupaas_<snake_case>
Project               → gpupaas_project
WorkspaceCollaborator   → gpupaas_workspace_collaborator
VirtualMachine          → gpupaas_virtual_machine
SecurityGroup           → gpupaas_security_group
SshKey                  → gpupaas_ssh_key
Storage                 → gpupaas_storage
```

Add managed resources when the typed client supports create/update/delete; add data sources when get/list is the primary operation.

## Resource mapping

### Common conventions

All resources include where applicable:

```hcl
name        = string
project     = string   # project-scoped and workspace-scoped resources
workspace   = string   # workspace-scoped resources only
labels      = map(string), optional
annotations   = map(string), optional
```

| Terraform | SDK |
|---|---|
| `name` | `metadata.name` |
| `project` | `metadata.project` |
| `workspace` | `metadata.workspace` |
| `labels` | `metadata.labels` |
| `annotations` | `metadata.annotations` |
| spec fields | `spec.*` |
| computed status | `status.*` |

Do not expose `apiVersion` / `kind` as required Terraform arguments. Set internally to `gpupaas.ai/v1alpha1` and the appropriate kind.

Use Plugin Framework typed values (`types.String`, `types.Int64`, `types.Map`, `types.List`, `types.Object`) — avoid `map[string]interface{}` in state models.

### gpupaas_project

- State ID: `<name>`
- Import: `<name>`
- ForceNew: `name`
- Computed: `id`, `phase`
- SDK: `cs.V1alpha1().Projects()`

### gpupaas_workspace

- State ID: `<project>/<name>`
- Import: `<project>/<name>`
- ForceNew: `project`, `name`
- Computed: `id`, `phase`
- SDK: `cs.V1alpha1().Workspaces(project)`

### gpupaas_workspace_collaborator

- State ID: `<project>/<workspace>/<name>` (name is typically user email)
- Import: `<project>/<workspace>/<name>`
- ForceNew: `project`, `workspace`, `name`
- SDK: workspace collaborator typed client
- Example import: `terraform import gpupaas_workspace_collaborator.alice demo/dev/alice@example.com`

### gpupaas_virtual_machine

- Project-scoped: state/import `<project>/<name>` when `workspace` is unset
- Workspace-scoped: state/import `<project>/<workspace>/<name>`
- SDK: `cs.V1alpha1().VirtualMachines(project)` or `cs.V1alpha1().Workspaces(project).VirtualMachines(workspace)`
- Supports Start/Stop via SDK action methods where exposed; map `status` fields as computed
- Data source: read-only get/list

### gpupaas_storage

- Dual scope (project or workspace) — same import ID rules as VirtualMachine
- SDK: `Storages(project)` or `Workspaces(project).Storages(workspace)`
- CRUD only (no status/action endpoints in SDK)

### gpupaas_security_group

- Dual scope — same pattern as Storage
- SDK: `SecurityGroups(project)` or `Workspaces(project).SecurityGroups(workspace)`
- Nested rule types: `ip_rules`, `port_forward_rules`, `rules` — use `schema.ListNestedAttribute`

### gpupaas_ssh_key

- Dual scope — same pattern as Storage
- SDK: `SshKeys(project)` or `Workspaces(project).SshKeys(workspace)`

## Model conversion

`internal/resources/converters.go` — one pair per kind:

```go
func ProjectModelToSDK(ctx context.Context, model ProjectModel) (*v1alpha1.Project, diag.Diagnostics)
func ProjectSDKToModel(ctx context.Context, obj *v1alpha1.Project, model *ProjectModel) diag.Diagnostics
// Workspace, WorkspaceCollaborator, VirtualMachine, Storage, SecurityGroup, SshKey …
```

Rules:

- Preserve unknown/null Framework semantics
- Do not send computed status fields on create/update
- Use diagnostics, not panics
- API version: `gpupaas.ai/v1alpha1`

## Resource interfaces

Each resource implements:

- `resource.Resource`
- `resource.ResourceWithConfigure`
- `resource.ResourceWithImportState`

Each data source implements:

- `datasource.DataSource`
- `datasource.DataSourceWithConfigure`

Lifecycle:

| Operation | SDK call |
|---|---|
| Create | `Create` (POST collection for dev resources) |
| Read | `Get` — NotFound → remove from state |
| Update | `Update` or `Apply` where SDK uses upsert |
| Delete | `Delete` — NotFound → success |
| Import | Parse ID → set state → Read |

## Error handling

Use SDK helpers:

```go
gpupaas.IsNotFound(err)
gpupaas.IsConflict(err)
gpupaas.IsUnauthorized(err)
gpupaas.IsForbidden(err)
```

Diagnostic format:

```text
Unable to read GPU PaaS workspace
project=demo name=dev error=<sdk error>
```

## Import parsing

`internal/resources/common.go`:

```go
func ParseProjectImportID(id string) (name string, diags diag.Diagnostics)
func ParseProjectScopedImportID(id string) (project, name string, diags diag.Diagnostics)
func ParseWorkspaceScopedImportID(id string) (project, workspace, name string, diags diag.Diagnostics)
```

Reject empty and malformed IDs. VirtualMachine uses two- or three-segment IDs depending on scope.

## Testing

### Unit tests (always run)

- Provider schema and configure (attrs + env)
- Model ↔ SDK conversion per resource kind
- Import ID parsing (1-, 2-, and 3-segment)
- NotFound read → state remove (where mockable)

Use fake SDK client in `internal/client/fake.go`.

### Acceptance tests (`TF_ACC=1`)

- Full CRUD + import per managed resource
- Data source reads
- Skip unless `TF_ACC=1` and `GPUPAAS_ENDPOINT` / `GPUPAAS_TOKEN` set

## Documentation

- `README.md`: compatibility, install, dev override, examples, import patterns, testing, SDK link
- `docs/index.md`, `docs/resources/*.md`, `docs/data-sources/*.md`
- `examples/` mirroring registry layout; extend smoke config with additional `gpupaas_*` resources per SDK README

## Local dev override

Document in README:

```hcl
provider_installation {
  dev_overrides {
    "gpupaas-ai/gpupaas" = "/absolute/path/to/local/provider/bin"
  }
  direct {}
}
```

Note separate CLI config files for Terraform vs OpenTofu when needed.

Makefile target `install-local` for plugin path layout.

## main.go

Standard Framework provider server:

```go
providerserver.Serve(ctx, provider.New(version), providerserver.ServeOpts{
    Address: "registry.terraform.io/gpupaas-ai/gpupaas",
    Debug:   debug,
})
```

## Constraints

- No HTTP outside SDK
- No global mutable clients
- No secrets in logs or non-sensitive state
- No panics in provider paths
- Compatible with OpenTofu (standard plugin protocol)
- Do not assume a fixed resource list — follow SDK `Kind*` additions

## Relationship diagram

```text
gpupaas-go (SDK)
    ↑
    ├── paasctl (CLI)
    └── terraform-provider-gpupaas (this repo)
```

Both consumers share the declarative resource model: `apiVersion`, `kind`, `metadata`, `spec`, `status`.
