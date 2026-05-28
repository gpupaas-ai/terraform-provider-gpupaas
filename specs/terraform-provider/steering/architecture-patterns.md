---
inclusion: auto
---

# terraform-provider-gpupaas — Architecture Patterns

## Layering

```text
┌──────────────────────────────────────────┐
│  Terraform / OpenTofu CLI                │
└────────────────────┬─────────────────────┘
                     ↓ gRPC plugin protocol
┌──────────────────────────────────────────┐
│  internal/provider     Provider config   │
└────────────────────┬─────────────────────┘
                     ↓ clientset in Configure data
┌──────────────────────────────────────────┐
│  internal/resources    CRUD + import   │
│  internal/datasources    Read-only       │
│  converters.go           TF ↔ SDK      │
└────────────────────┬─────────────────────┘
                     ↓
┌──────────────────────────────────────────┐
│  gpupaas-go clientset                    │
└────────────────────┬─────────────────────┘
                     ↓
              gpupaas.ai API
```

## SDK as source of truth

Do not maintain a fixed resource list in provider code or specs. When implementing or extending:

1. Read [gpupaas-go README appendix](https://github.com/gpupaas-ai/gpupaas-go/blob/main/README.md#appendix-resource-reference)
2. Check `Kind*` constants in `apis/v1alpha1`
3. Check typed client methods in `clientset/typed/v1alpha1`
4. Derive Terraform name: `gpupaas_<snake_case_kind>`

## Plugin Framework interfaces

**Provider**

- `provider.Provider`
- `provider.ProviderWithMetadata`

**Resources**

- `resource.Resource`
- `resource.ResourceWithConfigure`
- `resource.ResourceWithImportState`

**Data sources**

- `datasource.DataSource`
- `datasource.DataSourceWithConfigure`

## Configure pattern

1. Provider `Configure` builds `clientset.Interface` from merged HCL + env config
2. Store clientset in a private configure data struct
3. Each resource/data source `Configure` retrieves clientset from provider metadata
4. No global mutable SDK clients

## State model pattern

Use Framework typed values in Terraform models:

```go
type ProjectModel struct {
    ID          types.String
    Name        types.String
    DisplayName types.String
    Phase       types.String
    Labels      types.Map types.String
    // ...
}
```

Avoid `map[string]interface{}` for state.

Use `schema.ListNestedAttribute` and `schema.SingleNestedAttribute` for nested spec types (e.g. SecurityGroup rules, VM sharing).

## Field mapping pattern

| Terraform | SDK |
|---|---|
| `name` | `metadata.name` |
| `project` | `metadata.project` |
| `workspace` | `metadata.workspace` |
| `labels`, `annotations` | `metadata.labels`, `metadata.annotations` |
| user spec fields | `spec.*` |
| computed attributes | `status.*` |

Set `apiVersion` and `kind` internally — do not require users to set them in HCL.

## Scope pattern

```text
Organization (implicit — from API token)
└── Project                    cluster-scoped
    ├── Storage / SecurityGroup / SshKey / VirtualMachine   project-scoped
    └── Workspace                project-scoped
        ├── WorkspaceCollaborator
        └── Storage / SecurityGroup / SshKey / VirtualMachine   workspace-scoped
```

Typed client routing:

- Project-scoped: `cs.V1alpha1().Storages(project)`
- Workspace-scoped: `cs.V1alpha1().Workspaces(project).Storages(workspace)`

## Lifecycle pattern

```text
Create  → SDK Create (POST collection for dev resources)
Read    → SDK Get (NotFound → remove from state)
Update  → SDK Update / Apply
Delete  → SDK Delete (NotFound → success)
Import  → parse ID → populate model → Read
```

Every managed resource implements `ImportState`.

## Conversion pattern

Centralize in `internal/resources/converters.go` — one ToSDK/FromSDK pair per kind:

```go
func ProjectModelToSDK(ctx, model) (*v1alpha1.Project, diag.Diagnostics)
func ProjectSDKToModel(ctx, obj, model) diag.Diagnostics
func StorageModelToSDK(ctx, model) (*v1alpha1.Storage, diag.Diagnostics)
// … Workspace, WorkspaceCollaborator, VirtualMachine, SecurityGroup, SshKey
```

Rules:

- Handle null/unknown Framework values correctly
- Never send status on write
- Return diagnostics, never panic

## Import pattern

| Scope | Import ID | Parser |
|---|---|---|
| Project | `<name>` | `ParseProjectImportID` |
| Project-scoped | `<project>/<name>` | `ParseProjectScopedImportID` |
| Workspace-scoped | `<project>/<workspace>/<name>` | `ParseWorkspaceScopedImportID` |

VirtualMachine: two-segment when workspace empty; three-segment when workspace set.

```go
func (r *workspaceCollaboratorResource) ImportState(ctx, req, resp) {
    project, workspace, name, diags := ParseWorkspaceScopedImportID(req.ID)
    // set state identity, call Read
}
```

Reject empty and malformed IDs with clear diagnostics.

## Error handling pattern

```go
if gpupaas.IsNotFound(err) {
    resp.State.RemoveResource(ctx)
    return
}
```

Diagnostics must include resource context, never tokens:

```text
Unable to read GPU PaaS storage
project=demo workspace=dev name=vol1 error=...
```

## Testing pattern

| Layer | Approach |
|---|---|
| Provider config | Unit test with env vars and HCL attrs |
| Converters | Table-driven model ↔ SDK tests per kind |
| Import parsing | 1-, 2-, 3-segment ID cases |
| Resources | Fake clientset in `internal/client/fake.go` |
| Acceptance | `terraform-plugin-testing`; gate with `TF_ACC=1` |

Unit tests MUST pass without real API credentials.

## Anti-patterns

- Do NOT implement HTTP in the provider
- Do NOT duplicate `gpupaas-go` API structs for business logic
- Do NOT log tokens
- Do NOT use Plugin SDK v2
- Do NOT use global clients
- Do NOT assume Terraform-only behavior that breaks OpenTofu
- Do NOT hard-code a fixed resource list — follow SDK `Kind` additions

## Shared declarative model

Same resource shape as CLI and SDK:

```yaml
apiVersion: gpupaas.ai/v1alpha1
kind: Workspace
metadata:
  name: dev
  project: demo
spec:
  displayName: Development
status:
  phase: Ready
```

Terraform HCL is an alternate surface for the same underlying objects.

## Reference

- [gpupaas-go README appendix](https://github.com/gpupaas-ai/gpupaas-go/blob/main/README.md#appendix-resource-reference) — resource shapes
- `specs/gpupaas-go/` — SDK specs (implement first)
- `specs/terraform_provider_gpupaas_cursor_prompt.md` — implementation prompt
- `paasctl/specs/kubectl_style_cli_architecture_prompt.md` — kubectl-style declarative CLI/SDK model
