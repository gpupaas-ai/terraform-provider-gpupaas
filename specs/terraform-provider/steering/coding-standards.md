---
inclusion: auto
---

# terraform-provider-gpupaas — Coding Standards

## Framework conventions

- Use **Terraform Plugin Framework** exclusively
- Typed schema attributes: `schema.StringAttribute`, `schema.Int64Attribute`, `schema.MapAttribute`, `schema.ListNestedAttribute`, `schema.SingleNestedAttribute`
- Typed state values: `types.String`, `types.Int64`, `types.Bool`, `types.Map`, `types.List`, `types.Object`
- Mark `token` as sensitive in provider schema
- Use `terraform-plugin-log` for structured logging — never log token values

## Go conventions

- Go 1.22+
- Context as first parameter in Configure/Create/Read/Update/Delete
- `gofmt` before commit
- Wrap SDK errors in diagnostics with context

## Package layout

| Package | Role |
|---|---|
| `internal/provider` | Provider definition, schema, configure |
| `internal/client` | Clientset configure data, fakes |
| `internal/resources` | Managed resources, converters, import |
| `internal/datasources` | Read-only data sources |
| `internal/acctest` | Acceptance test helpers |

Keep business logic in resources/converters — not in `main.go`.

## Resource naming

Terraform type names use the `gpupaas_` prefix and snake_case derived from SDK kinds:

```text
Project               → gpupaas_project
Workspace             → gpupaas_workspace
WorkspaceCollaborator → gpupaas_workspace_collaborator
VirtualMachine        → gpupaas_virtual_machine
Storage               → gpupaas_storage
SecurityGroup         → gpupaas_security_group
SshKey                → gpupaas_ssh_key
```

When the SDK adds a new `Kind`, add the corresponding `gpupaas_*` resource and/or data source.

Data sources use the same logical names as resources where both exist.

## Schema conventions

All resources include where applicable:

- `name` — required, ForceNew
- `project` — required for project-scoped and workspace-scoped resources, ForceNew
- `workspace` — required for workspace-scoped resources, ForceNew
- `labels`, `annotations` — optional maps
- `id` — computed
- status fields — computed only (`phase`, `status`, `reason`, etc.)

Map resource-specific fields from the SDK README section for that kind.

## Import IDs

| Scope | Format | Example |
|---|---|---|
| Project (cluster) | `<name>` | `demo` |
| Project-scoped | `<project>/<name>` | `demo/my-ssh-key` |
| Workspace-scoped | `<project>/<workspace>/<name>` | `demo/dev/alice@example.com` |
| VirtualMachine (project) | `<project>/<name>` | `demo/my-vm` |
| VirtualMachine (workspace) | `<project>/<workspace>/<name>` | `demo/dev/my-vm` |

Validate non-empty, correct segment count, clear diagnostics on failure.

Implement `ImportState` on **every** managed resource.

## SDK usage

Always use gpupaas-go clients:

```go
cs.V1alpha1().Projects().Create(ctx, obj, gpupaas.CreateOptions{})
cs.V1alpha1().Workspaces(project).Get(ctx, name, gpupaas.GetOptions{})
cs.V1alpha1().Storages(project).Create(ctx, obj, gpupaas.CreateOptions{})
cs.V1alpha1().Workspaces(project).Storages(workspace).List(ctx, gpupaas.ListOptions{})
```

Check errors with:

```go
gpupaas.IsNotFound(err)
gpupaas.IsUnauthorized(err)
gpupaas.IsForbidden(err)
gpupaas.IsConflict(err)
```

## Testing standards

**Unit tests** (required, no credentials):

```bash
go test ./...
```

Cover: provider schema, configure, converters (all kinds), import parsing (1/2/3 segment).

**Acceptance tests** (optional, gated):

```bash
TF_ACC=1 GPUPAAS_ENDPOINT=... GPUPAAS_TOKEN=... go test ./... -v -count=1
```

Skip acceptance tests when `TF_ACC` is unset.

## Documentation

- README: Terraform + OpenTofu note, install, dev override, examples, import patterns, testing, SDK link
- `docs/` for registry publication — one page per resource and data source
- `examples/` for copy-paste HCL; extend smoke config per SDK README
- Document kind-specific import rules on each resource docs page

## Makefile targets

```makefile
build test testacc fmt lint install-local
```

Document dev override path in README when install layout differs between Terraform/OpenTofu versions.

## Security checklist

- [ ] Token marked sensitive
- [ ] No token in logs or diagnostics
- [ ] No token stored in resource state
- [ ] Endpoint normalized (no trailing slash)

## Code review checklist

- [ ] No direct HTTP outside gpupaas-go
- [ ] All SDK-documented kinds have managed resources and/or data sources as appropriate
- [ ] ImportState on every managed resource
- [ ] NotFound read removes state; NotFound delete succeeds
- [ ] Converters exclude status on write
- [ ] Unit tests pass without API
- [ ] `go build` produces plugin binary
- [ ] New SDK kinds follow naming convention (`gpupaas_<snake_case>`)

## Dependencies

Pin tagged gpupaas-go versions in go.mod. Use local `replace` only for development:

```go
replace github.com/gpupaas-ai/gpupaas-go => ../gpupaas-go
```

Run `go mod tidy` after changes.

## Source prompt

Authoritative implementation details: `specs/terraform_provider_gpupaas_cursor_prompt.md`
