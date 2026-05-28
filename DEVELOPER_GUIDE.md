# Developer Guide — Adding & Updating Resources Across `gpupaas-go` and `terraform-provider-gpupaas`

This guide explains how to add a new resource (or update an existing one) end
to end, starting from a platform/backend API change and finishing with a
released Terraform provider.

It is written for a **Cursor workspace that has both repositories checked out
as siblings**:

```
~/code/src/github.com/gpupaas-ai/
├── gpupaas-go/                   # The Go SDK (source of truth for resources)
└── terraform-provider-gpupaas/   # The Terraform/OpenTofu provider (SDK consumer)
```

> If you only have one repo open, clone the other one next to it. The provider's
> `go.mod` already contains `replace github.com/gpupaas-ai/gpupaas-go => ../gpupaas-go`,
> so a sibling checkout is required for local development.

---

## Table of contents

1. [The golden rule: SDK first, provider second](#1-the-golden-rule-sdk-first-provider-second)
2. [High-level architecture](#2-high-level-architecture)
3. [Repository structure](#3-repository-structure)
4. [The design patterns & steering docs you must follow](#4-the-design-patterns--steering-docs-you-must-follow)
5. [Workflow A — Update the SDK (`gpupaas-go`)](#5-workflow-a--update-the-sdk-gpupaas-go)
6. [Workflow B — Update the provider (`terraform-provider-gpupaas`)](#6-workflow-b--update-the-provider-terraform-provider-gpupaas)
7. [Sample Cursor prompts (copy/paste & adapt)](#7-sample-cursor-prompts-copypaste--adapt)
8. [Build, run, and test (dev / unit)](#8-build-run-and-test-dev--unit)
9. [Release builds](#9-release-builds)
10. [Cursor rules in this workspace](#10-cursor-rules-in-this-workspace)
11. [Conventions, backward compatibility & troubleshooting](#11-conventions-backward-compatibility--troubleshooting)

---

## 1. The golden rule: SDK first, provider second

When the platform backend changes (a new REST resource, a new field, a new
imperative action), the change flows in **one direction**:

```
Platform/backend API change
        │
        ▼
1. Update gpupaas-go (SDK)         ← Workflow A
        │   tag a new SDK version
        ▼
2. Update terraform-provider-gpupaas  ← Workflow B
        │   bump the SDK dependency, surface the resource
        ▼
3. Release the provider
```

The SDK is the **single source of truth** for what resources exist, their
fields, scope, and action verbs. The provider is a *thin adapter* — it never
talks HTTP directly and never invents fields that the SDK does not expose.

**Never** add a Terraform resource for an API the SDK does not yet model. Add it
to the SDK first.

---

## 2. High-level architecture

### 2.1 `gpupaas-go` — a layered, Kubernetes-style SDK

The SDK presents every resource using a Kubernetes-style envelope
(`apiVersion`, `kind`, `metadata`, `spec`, `status`) under the API group
`gpupaas.ai/v1alpha1`, and translates that to whatever wire shape the backend
actually uses (`dev.envmgmt.io/v1`, `paas.envmgmt.io/v1`, `infra.k8smgmt.io/v3`,
`auth/v1`, …).

```
   Caller (provider / CLI / automation)
            │  k8s-style gpupaas.ai/v1alpha1 objects
            ▼
   ┌─────────────────────────────────────────────┐
   │ apis/v1alpha1/   Go types + kind registration │   (no HTTP here)
   ├─────────────────────────────────────────────┤
   │ clientset/typed/v1alpha1/                     │   typed clients per kind
   │   interfaces.go / *_client.go                 │   (Create/Get/List/Delete/actions)
   ├─────────────────────────────────────────────┤
   │ convert/         wire types + ToX / FromX     │   k8s-style ⇄ backend wire
   │ convert/paths.go REST path constants          │
   ├─────────────────────────────────────────────┤
   │ rest/            HTTP, HMAC signing, errors    │
   ├─────────────────────────────────────────────┤
   │ backend/                                       │
   │   remote/  → real API via clientset           │
   │   memory/  → in-memory fake (no network)       │   used by `-memory` examples/tests
   └─────────────────────────────────────────────┘
```

Key idea: **all wire/HTTP concerns live in `convert/` and `rest/`**. The
`apis/v1alpha1/` package stays transport-free. Reads are always normalized back
to `gpupaas.ai/v1alpha1`.

### 2.2 `terraform-provider-gpupaas` — a thin Plugin-Framework adapter

```
   HCL config (gpupaas_<kind>)
            │
            ▼
   ┌─────────────────────────────────────────────┐
   │ internal/provider/                            │
   │   provider.go        register resources/DS    │
   │   resources/                                  │
   │     <kind>_resource.go        CRUD + Import   │
   │     <kind>_data_source.go     read-by-name    │
   │     <kind>_status_data_source.go (optional)   │
   │     metadata.go / sharing.go / common.go      │
   ├─────────────────────────────────────────────┤
   │ internal/client/                              │
   │   client.go   ProviderData{ clientset.Interface }
   │   fake.go     in-memory fake clientset (tests)│
   └─────────────────────────────────────────────┘
            │  calls typed clientset
            ▼
        gpupaas-go SDK  (transport, retries, auth)
```

Each resource maps schema ⇄ `apis/v1alpha1` struct, calls a typed clientset
method, and converts the response back into Terraform state. Imperative SDK
verbs (start/stop/reboot/drain/cordon/upgrade/…) are surfaced as a single
`desired_action` attribute, never as the SDK spec.

---

## 3. Repository structure

### 3.1 `gpupaas-go`

```
gpupaas-go/
├── apis/v1alpha1/                # k8s-style Go types + kind registry
│   ├── types.go                  #   shared TypeMeta/ObjectMeta + Kind* constants
│   ├── types_dev.go              #   dev.envmgmt.io resources (VM, Storage, SG, SshKey)
│   ├── types_baremetal.go        #   infra.k8smgmt.io BaremetalMachine
│   ├── types_mks.go              #   paas.envmgmt.io MKS cluster/node/wng/audit
│   └── register.go               #   scheme.AddKnownTypeWithName(...)
├── convert/                      # wire types + ToX/FromX converters
│   ├── paths.go                  #   REST path string constants
│   ├── dev_*.go                  #   per-resource converters
│   ├── paas_mks.go               #   MKS converters
│   └── *_test.go
├── clientset/
│   ├── clientset.go
│   └── typed/v1alpha1/
│       ├── interfaces.go         #   <Kind>Interface definitions
│       ├── client.go             #   Interface entrypoints (project scope)
│       ├── dev_resource_client.go
│       ├── baremetal_machine_client.go
│       ├── mks_client.go
│       └── *_test.go
├── backend/
│   ├── remote/remote.go          #   generic dispatch over the clientset
│   └── memory/memory.go          #   in-memory fake backend
├── rest/                         # HTTP client, HMAC signing, error mapping
├── client/                       # high-level generic client (Apply/Get/List/Delete)
├── apply/                        # ApplyFile (multi-doc YAML)
├── runtime/                      # Object/GVK/GVR primitives (no k8s.io deps)
├── validation/                   # scope validation
├── examples/                     # runnable example programs (one dir per verb)
├── specs/
│   ├── add-resource-from-swagger/SKILL.md   ← canonical SDK workflow
│   └── gpupaas-go/steering/                  ← design patterns (read these)
│       ├── project-overview.md
│       ├── architecture-patterns.md
│       └── coding-standards.md
├── config.go / errors.go / options.go / version.go
└── README.md                     # Appendix = canonical resource catalog
```

> `gpupaas-go` has **no Makefile** — use the raw `go` commands in
> [Section 8](#8-build-run-and-test-dev--unit).

### 3.2 `terraform-provider-gpupaas`

```
terraform-provider-gpupaas/
├── internal/
│   ├── client/
│   │   ├── client.go             # ProviderData holding clientset.Interface
│   │   ├── fake.go               # in-memory fake clientset for unit tests
│   │   └── fake_test.go
│   └── provider/
│       ├── provider.go           # GPUProvider + Resources()/DataSources()
│       └── resources/
│           ├── common.go         # import-ID parsers, error mapping, helpers
│           ├── metadata.go       # MetadataModel + schema attrs
│           ├── sharing.go        # sharing converters
│           ├── <kind>_resource.go
│           ├── <kind>_data_source.go
│           ├── <kind>_status_data_source.go
│           └── *_test.go
├── examples/                     # registry-layout HCL examples
│   ├── resources/gpupaas_<kind>/resource.tf
│   └── data-sources/gpupaas_<kind>/data-source.tf
├── docs/                         # registry-formatted docs (tfplugindocs)
├── specs/
│   ├── add-resource-from-sdk/SKILL.md       ← canonical provider workflow
│   └── terraform-provider/steering/          ← design patterns (read these)
│       ├── project-overview.md
│       ├── architecture-patterns.md
│       └── coding-standards.md
├── main.go
├── Makefile                      # build / test / testacc / fmt / vet / install-local
├── go.mod                        # replace gpupaas-go => ../gpupaas-go
└── README.md
```

---

## 4. The design patterns & steering docs you must follow

Each repo ships **steering docs** (design patterns) and a **SKILL** (the exact,
step-by-step workflow). Always read them before generating code, and tell the
Cursor agent to follow them.

| Repo | Workflow SKILL (steps) | Steering / design patterns |
|------|------------------------|-----------------------------|
| `gpupaas-go` | [`specs/add-resource-from-swagger/SKILL.md`](../gpupaas-go/specs/add-resource-from-swagger/SKILL.md) | [`specs/gpupaas-go/steering/`](../gpupaas-go/specs/gpupaas-go/steering/) |
| `terraform-provider-gpupaas` | [`specs/add-resource-from-sdk/SKILL.md`](specs/add-resource-from-sdk/SKILL.md) | [`specs/terraform-provider/steering/`](specs/terraform-provider/steering/) |

The SKILLs encode the non-negotiable invariants:

- **SDK envelope** — every resource is `apiVersion/kind/metadata/spec/status`.
- **Status is observed-only** — strip it before every write.
- **Backward compatibility** — never silently remove/rename a field, method,
  kind, attribute, scope, or import-ID format. When the backend drops a field,
  *ask first* and default to keeping it marked `// Deprecated:`.
- **Imperative actions** — modelled as `desired_action` in Terraform, never as
  SDK spec.
- **No transport in the provider** — all HTTP/retry/auth lives in the SDK.

Cursor rules in `.cursor/rules/` of each repo reference these automatically (see
[Section 10](#10-cursor-rules-in-this-workspace)).

---

## 5. Workflow A — Update the SDK (`gpupaas-go`)

**Trigger:** the backend exposes a new resource/field/action, and you have its
Swagger/OpenAPI spec (or a description of the REST endpoints).

Follow [`gpupaas-go/specs/add-resource-from-swagger/SKILL.md`](../gpupaas-go/specs/add-resource-from-swagger/SKILL.md).
The SKILL's checklist, condensed:

| # | Step | File(s) |
|---|------|---------|
| 1 | Parse swagger → resource plan (kind, scope, paths, spec/status, sub-actions, idempotency) | — |
| 2 | Detect existing kind & diff (add / modify / **ask-before-remove**) | `rg "Kind<Name>"` |
| 3 | Add/update k8s-style types + `DeepCopyObject` | `apis/v1alpha1/types_*.go`, kind constant in `types.go` |
| 4 | Register the kind | `apis/v1alpha1/register.go` |
| 5 | Add REST path constants | `convert/paths.go` |
| 6 | Add wire types + `ToX`/`FromX` converters | `convert/<group>_<kind>.go` |
| 7 | Add `<Kind>Interface` | `clientset/typed/v1alpha1/interfaces.go` |
| 8 | Implement the typed client + wire entrypoints | `clientset/typed/v1alpha1/*_client.go`, `client.go` |
| 9 | Extend generic backend dispatch | `backend/remote/remote.go` |
| 10 | Tests: converter round-trip + clientset `httptest` | `convert/*_test.go`, `clientset/typed/v1alpha1/*_client_test.go` |
| 11 | Add runnable `examples/` programs (use `-memory` where possible) | `examples/<verb>-<kind>/main.go` |
| 12 | Update `README.md` (backend table + Appendix entry) | `README.md` |
| 13 | **Verify gate** | see below |

### 5.1 Scope decisions (the part that bites)

The validation layer (`validation/validation.go`) decides whether a kind
requires a workspace. When adding a **project-scoped** resource (no workspace),
add the kind to the project-scoped `switch` case there — otherwise the in-memory
backend wrongly demands `metadata.workspace`. (This is exactly what tripped up
`BaremetalMachine` and `MKSCluster`.)

| Scope | Accessor | Examples |
|-------|----------|----------|
| Cluster (`metadata.name` only) | `cs.V1alpha1().<Kind>s()` | `Project` |
| Project (`metadata.project` + name) | `cs.V1alpha1().<Kind>s(project)` | `Workspace`, `BaremetalMachine`, `MKSCluster` |
| Project-or-Workspace (dev) | `…Workspaces(project).<Kind>s(workspace)` or `…<Kind>s(project)` | `VirtualMachine`, `Storage`, `SecurityGroup`, `SshKey` |
| Cluster sub-resource | `…MKSClusters(project).Nodes(cluster)` | `MKSNode`, `MKSWorkerNodeGroup`, `MKSAuditEvent` |

Only **top-level** kinds are wired into the generic remote backend
(`backend/remote/remote.go`). Sub-resources that need a parent name (e.g. MKS
nodes need a cluster) are reached only through the typed clientset.

### 5.2 Verify gate (must all pass before you stop)

```bash
cd gpupaas-go
gofmt -l .          # must print nothing
go vet ./...
go build ./...
go test ./...
```

### 5.3 Tag a new SDK version

Once merged to `main`, cut a semver tag so the provider can depend on it:

```bash
cd gpupaas-go
git tag v0.2.0           # bump per semver: additive = minor, fix = patch
git push origin v0.2.0
```

> `gpupaas.ai/v1alpha1` is pre-1.0, so minor bumps may carry small breaking
> changes — call them out in the tag/release notes.

---

## 6. Workflow B — Update the provider (`terraform-provider-gpupaas`)

**Trigger:** the SDK now exposes a kind/field/action the provider doesn't.

Follow [`specs/add-resource-from-sdk/SKILL.md`](specs/add-resource-from-sdk/SKILL.md).
Condensed checklist:

| # | Step | File(s) |
|---|------|---------|
| 1 | Diff the SDK README appendix + typed interfaces vs the provider | `../gpupaas-go/README.md`, `../gpupaas-go/clientset/typed/v1alpha1/interfaces.go` |
| 2 | Bump the `gpupaas-go` dependency (if needed) | `go.mod` |
| 3 | Design the resource model (mirror the SDK envelope) | `resources/<kind>_resource.go` |
| 4 | Implement Resource (Create/Read/Update/Delete/Import) | same |
| 5 | Add `desired_action` (+ `action_inputs`) for imperative verbs | same |
| 6 | Surface `status.*` as `Computed`; add a status data source if the SDK has a live `GetStatus` | `resources/<kind>_status_data_source.go` |
| 7 | Add the read/lookup data source | `resources/<kind>_data_source.go` |
| 8 | Register in the provider | `internal/provider/provider.go` |
| 9 | Extend the fake clientset (must satisfy the full interface) | `internal/client/fake.go` |
| 10 | Tests: converter round-trip + import parser + action diff | `resources/*_test.go` |
| 11 | Examples + docs (registry layout) | `examples/…`, `docs/…` |
| 12 | **Verify gate** | see below |

### 6.1 Dependency wiring (local vs released)

The provider's `go.mod` pins `replace github.com/gpupaas-ai/gpupaas-go => ../gpupaas-go`.

- **Local development:** the `replace` makes the provider compile against your
  working tree of the SDK — no tag needed. Keep it while iterating.
- **Before release:** point at a real tag and (optionally) drop the replace:

```bash
cd terraform-provider-gpupaas
go get github.com/gpupaas-ai/gpupaas-go@v0.2.0
go mod tidy
```

> Do not commit a `replace => ../gpupaas-go` in a release PR. Split "bump the
> SDK" commits from "remove attributes" commits.

### 6.2 Naming & scope mapping

| SDK Kind | Terraform type | Import ID |
|----------|----------------|-----------|
| `Project` | `gpupaas_project` | `<name>` |
| `Workspace` | `gpupaas_workspace` | `<project>/<name>` |
| `VirtualMachine` | `gpupaas_virtual_machine` | `<project>/<name>` or `<project>/<workspace>/<name>` |
| `BaremetalMachine` | `gpupaas_baremetal_machine` | `<project>/<name>` |
| `MKSCluster` | `gpupaas_mks_cluster` | `<project>/<name>` |
| *(new)* | `gpupaas_<snake_case>` | match the SDK scope |

Status data sources use the `_status` suffix and only exist for kinds with a
live `GetStatus` sub-route.

### 6.3 Imperative actions → `desired_action`

Any SDK verb that mutates lifecycle without changing the desired spec
(`Start`, `Stop`, `Reboot`, `Upgrade`, `ScaleNodeGroup`, `Drain`, `Cordon`,
`Uncordon`, …) is surfaced as a single `desired_action` string with an `OneOf`
validator sourced from the typed interface, plus an optional `action_inputs`
nested block for payloads. The `Update` method diffs old vs. new
`desired_action` and dispatches the matching SDK call. See the canonical pattern
in the SKILL and in `resources/virtual_machine_resource.go`.

### 6.4 Verify gate (must all pass before you stop)

```bash
cd terraform-provider-gpupaas
go fmt ./...
go vet ./...
go test ./...
go build ./...
```

Then regenerate docs and confirm a clean diff:

```bash
tfplugindocs generate
git diff docs/      # only intended new files
```

---

## 7. Sample Cursor prompts (copy/paste & adapt)

These are improved versions of the prompts used to add the Baremetal and MKS
resources. Open the relevant repo (or the workspace root) and paste.

### 7.1 Prompt for the SDK (`gpupaas-go`)

> Use `@gpupaas-go/specs/add-resource-from-swagger/SKILL.md` and the design
> patterns in `@gpupaas-go/specs/gpupaas-go/steering/`.
>
> Add/update the SDK for the **`<Kind>`** resource from the Swagger spec below.
>
> Constraints:
> - Scope: **`<project | workspace | project-or-workspace | cluster-subresource>`**.
>   Add the kind to the project-scoped case in `validation/validation.go` if it
>   has no workspace.
> - Skip these definitions/operations: `<anything to skip, e.g. MKSProfile>`.
> - Imperative sub-actions to expose as interface methods:
>   `<e.g. Upgrade, ScaleNodeGroup, Drain, Cordon, Uncordon>`.
> - Idempotency: route `Update` through `Create` unless the swagger has a real
>   `PUT`.
> - Preserve backward compatibility — do not remove/rename any existing field,
>   method, or kind without asking me first.
>
> Do the full pipeline: `apis/v1alpha1` types (+ deepcopy + kind constant +
> register) → `convert/` wire types, converters, and path constants →
> `clientset/typed/v1alpha1` interface + client (+ wire it into the root
> `Interface`/`Client`) → `backend/remote` dispatch for top-level kinds →
> convert + clientset `httptest` tests → runnable `examples/` programs using the
> `-memory` flag where possible → README backend table row + Appendix section.
>
> Finish with the verify gate: `gofmt -l .`, `go vet ./...`, `go build ./...`,
> `go test ./...` — all clean.
>
> ```yaml
> <paste the OpenAPI/Swagger here>
> ```

### 7.2 Prompt for the provider (`terraform-provider-gpupaas`)

> Use `@terraform-provider-gpupaas/specs/add-resource-from-sdk/SKILL.md` and the
> design patterns in `@terraform-provider-gpupaas/specs/terraform-provider/steering/`.
>
> The SDK (`../gpupaas-go`) now exposes **`<Kind>`**. Add the matching Terraform
> resource `gpupaas_<snake_case>` and its data source(s).
>
> Use the local sibling SDK as the source of truth:
> `@gpupaas-go/README.md` (Appendix) and
> `@gpupaas-go/clientset/typed/v1alpha1/interfaces.go`.
>
> Constraints:
> - Scope / import ID: `<project/name | project/workspace/name | name>`.
> - Imperative verbs → a single `desired_action` attribute with an `OneOf`
>   validator: `<none, upgrade, scale_node_group, drain, cordon, uncordon, …>`,
>   plus `action_inputs` for payloads. Implement the diff-on-Update dispatch.
> - Surface every `apiv1.<Kind>Status` field as `Computed`. Add a `_status` data
>   source only if the SDK has a live `GetStatus`.
> - Keep the provider thin — no HTTP/retry logic; call the typed clientset only.
> - Backward compatibility: never remove/rename an attribute or change an import
>   ID format without asking me first.
>
> Wire it through: resource (CRUD + Import) → data source(s) → register in
> `internal/provider/provider.go` → extend `internal/client/fake.go` to satisfy
> the full interface → tests (converter round-trip, import-ID parser, action
> diff with the fake) → `examples/resources/gpupaas_<kind>/` and
> `examples/data-sources/…` → `docs/` via `tfplugindocs`.
>
> Finish with: `go fmt ./...`, `go vet ./...`, `go test ./...`, `go build ./...`,
> then `tfplugindocs generate` and confirm a clean `git diff docs/`.

### 7.3 Tips for good agent runs

- Paste the **full** Swagger/OpenAPI block inline — it's treated as
  authoritative input.
- Be explicit about **scope** and anything to **skip** (e.g. "Skip MKSProfile").
- Name the **imperative verbs** up front so they land as interface methods /
  `desired_action` values rather than CRUD.
- Tell the agent to **stop and ask** before any field/method removal.
- Ask it to **run the verify gate** and report results — don't accept "done"
  without green `build`/`vet`/`test`.

---

## 8. Build, run, and test (dev / unit)

### 8.1 SDK — `gpupaas-go` (no Makefile; raw `go`)

```bash
cd gpupaas-go

# Format / static analysis / build / unit tests
gofmt -l .            # prints files needing formatting (should be empty)
go vet ./...
go build ./...
go test ./...

# Focused tests while iterating
go test ./convert/... ./clientset/... ./backend/...

# Run an example against the in-memory backend (no creds, no network)
go run ./examples/create-mks-cluster -memory -project demo -name my-cluster
go run ./examples/list-mks-clusters  -memory -project demo

# Run an example against the live API (sub-actions need the real backend)
export GPUPAAS_API_KEY=...        # X-API-KEY (HMAC also uses GPUPAAS_API_SECRET)
export GPUPAAS_ENDPOINT=https://console.gpupaas.ai
go run ./examples/mks-cluster-upgrade -project demo -name my-cluster -k8s-version 1.32 -v
```

The `-memory` flag uses `backend/memory` so most CRUD examples run with no
credentials. Imperative sub-actions require the live API.

### 8.2 Provider — `terraform-provider-gpupaas` (Makefile)

```bash
cd terraform-provider-gpupaas

make fmt              # go fmt ./...
make vet              # go vet ./...
make test             # go test ./...  (in-memory fake clientset; no API needed)
make build            # -> ./bin/terraform-provider-gpupaas

# Acceptance tests against the real API (opt-in)
export TF_ACC=1
export GPUPAAS_ENDPOINT="https://console.gpupaas.ai"
export GPUPAAS_API_KEY="..."
make testacc          # TF_ACC=1 go test ./... -v -count=1
```

### 8.3 Try the provider locally with `dev_overrides`

Build and install the binary into your local plugin dir, then point Terraform at
it via `~/.terraformrc`:

```bash
cd terraform-provider-gpupaas
make install-local    # copies bin into ~/.terraform.d/plugins/gpupaas-ai/gpupaas/<ver>/<os>_<arch>/
```

`~/.terraformrc`:

```hcl
provider_installation {
  dev_overrides {
    "gpupaas-ai/gpupaas" = "/absolute/path/to/terraform-provider-gpupaas/bin"
  }
  direct {}
}
```

Then in a scratch dir:

```bash
export GPUPAAS_API_KEY=...
terraform plan        # no `terraform init` needed with dev_overrides
terraform apply
```

> With `dev_overrides`, do **not** run `terraform init` — Terraform prints a
> warning and uses the override binary directly.

### 8.4 The local SDK ⇄ provider loop

Because of the `replace => ../gpupaas-go` directive, edits in your local SDK tree
are picked up immediately by the provider build:

```bash
# 1. change gpupaas-go, verify it
cd gpupaas-go && go build ./... && go test ./...

# 2. rebuild the provider against the local SDK
cd ../terraform-provider-gpupaas && go build ./... && make test

# 3. if you added SDK symbols, refresh the module graph
go mod tidy
```

---

## 9. Release builds

### 9.1 Release the SDK (`gpupaas-go`)

The SDK is a plain Go module — "release" = a pushed semver tag.

```bash
cd gpupaas-go
gofmt -l . && go vet ./... && go build ./... && go test ./...   # green gate
git tag v0.2.0
git push origin v0.2.0
```

Then any consumer (the provider, CLIs, automation) upgrades with
`go get github.com/gpupaas-ai/gpupaas-go@v0.2.0`. Document breaking changes in
the tag/release notes while the API is `v1alpha1`.

### 9.2 Release the provider (`terraform-provider-gpupaas`)

A Terraform provider release produces signed, multi-platform binaries that the
Terraform Registry can serve. Steps:

1. **Pin a released SDK** (no local `replace`):

   ```bash
   go get github.com/gpupaas-ai/gpupaas-go@v0.2.0
   go mod tidy
   ```

2. **Green gate + docs:**

   ```bash
   go fmt ./... && go vet ./... && go test ./... && go build ./...
   tfplugindocs generate && git diff --exit-code docs/
   ```

3. **Tag** with a `v`-prefixed semver:

   ```bash
   git tag v0.2.0
   git push origin v0.2.0
   ```

4. **Build signed cross-platform archives.** The Registry expects GoReleaser
   output (per-OS/arch zips, `SHA256SUMS`, a GPG `.sig`, and a
   `terraform-registry-manifest.json`). The recommended setup:

   - Add a `.goreleaser.yml` and `terraform-registry-manifest.json`
     (HashiCorp publishes a standard template for providers).
   - Export a GPG signing key (`GPG_FINGERPRINT`) and run GoReleaser, typically
     from a tag-triggered GitHub Actions workflow:

     ```bash
     export GPG_FINGERPRINT=...
     goreleaser release --clean
     ```

   - Upload the artifacts to the GitHub Release and publish/refresh the version
     in the Terraform Registry (the registry pulls from the signed GitHub
     Release).

> Current state: the repo's `Makefile` covers local `build` and `install-local`
> only — release automation (`.goreleaser.yml` + CI) is the recommended next
> step and is not yet committed. Until it exists, cut releases by adding the
> GoReleaser config above, or distribute the `make build` binary via
> `dev_overrides` for internal testing.

---

## 10. Cursor rules in this workspace

Both repos contain Cursor rules under `.cursor/rules/` that auto-attach the
right SKILL + steering docs when you edit the relevant source, and remind the
agent of the SDK-first ordering and backward-compatibility invariants:

| Repo | Rule | What it does |
|------|------|--------------|
| `gpupaas-go` | `.cursor/rules/sdk-resource-workflow.mdc` | When editing `apis/`, `convert/`, `clientset/`, `backend/`, follow the swagger SKILL + steering; enforce the envelope, status-read-only, scope/validation, and backward-compat rules. |
| `terraform-provider-gpupaas` | `.cursor/rules/provider-resource-workflow.mdc` | When editing `internal/`, follow the SDK-consumer SKILL + steering; enforce SDK-first ordering, `desired_action` for verbs, computed status, fake-clientset upkeep, and backward-compat rules. |

You can also invoke a rule explicitly in chat (e.g. "follow the
sdk-resource-workflow rule"). The rules intentionally point at the SKILLs and
steering docs rather than duplicating them, so the workflow stays in one place.

---

## 11. Conventions, backward compatibility & troubleshooting

### 11.1 Cross-cutting conventions

- **Envelope everywhere:** `apiVersion` (`gpupaas.ai/v1alpha1`), `kind`,
  `metadata`, `spec`, `status`.
- **JSON casing:** SDK `apis/` types use camelCase JSON tags. Backend wire
  casing (often snake_case) lives only in `convert/`.
- **Status is read-only:** strip it before every write, in both the SDK client
  and the provider converters.
- **Context first:** every SDK/provider method takes `ctx context.Context`
  first.
- **No secrets in logs.** Mark sensitive Terraform attributes `Sensitive: true`.

### 11.2 Backward-compatibility hard rules (both repos)

1. Never silently remove a field/method/kind/attribute/data-source. Ask first;
   default to keeping it marked `// Deprecated:` / `DeprecationMessage:`.
2. Never rename a public Go identifier or a Terraform attribute — add the new
   name and keep the old as an alias.
3. Never change JSON tag names or a Terraform import-ID format.
4. Never change a resource's scope (cluster ↔ project ↔ workspace) without
   explicit approval — it breaks every caller and every import.
5. Never auto-trigger an imperative action on refresh — only on an explicit
   `desired_action` plan diff.

### 11.3 Troubleshooting

| Symptom | Likely cause / fix |
|---------|--------------------|
| SDK error `metadata.workspace is required` for a project-scoped kind | Add the kind to the project-scoped `switch` case in `gpupaas-go/validation/validation.go`. |
| Provider build fails: `*fake… does not implement … Interface (missing method …)` | The SDK interface grew; mirror the new method(s) in `internal/client/fake.go`. |
| Provider can't find new SDK symbols | Run `go mod tidy` in the provider; confirm the `replace => ../gpupaas-go` (local) or the `go get …@tag` (released) is in place. |
| `terraform init` warns/ignored with dev_overrides | Expected — skip `init`; run `plan`/`apply` directly. |
| `git diff docs/` not empty after a change | Run `tfplugindocs generate` and commit the regenerated docs. |
| MKS/Baremetal sub-action returns "unsupported" on `-memory` | Sub-actions need the live API; drop `-memory` and set `GPUPAAS_API_KEY`. |
| Wire payload looks wrong | Add `-v` (or `GPUPAAS_VERBOSE=1`) to dump HTTP traffic; the bug is almost always in `convert/`. |

### 11.4 Quick reference — full round trip for one new resource

```bash
# 1. SDK
cd gpupaas-go
#    (run Workflow A via the swagger SKILL)
gofmt -l . && go vet ./... && go build ./... && go test ./...
git tag v0.2.0 && git push origin v0.2.0

# 2. Provider
cd ../terraform-provider-gpupaas
go get github.com/gpupaas-ai/gpupaas-go@v0.2.0   # or keep the local replace while iterating
#    (run Workflow B via the SDK-consumer SKILL)
go fmt ./... && go vet ./... && go test ./... && go build ./...
tfplugindocs generate && git diff docs/

# 3. Release the provider (GoReleaser + signed tag) — Section 9.2
```
