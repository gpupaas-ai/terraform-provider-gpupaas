# plan.md — VMAAS First-Class Entities in `terraform-provider-gpupaas`

**Branch:** `VMAAS-581-vmaas-tf-implementation` · **Status:** DECISIONS RESOLVED — READY TO IMPLEMENT
**Methodology:** spec-kit (constitution → specify → clarify → plan → tasks → implement → converge).
This document carries all phases in one file, as requested. It complements (and
where noted, amends) the existing `specs/terraform-provider/*`.

---

## 0. DECISION GATES (resolve before implementation)

### D1 — SDK v2 vs plugin-framework  ✅ RESOLVED: **plugin-framework** (pure, no mux)

> Decision (2026-07-07): proceed with terraform-plugin-framework per the
> recommendation below. Note the two libraries are mutually exclusive; "mux"
> only exists to mix them and is NOT used here.

The requirement says **"ensure we use SDK v2"**. Three verified facts conflict
with it:

1. **The named reference is not SDK v2.** `terraform-provider-rafay/internal/
   resource_mks_cluster/mks_cluster_resource_ext.go` is a **plugin-framework**
   file. terraform-provider-rafay is *muxed*: ~90 legacy resources on SDK v2
   (`rafay/`), but every **new** resource (MKS, EKS) is framework
   (`internal/provider/` + `internal/resource_*/`), served over protocol v6.
2. **This repo is already framework end-to-end**: provider, 9 resources,
   converters, fake clientset, unit tests — on `terraform-plugin-framework
   v1.16.1`. Its own steering doc *forbids* SDK v2 as an anti-pattern.
3. **HashiCorp position**: SDK v2 is maintenance-mode; framework is the
   recommended target for new providers. Framework `types.Set` +
   `RequiresReplace` plan modifiers natively express our order-agnostic and
   Day-2-restriction requirements.

**Recommendation (critical-architect hat): stay on plugin-framework.**
Rewriting to SDK v2 discards a working scaffold, contradicts the repo's own
constitution, and re-implements set semantics that framework gives us. The
"SDK v2" intent — *follow the proven rafay provider pattern* — is satisfied by
mirroring the rafay provider's **modern** half (the exact file cited).

**If SDK v2 is a hard organizational mandate**, the fallback is documented in
Appendix A (mux SDK v2 alongside the framework provider; reuse the generated
SDK v2 schemas in `rafay-common/pkg/hub/terraform/resource/*_schema.go`;
expand/flatten per `rafay/resource_taggroup.go`). Cost: mux plumbing, two
paradigms in one repo, hand-rolled set hashing. Everything else in this plan
(drift, sets, Day-2, OOB, tests) is paradigm-independent and carries over.

### D2 — Which API surface: `gpupaas-go` clientset (chosen)
The repo consumes `github.com/gpupaas-ai/gpupaas-go` (sibling checkout, now
cloned; repo builds and tests pass). It fronts the same Hub REST API
(`dev.envmgmt.io/v1`) that paasctl talks to. We stay on it. Note its VM client
has **no `Update`** — `Create` is the documented upsert; Day-2 restriction is
therefore enforced **client-side** (plan modifiers), not by the API.

### D3 — Entity scope vs SDK coverage  ✅ RESOLVED (paasctl-parity as target)
`gpupaas-go` exposes: Project, Workspace, Collaborator, VirtualMachine,
Storage, SecurityGroup, SshKey, Baremetal, MKS. It does **not** expose
Image, VmProfile, Vpc, Datacenter.

**paasctl comparison (evaluated):** paasctl talks raw REST to the Hub — no Go
SDK constraint — so it implements **all 7 user-managed dev entities**:
VM/Storage/SecurityGroup/SshKey (workspace-aware CRUD), Image/VmProfile
(project-scoped CRUD), Vpc (read-only get/list); Datacenter deliberately
excluded (admin-preconfigured). The Terraform provider is constrained by its
own constitution (SDK-only API access) + gpupaas-go's missing clients, so it
currently covers 4 of paasctl's 7.

Decision:
- **In scope now:** VirtualMachine (primary), SecurityGroup, Storage, SshKey —
  brought up to the requirements below.
- **Deferred until gpupaas-go grows the clients (paasctl-parity gap):**
  `gpupaas_image` + `gpupaas_vm_profile` resources; `gpupaas_vpc` **data
  source** (read-only). File the gpupaas-go issue as part of Phase 3 — the
  Hub endpoints already exist (paasctl proves them), so it's mechanical
  clientset work (Image/VmProfile: Create/Get/List/Delete; Vpc: Get/List).
- **Excluded permanently:** Datacenter — consistent with paasctl; referenced
  by name in `spec.datacenter`, never managed.

### D4 — Day-2 mutable set  ✅ RESOLVED
Requirement 9 says password + security-group only; requirement 4 says sharing
must work Day-2. **Mutable set = { `guest_password`, `security_group`,
`sharing` }** (confirmed).

**Immutable-field behavior — PRIMARY: option (b) hard error.** Editing any
immutable field fails the plan with a clear diagnostic
(`"cpu_count cannot be modified on an existing VM; destroy and recreate
explicitly"`). Chosen for safety: VMs carry data, and a hard error can never
destroy one by accident.
> Terminology note for reviewers: option **(a) `RequiresReplace`** is the
> Terraform-idiomatic standard (AWS/GCP style: plan shows
> `# forces replacement`, apply destroys+recreates). Option (b) is the
> stricter deviation, deliberately chosen here.

**BACKUP (implement-later switch): option (a) `RequiresReplace`.** The
implementation MUST make flipping trivial: all immutable attributes take their
plan modifier from one shared helper —
```go
// internal/provider/resources/immutable.go
// mode is a single package-level constant: ImmutableError (default) | ImmutableReplace
func immutable() planmodifier.String  // + Bool/Int64/Set variants
```
`ImmutableError` → custom modifier that adds an error diagnostic when the
value changes on an existing resource (state non-null, plan ≠ state).
`ImmutableReplace` → returns `stringplanmodifier.RequiresReplace()` etc.
Switching modes later = one-line change + re-run the plan-matrix tests (which
assert per-mode expectations).

**Sanity check vs paasctl (evaluated):** paasctl enforces *neither* — its
`apply` is pure pass-through (validate envelope → POST full manifest; server
decides). So Terraform is the first client to enforce immutability; the
testbed check below (Risk 2) stays mandatory to learn what the backend itself
does with an immutable-field upsert.

---

## 1. CONSTITUTION (principles — inherited + amended)

1. **The Hub owns the schema.** Provider converts faithfully; no invented fields.
2. **Server is the authority for authz** — provider surfaces denials, never
   pre-checks roles (same principle as paasctl).
3. **State is derived, never authored**: `Read` must reconstruct everything
   (except write-only secrets) from `Get`; the provider holds no local state →
   remote state backends (S3/consul/cloud) work by construction.
4. **Semantic equality over textual equality**: collections whose order the
   backend does not guarantee are modeled as *sets*; two configs that mean the
   same thing must produce an empty plan.
5. **Every async operation has a bounded wait** with ticker + timeout +
   exponential backoff on transient errors (rafay `WaitFor*` pattern).
6. **Unit tests run offline** (fake clientset, converter round-trips);
   acceptance tests are `TF_ACC`-gated.
7. **Amendment to existing steering:** add the Drift, Order-Insensitivity,
   Day-2-Restriction, and Async-Wait requirements (they are absent from
   `specs/terraform-provider/requirements.md` today).

---

## 2. SPECIFICATION (WHAT — with acceptance criteria)

### FR-1 Provider paradigm
Per D1. Acceptance: single paradigm across the repo; `make build/test` green.

### FR-2 Remote state management
The provider must be fully compatible with any Terraform state backend:
- No files/caches outside Terraform state; all knowledge from API + state.
- `terraform import` for every resource (`project/name` or
  `project/workspace/name` flexible IDs — already present, keep + test).
- `terraform refresh` fully reconciles state from `Get`.
**Acceptance:** import → plan shows *no diff* for an unmodified resource;
state file relocation (local→S3) changes nothing.

### FR-3 Drift detection
- `Read` maps every server-returned spec field into state.
- Computed/status fields never generate diffs.
- Write-only fields (see FR-10) never false-drift.
**Acceptance:** mutate a field out-of-band via API/CLI → `terraform plan`
shows exactly that field; unmodified resources plan clean.

### FR-4 Order-agnostic sharing (incl. Day-2)
`sharing.workspaces`: set of strings. `sharing.projects`: **set** of
`{ name, workspaces(set) }`.
**Acceptance:** (a) reordering projects/workspaces in HCL → empty plan;
(b) server returns memberships in different order → empty plan; (c) Day-2 add/
remove of a shared project produces a minimal, correct diff and applies
successfully regardless of ordering.

### FR-5 Order-agnostic security-group rules
`rules`, `ip_rules`, `port_forward_rules`: sets of rule objects.
**Acceptance:** same three criteria as FR-4 applied to rules; duplicate rule
detection is the server's concern (set semantics de-dupe identical rules —
documented behavior).

### FR-6 Deletion (async)
`Delete` → server soft-delete (`DESTROYPENDING`) → poll until gone
(404/`DESTROYED`), bounded by configurable timeout; `DESTROYFAILED` → error
with server reason. Delete of an already-gone resource succeeds.
**Acceptance:** `terraform destroy` blocks until the VM is actually destroyed
or fails with the reason; re-destroy is a no-op.

### FR-7 Out-of-band detection
- OOB **deletion**: `Read` 404 → remove from state → plan proposes re-create.
- OOB **modification**: FR-3 covers it.
- OOB **password change**: undetectable by design (write-only) — documented.
**Acceptance:** delete a VM via paasctl → `terraform plan` proposes create.

### FR-8 Unit tests
Converter round-trips; **order-insensitivity proofs** (permuted fixtures ⇒
semantically-equal values); waiter state machine (fake clientset returning
SUBMITTED→SUCCESS / FAILED / 404 sequences); Day-2 plan-modifier matrix;
import-ID parsing. All offline. Acceptance harness (`terraform-plugin-testing`
+ `NewWithClientset(fake)`) for full lifecycle: create→drift→update→destroy.
**Acceptance:** `go test ./...` green offline; lifecycle tests pass with fake.

### FR-9 Day-2 restriction (per D4)
Mutable: `guest_password`, `security_group`, `sharing`. Everything else in
`spec` (type/profile, cpu_count, memory, image, vpc, subnet, datacenter,
ssh_key, assign_public_ip, dns_servers, user_data, timezone, shared_storage,
block_storage_type, boot_disk_size, create_additional_block,
additional_block_size) and `metadata.{name,project,workspace}` ⇒
**immutable — plan-time hard error** (D4 option (b) primary; option (a)
`RequiresReplace` available behind the `immutable()` mode switch).
**Acceptance (ImmutableError mode, default):** changing `cpu_count` fails
`terraform plan` with `cpu_count cannot be modified on an existing VM;
destroy and recreate explicitly`; changing password/SG/sharing plans an
in-place update; `Update` pushes the upsert and waits per FR-6 semantics.
**Acceptance (ImmutableReplace mode, backup):** same edit instead plans
`# forces replacement`. The plan-matrix tests cover both modes.

### FR-10 Sensitive/write-only password
`guest_password`: `Sensitive: true`; treated as write-only — `Read`/`FromHub`
never overwrites state with the (absent/encrypted) server value.
**Acceptance:** plan after apply is empty even though `Get` doesn't return the
password; password change plans in-place update.

### FR-11 Validations (parity with paasctl first-class work)
`metadata.name` + `metadata.project` required; `workspace` optional
(workspace-scoped path when set — VM/Storage/SG/SshKey only);
`sharing.share_mode` ∈ {None, All, Custom} with `projects` only meaningful for
Custom (validator); read-only kinds are data sources, never resources;
`desired_action` ∈ {none,start,stop,reboot}. Storage: model **both** `type`
(profile/tier) and `storage_type` (block|file|object) per devpb schema.

---

## 3. TECHNICAL PLAN (HOW)

### 3.1 Architecture (framework path, per D1 recommendation)
Keep the existing layering; close the gaps:

```
main.go → internal/provider (Configure → gpupaas-go clientset → ProviderData)
        → internal/provider/resources/*  (schema + CRUD + plan modifiers)
        → converters (model ↔ apiv1 SDK types)   [order-normalizing]
        → internal/wait (NEW: ticker/timeout/backoff pollers)
        → internal/client/fake (extended: status sequences, delete latency)
```

### 3.2 Order-agnostic collections — mechanics
Replace `ListAttribute`/`ListNestedAttribute` with `SetAttribute`/
`SetNestedAttribute` in: `sharing.workspaces`, `sharing.projects`,
`sharing.projects[].workspaces`, SG `rules`/`ip_rules`/`port_forward_rules`.
Converters additionally **canonicalize on FromHub** (sort keys) so fake-based
equality tests are stable. State upgrade: list→set is a schema change on
existing state — add `SchemaVersion` bump + `UpgradeState` implementations
(cheap: same element types).

### 3.3 Day-2 enforcement — mechanics
New `internal/provider/resources/immutable.go`: `immutable()` helper (String/
Bool/Int64/Set variants) applied to every immutable attribute (FR-9 list).
Default mode `ImmutableError`: a custom plan modifier that, when the resource
exists (state value known) and the planned value differs, appends
`resp.Diagnostics.AddError("<attr> cannot be modified on an existing
<kind>", "destroy and recreate explicitly, or revert the change")`. Backup
mode `ImmutableReplace`: the helper returns the stock `RequiresReplace()`
modifier instead — one-line switch (D4).
In `Update`: defensively verify only mutable fields differ (belt-and-braces vs
modifier gaps) then upsert via `Create`. `desired_action`/`action_inputs`
remain the imperative day-2 trigger (existing pattern, kept).

### 3.4 Waiters — `internal/wait`
Port the rafay pattern + paasctl `pkg/lcm` status model:
`WaitForVMReady` (create/update: pending {NOT_DEPLOYED, SUBMITTED, PENDING} →
OK `SUCCESS` / ERR `FAILED`), `WaitForVMAction` (`ACTIONCOMPLETE`/`ACTIONFAILED`),
`WaitForGone` (delete: 404 or `DESTROYED` / ERR `DESTROYFAILED`). Normalization:
`strip "Status_" + lowercase`. Ticker 10s, create/update timeout 30m, delete
15m — all overridable via the framework `timeouts` block on each resource.
Exponential backoff (2^n s, max 5) on transient errors.

**True-status guard (stale-terminal-state trap).** Status is synced back
asynchronously from the workspace controller (`FirstClassStatusSyncer`), so
immediately after submitting an operation the `/status` endpoint may still
report the *previous* operation's terminal state (e.g. an old
`ACTIONCOMPLETE`). Waiters MUST NOT accept a terminal state unless it belongs
to our operation:
1. `status.action` must match the submitted verb; AND
2. either at least one non-terminal (pending) observation preceded it, or
   `provisioned_at` is newer than our submission timestamp.
Unit tests (T1) include a "stale terminal first, then pending, then real
terminal" fake sequence to prove the guard.

**Day-2 power LCM (refinement of the scaffold's `desired_action`):** model
start/stop declaratively as `power_state = "on"|"off"` — Read populates the
actual value from status, so OOB stop/start shows as drift and `apply`
reconciles by dispatching the SDK Start/Stop verb + `WaitForVMAction`.
`reboot` is an edge (not a convergeable state) and stays outside Terraform
(use `paasctl vm reboot`); drop or deprecate `desired_action`/`action_inputs`
accordingly. Settled re-read after every waiter completion (§3.5).

### 3.5 Drift & OOB — mechanics
- Keep 404→`RemoveResource` in every `Read`.
- `FromHub` guard: only overwrite state when server value non-empty
  (rafay idiom) — and **never** for `guest_password`.
- After Create/Update wait completes, do a final `Get` → `FromHub` so state
  holds settled status/output (IPs, hostname).

### 3.6 Remote state / import
No provider changes needed beyond correctness of Read (FR-2); add import
round-trip tests. Document `terraform import 'gpupaas_virtual_machine.x'
project/workspace/name` for all resources.

### 3.7 Storage schema fix
Add `storage_type` (block|file|object) alongside `type` (profile/tier) —
mirrors devpb `storage.proto` fields 2/10; update docs + examples (the
Confluence table is stale on this).

### 3.8 Testing
- Unit (offline): converter round-trips incl. permutation fixtures; waiter
  sequences via fake; plan-modifier matrix via `resource.SchemaResponse`
  inspection; import parsers.
- Lifecycle (offline): `terraform-plugin-testing` `resource.Test` +
  `NewWithClientset(fake)` — create/read/import/update(password, SG, sharing
  reorder ⇒ empty plan)/replace(cpu change)/destroy/OOB-delete.
- Acceptance (gated `TF_ACC=1`): same suite against a real testbed.

---

## 4. GAP ANALYSIS (audited, current repo)

| Req | Status today | Work |
|---|---|---|
| SDK choice | framework (specs forbid SDK v2) | D1 decision |
| Remote state/import | DONE (flexible import IDs) | keep + tests |
| Drift | PARTIAL — `guest_password` will perpetually drift | FR-10 |
| Sharing order-agnostic | MISSING — ordered `types.List`, no normalization | FR-4 / 3.2 |
| SG rules order-agnostic | MISSING — ordered lists | FR-5 / 3.2 |
| Async delete | MISSING — returns immediately, no waiters anywhere | FR-6 / 3.4 |
| OOB delete | DONE (404→RemoveResource) | keep + tests |
| OOB change | PARTIAL (no settled-state re-read) | 3.5 |
| Day-2 restriction | MISSING — zero `RequiresReplace`; Update re-pushes all | FR-9 / 3.3 |
| Unit tests | PARTIAL — converters only; no order/wait/lifecycle tests | FR-8 |
| Build | fixed — `gpupaas-go` cloned as sibling | pin a tagged version later |

---

## 5. TASKS (ordered; [P] = parallelizable; TDD: test task precedes impl)

**Phase 0 — Decisions & foundations**
- T0 ~~Resolve D1 & D4~~ **DONE** (framework; mutable set
  {guest_password, security_group, sharing}; immutables = hard error, with
  RequiresReplace as the switchable backup).
- T1 `internal/wait` package + unit tests (fake status sequences). [P]
- T2 Extend fake clientset: scripted status transitions, delete latency, 404
  injection. [P]
- T2a `immutable.go` mode-switch helper + plan-modifier unit tests covering
  BOTH modes (error text in ImmutableError; replace flag in
  ImmutableReplace). [P]

**Phase 1 — VirtualMachine to spec**
- T3 Tests: permuted-sharing fixtures ⇒ equal state; password no-drift; plan
  matrix (cpu ⇒ replace; password/SG/sharing ⇒ update).
- T4 Sharing → sets (schema + converters + canonicalize + UpgradeState).
- T5 Apply `immutable()` to all immutable VM attrs (ImmutableError default);
  Update guard.
- T6 Wire waiters into VM Create/Update/Delete + `timeouts` block; settled
  re-read.
- T6a `power_state` attribute (on|off, declarative reconcile via Start/Stop +
  `WaitForVMAction`); deprecate `desired_action`/`action_inputs`; reboot
  documented as out-of-band (`paasctl vm reboot`).
- T7 `guest_password` write-only handling.
- T8 Lifecycle tests (fake): full FR-8 suite for VM.

**Phase 2 — SecurityGroup + Storage + SshKey**
- T9 SG rules → sets (+ tests incl. reorder-empty-plan). Day-2 allowed.
- T10 Storage: `storage_type` field; immutability matrix (all replace? confirm
  with backend); waiters. [P]
- T11 SshKey: immutability (public_key ⇒ replace); sharing sets. [P]

**Phase 3 — Docs, CI, convergence**
- T12 Update `specs/terraform-provider/requirements.md` + steering with the
  new FRs (constitution amendment); regenerate docs/; examples incl. sharing +
  import.
- T13 CI: `go vet`, `go test ./...`, lint; pin `gpupaas-go` to a tag (drop
  local replace for CI).
- T14 Converge: verify code vs this plan; append deltas.

**Deferred (D3):** Image/VmProfile resources + Vpc data source after
gpupaas-go grows those clients.

---

## 6. RISKS / OPEN QUESTIONS

1. ~~D1 overrule~~ — resolved: framework. Appendix A retained for the record
   only.
2. Does VM `Create`-as-upsert **reject** immutable-field changes server-side?
   paasctl enforces nothing client-side (pure pass-through), so the backend's
   behavior is currently unobserved. Testbed check required. Either way the
   provider's `immutable()` guard is the user-facing contract; if the backend
   silently accepts partial changes, report it as a backend bug.
3. Sharing Day-2 on the server: memberships reconcile on Apply (per backend
   docs) — verify remove-project actually revokes on testbed.
4. `DESTROYFAILED` semantics: retryable? Current plan: error out, user re-runs
   destroy.
5. list→set `UpgradeState` needs a state fixture from the current scaffold to
   test against (no released version exists yet, so risk is low).

---

## Appendix A — SDK v2 fallback design (if D1 is overruled)
Mux per terraform-provider-rafay `main.go` (`tf5to6server` + `tf6muxserver`);
new resources in a `sdkv2/` package; schemas imported from
`rafay-common/pkg/hub/terraform/resource/{virtualmachine,securitygroup,...}_schema.go`
(already generated, SDK v2, from the same protos); expand/flatten per
`rafay/resource_taggroup.go`; order-insensitivity via `schema.TypeSet` (+
custom `Set:` hash for rule objects); Day-2 via `ForceNew`; drift via
`d.SetId("")` on 404 and expand/flatten fidelity; waiters identical to §3.4.
Note this path talks to the Hub via rafay-common typed clients + rctl config —
a different auth stack than gpupaas-go; reconcile before choosing.
