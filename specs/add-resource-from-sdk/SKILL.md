---
name: add-resource-from-sdk
description: >-
  Add or update Terraform resources and data sources in
  terraform-provider-gpupaas based on the gpupaas-go SDK. Pulls the
  authoritative resource catalog from the SDK README appendix (inline if the
  user pastes it, otherwise from
  https://github.com/gpupaas-ai/gpupaas-go/blob/main/README.md), diffs it
  against the provider, and wires up Terraform-Plugin-Framework resources +
  data sources following the established patterns: snake_case `gpupaas_<kind>`
  naming, scope-aware import IDs, computed `status` blocks, status data
  sources, and a `desired_action` field for imperative SDK verbs
  (start/stop/reboot/rotate-credentials/run-command/...). Use whenever the
  user asks to add, refresh, or update Terraform resources for the GPU PaaS
  provider against a new or changed SDK release.
---

# Add / Update Terraform Resources from gpupaas-go

## When to apply

Use this skill whenever the user asks to add, regenerate, refresh, or update
Terraform resources / data sources in
`github.com/gpupaas-ai/terraform-provider-gpupaas`, anchored on the
`gpupaas-go` SDK. Triggers include:

- "Sync the provider with the latest SDK."
- "Add a Terraform resource for `<Kind>`."
- "Expose start/stop/reboot for VirtualMachine in Terraform."
- "Bump the gpupaas-go dependency and pick up new kinds."

This skill is the Terraform-side companion to
[`gpupaas-go/specs/add-resource-from-swagger/SKILL.md`](../../../gpupaas-go/specs/add-resource-from-swagger/SKILL.md).
Run the SDK skill **first** when the underlying API changes; then run this
skill to surface the change in the provider.

## Source of truth

The SDK README appendix is canonical for **what resources exist, their
fields, their scope, and their action verbs**. In priority order:

1. **README content provided inline in the prompt.** Treat as authoritative.
2. **The current local clone of `gpupaas-go`** (typical sibling checkout):
   ```text
   ../gpupaas-go/README.md
   ../gpupaas-go/apis/v1alpha1/
   ../gpupaas-go/clientset/typed/v1alpha1/interfaces.go
   ```
3. **The upstream URL** (only when the user has not provided either of the
   above):
   ```text
   https://github.com/gpupaas-ai/gpupaas-go/blob/main/README.md
   ```
   When fetching from the web, anchor on the
   `#appendix-resource-reference` section and verify the matching tagged
   version pinned in this repo's `go.mod`.

Whichever source is used, **always sanity-check the live SDK code**
(`apis/v1alpha1/types*.go`, `clientset/typed/v1alpha1/interfaces.go`,
`convert/paths.go`) to make sure the README hasn't drifted from the Go API.
The Go types win when in doubt.

## Inputs to gather

Before generating provider code, confirm:

| Input | Notes |
|------|-------|
| README source | Inline / local clone / URL (see above). |
| Target SDK kinds | One or more PascalCase kinds (e.g. `VirtualMachine`, `LoadBalancer`). If unspecified, diff the whole appendix against the provider. |
| Scope per kind | Cluster (`metadata.name`), project (`<project>/<name>`), workspace (`<project>/<workspace>/<name>`), or flexible 2-or-3 (`VirtualMachine` style). |
| Imperative actions | List of verbs the SDK exposes for the kind (`Start`, `Stop`, `Reboot`, `Action`, `RotateCredentials`, `RunCommand`, …). Note whether any take payloads (`ActionOptions{Envs, Variables}` etc.). |
| Status fields | From `apiv1.<Kind>Status` — these become Terraform `Computed: true`. |
| Extras | Sharing model (`DevSharingSpec` vs resource-specific), nested rule blocks, sensitive fields. |
| Provider version impact | Will this be additive, or does it require bumping the major resource version? Default to additive. |

If anything above is unclear, **ask before writing code**.

## Architecture map (always)

```text
internal/
├── provider/
│   ├── provider.go                 # Register resources + data sources
│   └── resources/
│       ├── common.go               # Import ID parsers, type helpers, error mapping
│       ├── metadata.go             # MetadataModel + Resource/Datasource attrs
│       ├── sharing.go              # SharingModel + DevSharingSpec / VirtualMachineSharingSpec converters
│       ├── <kind>_resource.go      # Managed resource (Create/Read/Update/Delete/Import)
│       ├── <kind>_data_source.go   # Read-only lookup
│       └── <kind>_status_data_source.go  # Optional helper for live status reads
└── client/
    ├── client.go                   # ProviderData holding clientset.Interface
    └── fake.go                     # In-memory fake clientset for unit tests

examples/
├── resources/gpupaas_<kind>/resource.tf
└── data-sources/gpupaas_<kind>/data-source.tf

docs/
├── resources/<kind>.md
└── data-sources/<kind>.md
```

## Naming rules

| SDK Kind                  | Terraform type             | Notes |
|---------------------------|----------------------------|-------|
| `Project`                 | `gpupaas_project`          | Cluster-scoped. |
| `Workspace`               | `gpupaas_workspace`        | Project-scoped. |
| `WorkspaceCollaborator`   | `gpupaas_workspace_collaborator` | Workspace-scoped. |
| `VirtualMachine`          | `gpupaas_virtual_machine`  | Flexible 2-or-3 scope. |
| `SecurityGroup`           | `gpupaas_security_group`   | Same. |
| `SshKey`                  | `gpupaas_ssh_key`          | Same. |
| `Storage`                 | `gpupaas_storage`          | Same. |
| *(new kind)*              | `gpupaas_<snake_case>`     | Derive automatically. |

Status data sources use the suffix `_status` (e.g.
`gpupaas_virtual_machine_status`). Reserve plain `gpupaas_<kind>` for the
read-by-name look-up data source.

## Workflow

Copy this checklist into the task and tick items off as you go:

```text
- [ ] Step 1 — Pull and diff the SDK appendix vs the provider
- [ ] Step 2 — Bump go.mod dependency on gpupaas-go (if needed)
- [ ] Step 3 — For each new/changed kind: design resource model
- [ ] Step 4 — Implement Resource (Create/Read/Update/Delete/Import)
- [ ] Step 5 — If the kind has actions, add `desired_action` + extras and
              implement the diff-on-Update pattern
- [ ] Step 6 — Surface `status.*` as Computed and add a status data source
- [ ] Step 7 — Add the read/lookup data source
- [ ] Step 8 — Register in internal/provider/provider.go
- [ ] Step 9 — Update internal/client/fake.go for the new interface
- [ ] Step 10 — Tests: converter round-trip + import parser + action diff
- [ ] Step 11 — Examples + docs (registry layout)
- [ ] Step 12 — Verify: gofmt + go vet + go test + go build
```

---

### Step 1 — Pull and diff

1. Resolve the README source (see *Source of truth*).
2. Enumerate every kind in the appendix. For each, note:
   - SDK Kind name, Terraform type name, scope, sharing model.
   - Spec fields (camelCase from SDK Go tags).
   - Status fields.
   - Action verbs exposed on the typed interface
     (`clientset/typed/v1alpha1/interfaces.go`).
3. Diff against `internal/provider/resources/` and `internal/provider/provider.go`:
   - **Missing kinds** → new resource + data source.
   - **Missing fields on an existing kind** → additive schema update.
   - **Removed fields** → STOP and ask the user. Default to keeping the
     field marked `// Deprecated:` in the converter; do not delete schema
     attributes silently.
   - **New action verbs** → extend the `desired_action` allow-list and
     implement the call site (Step 5).
   - **Scope changes** → never change scope on an existing resource without
     explicit user approval; it breaks every import ID.

Produce a short markdown plan before writing code and have the user confirm
any non-trivial item.

### Step 2 — Bump `gpupaas-go`

If the SDK has new symbols you depend on:

```bash
go get github.com/gpupaas-ai/gpupaas-go@<tag>
go mod tidy
```

When working from a local clone, ensure `go.mod` has a `replace` directive
pointing at it, but do **not** commit the replace.

### Step 3 — Design the resource model

Mirror the SDK envelope. For each managed resource:

```go
type <kind>ResourceModel struct {
    ID         types.String `tfsdk:"id"`
    APIVersion types.String `tfsdk:"api_version"`
    Kind       types.String `tfsdk:"kind"`
    Metadata   MetadataModel `tfsdk:"metadata"`
    Spec       <kind>Spec    `tfsdk:"spec"`
    Status     <kind>Status  `tfsdk:"status"`

    // Imperative action surface (only when the SDK exposes verbs):
    DesiredAction types.String `tfsdk:"desired_action"`
    // Optional payload that pairs with DesiredAction:
    ActionInputs  *<kind>ActionInputs `tfsdk:"action_inputs"`
}
```

Schema rules:

- Never expose `apiVersion` / `kind` as user-required. Set them internally
  to `apiv1.APIVersion` / `apiv1.Kind<Kind>` and mark them `Computed: true`.
- Required spec fields come from the SDK README's `required:` annotations
  and `apis/v1alpha1/types_dev.go`.
- Use `Computed: true` for every `status.*` field — never required, never
  optional.
- Sensitive fields (passwords, tokens, private keys) get `Sensitive: true`.
- Use `stringplanmodifier.UseStateForUnknown()` on `id`, computed status
  scalars, and immutable identifiers to keep diffs stable.
- Use `RequiresReplace` plan modifiers on fields the SDK cannot mutate
  (typically `metadata.name`, `metadata.project`, `metadata.workspace`).

Re-use shared building blocks:

- `MetadataResourceAttribute(projectScoped, workspaceScoped bool)` from
  `metadata.go`.
- `SharingResourceAttribute()` from `sharing.go`. Use the
  `VirtualMachineSharingSpec` helpers (`vmSharingToSDK` /
  `vmSharingFromSDK`) only for VM-style sharing.

### Step 4 — Implement the resource

The skeleton is the same for every dev resource (model on
`virtual_machine_resource.go`):

```go
type <kind>Resource struct{ pd *client.ProviderData }

func (r *<kind>Resource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
    r.pd = providerData(req.ProviderData, &resp.Diagnostics)
}

func (r *<kind>Resource) client(project, workspace string) typed.<Kind>Interface {
    if workspace == "" {
        return r.pd.Clientset.V1alpha1().<Kind>s(project)
    }
    return r.pd.Clientset.V1alpha1().Workspaces(project).<Kind>s(workspace)
}

// Create -> client.Create
// Read   -> client.Get; NotFound => resp.State.RemoveResource
// Update -> see Step 5 (action diff + idempotent Create for spec changes)
// Delete -> client.Delete with IgnoreNotFound: true
// Import -> ParseFlexibleScopedImportID for dual-scope dev resources,
//           ParseProjectScopedImportID for strictly project-scoped,
//           ParseClusterScopedImportID for cluster-scoped.
```

Rules:

- Always pass `ctx` first; respect `req.Plan.Get`/`req.State.Get`
  diagnostics.
- Strip computed status before writing — the converters in
  `<kind>ModelToSDK` must never copy `Status` into the SDK object.
- Map SDK errors via `handleAPIError(..., op, notFound)`; for `Read`,
  always supply the `resp.State.RemoveResource(ctx)` callback so 404s drop
  the resource cleanly.
- ID format follows the import ID format (`<name>` / `<project>/<name>` /
  `<project>/<workspace>/<name>`). Recompute it on every read so it stays
  in sync with the SDK truth.

For resources whose SDK has **no dedicated Update** endpoint (most dev
resources: `Storage`, `SecurityGroup`, `SshKey`, `VirtualMachine`), route
spec-only updates through `Create`:

```go
// SDK has no Update; Create is idempotent on the backend.
out, err := r.client(project, workspace).Create(ctx, obj, gpupaas.CreateOptions{})
```

This is intentional — see `virtual_machine_resource.go::Update`.

### Step 5 — Imperative actions via `desired_action`

> **Pattern requirement (verbatim user intent):** Actions like `start`,
> `stop`, `reboot`, `power_cycle`, `rotate_credentials`, `run_command` are
> imperative and must be modelled as a `desired_action` field. The Update
> path compares old vs. new state and dispatches the matching SDK call.

#### 5.1 Schema additions

```go
"desired_action": schema.StringAttribute{
    Optional: true,
    MarkdownDescription: "Imperative action to perform on the resource. " +
        "Set to one of: `none`, `start`, `stop`, `reboot`. " +
        "Changing this attribute triggers the corresponding SDK action; " +
        "setting it back to `none` is a no-op.",
    Validators: []validator.String{
        stringvalidator.OneOf("none", "start", "stop", "reboot"),
    },
    PlanModifiers: []planmodifier.String{
        // Treat unset as "none" to keep diffs stable.
        stringplanmodifier.UseStateForUnknown(),
    },
},
// Optional payload for actions that take envs/variables/etc.:
"action_inputs": schema.SingleNestedAttribute{
    Optional: true,
    Attributes: map[string]schema.Attribute{
        "envs":      schema.MapAttribute{Optional: true, ElementType: types.StringType},
        "variables": schema.MapAttribute{Optional: true, ElementType: types.StringType},
    },
},
```

If the SDK exposes additional inputs per verb (e.g. `RunCommand` needs a
`command string`), add them as siblings. Default to a nested attribute
named `action_inputs` so the schema stays stable as new verbs land.

The allow-list comes from the typed interface. For VirtualMachine today
(`VirtualMachineInterface`) the verbs are
`Start`, `Stop`, `Reboot`, `Action` — so the validator list is
`{"none", "start", "stop", "reboot"}` and any future verbs documented in
the SDK README appendix get appended. Never accept arbitrary strings —
keep the validator in sync with the typed interface.

#### 5.2 Update implementation

The provider's `Update` method handles **two orthogonal kinds of change**:

1. **Spec changes** — re-run the idempotent `Create` (or `Update` where the
   SDK exposes one).
2. **Action transitions** — compare old `desired_action` from prior state
   against new `desired_action` from the plan, and call the matching SDK
   verb.

```go
func (r *<kind>Resource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
    var plan, state <kind>ResourceModel
    resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
    resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
    if resp.Diagnostics.HasError() {
        return
    }
    project   := plan.Metadata.Project.ValueString()
    workspace := plan.Metadata.Workspace.ValueString()
    name      := plan.Metadata.Name.ValueString()
    client    := r.client(project, workspace)

    // 1) Spec reconciliation — backend Create is idempotent.
    obj := <kind>ModelToSDK(ctx, plan, &resp.Diagnostics)
    if resp.Diagnostics.HasError() {
        return
    }
    out, err := client.Create(ctx, obj, gpupaas.CreateOptions{})
    if err != nil {
        handleAPIError(&resp.Diagnostics, err, "Update <kind>", nil)
        return
    }

    // 2) Imperative action dispatch.
    oldAction := stringOr(state.DesiredAction, "none")
    newAction := stringOr(plan.DesiredAction, "none")
    if newAction != "" && newAction != "none" && newAction != oldAction {
        actionOpts := buildActionOptions(plan.ActionInputs)
        switch newAction {
        case "start":
            out, err = client.Start(ctx, name, actionOpts)
        case "stop":
            out, err = client.Stop(ctx, name, actionOpts)
        case "reboot":
            out, err = client.Reboot(ctx, name, actionOpts)
        default:
            // Forward-compatible: any verb the SDK exposes via Action()
            // can be reached here. Keep the validator's allow-list in sync.
            out, err = client.Action(ctx, name, newAction, actionOpts)
        }
        if err != nil {
            handleAPIError(&resp.Diagnostics, err, "Execute "+newAction+" on <kind>", nil)
            return
        }
    }

    // 3) Persist the resolved state. desired_action is preserved verbatim;
    // the observed status (status.action, status.status, ...) comes from
    // the SDK response.
    model := <kind>SDKToModel(ctx, out, project, workspace, &resp.Diagnostics)
    model.DesiredAction = plan.DesiredAction
    model.ActionInputs  = plan.ActionInputs
    resp.Diagnostics.Append(resp.State.Set(ctx, model)...)
}
```

Helper:

```go
func buildActionOptions(in *<kind>ActionInputs) gpupaas.ActionOptions {
    if in == nil {
        return gpupaas.ActionOptions{}
    }
    return gpupaas.ActionOptions{
        Envs:      mapsFromTF(in.Envs),
        Variables: mapsFromTF(in.Variables),
    }
}
```

#### 5.3 Idempotency & state semantics

- `desired_action` lives in **config + state**, not in the SDK envelope.
  It is purely a Terraform-side "trigger" attribute. Do **not** map it to
  any SDK field on writes.
- The SDK round-trip never echoes `desired_action` back, so the converter
  `<kind>SDKToModel` must not touch it. The Update path explicitly copies
  `plan.DesiredAction` onto the post-call state to keep the value
  consistent.
- Read (refresh) does the same — preserve the prior `desired_action` from
  state; never overwrite it from the SDK.
- Observed action state lives at `status.action` (read-only). Users compare
  `desired_action` against `status.status` (e.g. `"running"`/`"stopped"`)
  to detect drift.
- Setting `desired_action = "none"` is the explicit no-op. Treat empty /
  null as equivalent to `"none"` so HCL omitting the field never triggers
  a call.

#### 5.4 Drift handling

If the user sets `desired_action = "start"` and the backend later transitions
the VM back to `stopped` (out-of-band shutdown, scheduler eviction),
Terraform will see `status.status != "running"` on the next refresh. It is
**not** the provider's job to auto-re-trigger the action — surface the
drift through the computed `status` block and let the user re-apply.

This matches the canonical pattern:

```text
if oldState != newState {
    switch newState {
    case "running": callStartInstanceAPI()
    case "stopped": callStopInstanceAPI()
    }
}
```

with `oldState`/`newState` taken from prior `desired_action` vs. planned
`desired_action`, not from the observed status.

### Step 6 — Status as Computed + status data source

Every kind that has an `apiv1.<Kind>Status` block surfaces it as a
`Computed: true` nested object on the resource. Mirror the SDK field
ordering and casing rules (`status`, `reason`, `action`, `provisionedAt`,
…) and use `nullableString` for optional scalars.

In parallel, add a **status data source** for kinds that expose a live
`GetStatus` sub-route (e.g. `VirtualMachine`):

```go
// internal/provider/resources/virtual_machine_status_data_source.go
type virtualMachineStatusDataSource struct{ pd *client.ProviderData }

func (d *virtualMachineStatusDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
    resp.TypeName = req.ProviderTypeName + "_virtual_machine_status"
}

func (d *virtualMachineStatusDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
    // 1) Resolve project / workspace / name from config
    // 2) Call the appropriate clientset(.Workspaces).VirtualMachines(...).GetStatus(ctx, name, ...)
    // 3) Project the SDK status into the data source schema
}
```

The status data source mirrors the resource's `status` nested attribute
exactly so users can `data.gpupaas_virtual_machine_status.web.status` in
HCL just like they'd read `gpupaas_virtual_machine.web.status`.

Kinds with no live status endpoint still expose the `status` block on the
resource (populated from `Get`) but do **not** get a `_status` data
source — that prevents an extra RPC that just duplicates `gpupaas_<kind>`.

### Step 7 — Lookup data source

Every managed resource also gets a plain `data "gpupaas_<kind>"` look-up
that takes `name`, `project`, and optionally `workspace` and returns the
full resource envelope. Mirror the pattern in
`virtual_machine_data_source.go`.

For kinds that benefit from listing, add a sibling `_list` data source
(`gpupaas_virtual_machines`, etc.) that returns a `List` attribute of
read-only objects. Only add this when a single Terraform consumer needs to
iterate — it's lower priority than the single-name look-up.

### Step 8 — Provider registration

Append the new constructors in
`internal/provider/provider.go`:

```go
func (p *GPUProvider) Resources(...) []func() resource.Resource {
    return []func() resource.Resource{
        // existing...
        resources.New<Kind>Resource,
    }
}

func (p *GPUProvider) DataSources(...) []func() datasource.DataSource {
    return []func() datasource.DataSource{
        // existing...
        resources.New<Kind>DataSource,
        resources.New<Kind>StatusDataSource, // when applicable
    }
}
```

### Step 9 — Fake client

For every interface change in the SDK, mirror it in
`internal/client/fake.go`. The fake must satisfy the full typed interface
or `go build` fails. Pattern (from `fakeVMs`):

```go
func (v *fake<Kind>s) Start(ctx context.Context, name string, opts gpupaas.ActionOptions) (*apiv1.<Kind>, error) {
    return v.Action(ctx, name, "start", opts)
}
func (v *fake<Kind>s) Action(ctx context.Context, name, action string, _ gpupaas.ActionOptions) (*apiv1.<Kind>, error) {
    x, err := v.Get(ctx, name, gpupaas.GetOptions{})
    if err != nil {
        return nil, err
    }
    x.Status.Action = action
    v.f.<kind>s[v.key(name)] = x
    return x, nil
}
```

Keep the fake exhaustive — interfaces grow over time and missing methods
break unrelated tests.

### Step 10 — Tests

Add (or extend):

- `internal/provider/resources/converters_test.go` — round-trip
  `<kind>ModelToSDK ↔ <kind>SDKToModel`, including the `desired_action`
  passthrough rule (`desired_action` must survive `Read` → `Update` even
  though the SDK never echoes it back).
- `internal/provider/resources/common_test.go` — import ID parser cases
  for the kind's scope.
- A dedicated `<kind>_action_test.go` (or extend `<kind>_resource_test.go`)
  exercising the action-diff branch with the fake clientset: start from
  `desired_action="none"`, plan `"start"`, assert `Start` was called and
  state ends with `desired_action="start"` plus the fake-mutated
  `status.action`.

Run:

```bash
go test ./internal/...
```

Add `TF_ACC=1` acceptance tests only when the resource has a real backend
fixture available; otherwise document the gap and rely on unit tests.

### Step 11 — Examples + docs

Examples mirror the registry layout exactly:

```text
examples/resources/gpupaas_<kind>/resource.tf
examples/resources/gpupaas_<kind>/import.sh
examples/data-sources/gpupaas_<kind>/data-source.tf
examples/data-sources/gpupaas_<kind>_status/data-source.tf   # when present
```

Each `resource.tf` must demonstrate:

1. Required `metadata` (name + project + optional workspace).
2. A realistic `spec`.
3. The `desired_action` field when the kind supports actions, with a
   commented walkthrough of the lifecycle (e.g. `# Set to "stop" then
   apply; set back to "none" to leave the VM running.`).

Docs go under `docs/resources/<kind>.md` and `docs/data-sources/<kind>.md`,
following the registry's frontmatter format. Generate with
`tfplugindocs` once the schema is in place; hand-edit the action / status
sections for clarity.

### Step 12 — Verify

From the provider repo:

```bash
go fmt ./...
go vet ./...
go test ./...
go build ./...
```

Then sanity-check the registry-style examples render cleanly:

```bash
tfplugindocs generate
git diff docs/
```

All four `go` commands must pass; `git diff docs/` must be empty after
`tfplugindocs` regeneration (or contain only the intended new files).

---

## Imperative-action pattern (canonical reference)

> Use this verbatim when modelling **any** SDK verb that mutates lifecycle
> state without changing the desired spec — `Start`, `Stop`, `Reboot`,
> `PowerCycle`, `RotateCredentials`, `RunCommand`, and any future verbs.

```text
desired_action = "stop"      # config
                  │
                  ▼
Update(ctx, ...)
   oldAction := state.desired_action  ("running" / "none" / ...)
   newAction := plan.desired_action   ("stop")
   if newAction != "none" && newAction != oldAction {
       switch newAction {
       case "start":  client.Start(ctx, name, opts)
       case "stop":   client.Stop(ctx, name, opts)
       case "reboot": client.Reboot(ctx, name, opts)
       default:       client.Action(ctx, name, newAction, opts)
       }
   }
   plan.desired_action -> state.desired_action  (preserved verbatim)
   status.action       -> from SDK response     (observed)
```

Rules:

1. **One field per kind.** Never split into `start_at`, `stop_at`,
   `reboot_now` — collapse to `desired_action`. Per-verb parameters live
   in `action_inputs` (or a richer nested object when the SDK demands it).
2. **Stable validator.** The `OneOf` list comes from the typed interface
   methods. Update both when the SDK adds a verb. Drop a verb only with
   user approval — and mark the old value as `Deprecated:` first.
3. **Idempotent on plan diff.** Setting `desired_action = "start"` twice
   in a row produces zero calls on the second apply (because
   `oldAction == newAction`). This is intentional — re-triggering a verb
   requires either a fresh plan value, an explicit `taint`, or running the
   companion CLI.
4. **No spec inference.** `desired_action` must not change the SDK spec
   payload. Spec reconciliation and action dispatch are independent
   branches of `Update`.
5. **Sensitive payloads.** Mark `action_inputs.envs` /
   `action_inputs.variables` as `Sensitive: true` when the SDK
   documentation calls them out as secrets.

## Status pattern (canonical reference)

1. **Computed-on-resource.** Every resource exposes `status` as
   `Computed: true` and never as Required/Optional.
2. **Field-for-field mirror.** Schema mirrors `apiv1.<Kind>Status`
   verbatim — including nested objects like
   `virtualMachineOutput`. Add fields as the SDK grows.
3. **Status data source.** Kinds with a live `GetStatus` sub-route get a
   `gpupaas_<kind>_status` data source. Schema mirrors the resource's
   `status` block exactly so consumers can swap the lookup without
   changing references.
4. **Never write Status.** Converters must zero `Status` before the SDK
   call (defense-in-depth even though the SDK strips it again).
5. **Use status to detect drift.** Document that
   `status.status != desired_action`-derived expectation is the user
   signal to re-apply.

## Backward-compatibility rules (hard requirements)

1. **Never silently remove a Terraform attribute, resource type, or data
   source.** If the SDK drops a field, ask the user; default to keeping
   the attribute and marking it `DeprecationMessage:` in the schema.
2. **Never rename an attribute.** Add a new attribute and keep the old
   alongside until the user accepts the breaking change. State migrations
   are required if a rename ever ships.
3. **Never change the import ID format on an existing resource.** It
   silently breaks every user's `terraform import` workflow. If scope
   genuinely changes (e.g. project-scoped → workspace-scoped) bump the
   resource to a new Terraform type or document a state migration.
4. **Never tighten validators on an existing field.** Loosen freely;
   tightening requires a major version bump.
5. **Never auto-trigger an action on refresh.** Actions are only ever
   triggered by an explicit `desired_action` plan diff.

## Anti-patterns

- ❌ Per-verb boolean / timestamp attributes (`start_now`, `last_started_at`)
  that aren't surfaced by the SDK.
- ❌ Writing `desired_action` back to any SDK field.
- ❌ Using `status.action` as the source of truth on Update — read the
  prior plan's `desired_action` instead.
- ❌ Adding HTTP / retry logic in the provider. The SDK owns transport.
- ❌ Calling `tfplugindocs` from CI without regenerating locally first —
  it produces churn-y diffs against hand-edited sections.
- ❌ Bumping the gpupaas-go dependency in the same PR that removes
  attributes. Split additive bumps from removals.

## Reference files (read these to mirror style)

- `internal/provider/resources/virtual_machine_resource.go` — flexible
  scope, sharing block, status mapping, idempotent Update.
- `internal/provider/resources/virtual_machine_data_source.go` — data
  source layout, projection from resource model.
- `internal/provider/resources/common.go` — import parsers, helpers,
  error mapping.
- `internal/provider/resources/metadata.go` — shared metadata schema.
- `internal/provider/resources/sharing.go` — DevSharingSpec and
  VirtualMachineSharingSpec converters.
- `internal/client/fake.go` — fake clientset with action methods.
- `../gpupaas-go/README.md` (Appendix: Resource reference) — canonical
  spec / status / action catalog.
- `../gpupaas-go/clientset/typed/v1alpha1/interfaces.go` — authoritative
  list of action verbs per kind.
