# Contributing

One file. Terse on purpose. Read top to bottom, about 10 minutes. For people and AI agents. Internal: no credentials here, ever.

Provider = thin layer: Terraform <-> GPU PaaS platform. **No HTTP here.** HTTP lives in the Go SDK `gpupaas-go`; check it out
**next to this repo** (`../gpupaas-go`). Other docs: `README.md` (users), `RELEASING.md` (releases, partner builds), `AGENTS.md` (agent pointer).

---

## 1. Rules

1. **SDK first.** SDK lacks a resource, field or action: add it in `../gpupaas-go`, then come back.
2. **Go types are truth.** Read `../gpupaas-go/apis/v1alpha1/` and `.../clientset/typed/v1alpha1/interfaces.go`, not prose.
3. **Copy a real resource.** Small: `internal/provider/resources/workspace_resource.go`. Full: `vm_resource.go` + `vm_convert.go` + `vm_validate.go`.
4. **Decode with helpers** (`decodePlan`, `decodeState`, `decodeConfig`). In CRUD never load the whole model with `req.Plan.Get` /
   `req.State.Get`. (`ModifyPlan` may read single attributes with `req.Plan.GetAttribute` to see unknowns; that is the pattern.)
5. **State after apply = plan.** Else Terraform fails: `Provider produced inconsistent result after apply`.
6. **Platform does not store a value? Keep the configured one.** Else every plan shows false change.
7. **Show the platform's real error.** Failed create says why, not just "failed".
8. **Write official names** (`gpupaas_*`, `GPUPAAS_*`) in docs, examples, errors. Partner builds rewrite them.
9. **Every fix gets a test that fails without the fix.**
10. **Done = section 8 checks pass.** Compiling is not done.

---

## 2. Map

| Path | What |
|---|---|
| `internal/provider/provider.go` | Provider settings. **List of resources + data sources: register new ones here.** |
| `internal/provider/resources/<kind>_resource.go` | One resource: schema, CRUD, import |
| `.../<kind>_convert.go` | Terraform model <-> SDK object (bigger kinds) |
| `.../<kind>_validate.go` | Plan-time checks vs SKU (`ModifyPlan`) |
| `.../<kind>_data_source.go`, `list_data_sources.go` | Lookup by name; all list data sources (one table) |
| `.../common.go` | Error mapping, import-ID parsers, decode helpers |
| `.../hidden_instance.go` | Backing compute instance of VM / bare metal / cluster |
| `internal/client/` | `ProviderData` (the clientset) + a small fake clientset (`fake.go`, `fake_compute.go`) used by a few unit tests |
| `internal/provider/*_acc_test.go` | Acceptance tests |
| `examples/`, `docs/` | Examples (you write) / docs (generated from schema; do not hand-edit) |
| `internal/brand`, `internal/brandctl`, `brands.yml` | Partner (white-label) builds |

## 3. Find things

| Need | Look |
|---|---|
| Fields, scope, status of a kind | `../gpupaas-go/apis/v1alpha1/types_*.go` |
| API paths | `../gpupaas-go/convert/paths.go` |
| What client can do | `../gpupaas-go/clientset/typed/v1alpha1/interfaces.go`, `*_wait.go` |
| How API really behaves | SDK mocks `../gpupaas-go/mock/`, then a live test env |
| What a SKU allows | profile kinds via SDK (see `vm_validate.go`) |
| Platform templates, handlers | repos `catalog-templates`, `catalog-workflow-handlers` |

Sources disagree: Go code beats prose. Live run beats mock. Teach the mock what live taught you.

---

## 4. Set up and run locally

```bash
# repos side by side:   .../gpupaas-go   .../terraform-provider-gpupaas
cd terraform-provider-gpupaas
make build                      # -> bin/terraform-provider-gpupaas
```
`go.mod` has `replace github.com/gpupaas-ai/gpupaas-go => ../gpupaas-go`. Keep while working. Never commit it in a release.

Try it: `~/.terraformrc` (or `.tofurc`):
```hcl
provider_installation {
  dev_overrides { "gpupaas-ai/gpupaas" = "/abs/path/to/terraform-provider-gpupaas/bin" }
  direct {}
}
```
Skip `init`; run `plan` / `apply` direct. Credentials from env: `GPUPAAS_ENDPOINT`, `GPUPAAS_API_KEY`,
`GPUPAAS_ORGANIZATION_ID` (partner key only), `GPUPAAS_INSECURE=true` (self-signed lab only).

**SDK loop.** Change SDK, run `go build ./... && go test ./...` there, rebuild provider here. New SDK symbol not found: `go mod tidy`.
SDK interface grew (new method or kind)? Mirror it in `internal/client/fake.go` / `fake_compute.go`, or the provider build and unit tests break.

**Change the SDK** (its own guide: `../gpupaas-go/CONTRIBUTING.md`):
types `apis/v1alpha1` -> wire mapping `convert/` + paths -> client + interface in `clientset/typed/v1alpha1` -> wait helper -> `validation/` -> mock in `mock/` -> tests.
SDK gate: `gofmt -l . && go vet ./... && go build ./... && go test ./...`.

---

## 5. Add a resource

Copy a neighbour at every step.

1. **Names, scope.** Type `gpupaas_<snake_case>`. Scope project or workspace, as SDK. Import ID `<project>/<name>` or
   `<project>/<workspace>/<name>`. Never change scope or ID format of an existing resource.
2. **SDK.** Done first (section 4).
3. **Schema + model.** Struct with `tfsdk` tags. `name`, `project`, `workspace`: `RequiresReplace`. Computed fields:
   `UseStateForUnknown`. Secrets: `Sensitive: true`. Description on every attribute: it becomes the docs.
4. **Converters.** `<kind>ToSDK` builds SDK object. `<kind>FromServer(base, srv, fromRead)` builds state from what user
   configured (`base`) and what platform returned.
5. **CRUD.** Follow `workspace_resource.go`. `r.pd == nil` check first. Errors via `handleAPIError`. `Read`: not found =
   remove from state. `Create`: save state right after object exists, before waiting, so a failed wait leaves something `destroy` can remove.
6. **Slow resources.** Wait with SDK `typed.WaitFor*`; need several stable polls in a row (VM uses 2: platform briefly shows
   old state). Add `timeouts` attribute. Delete also waits for hidden compute instance (`waitForHiddenInstanceGone`).
   Failure adds platform error (`addVMFailure`). Poll with `pollInterval`, never bare `time.Sleep`.
7. **Plan-time checks.** `ModifyPlan` (`vm_validate.go`): wrong image / size / datacenter fails in seconds, not minutes.
   Error on create or changed value. Unchanged old value that no longer fits: warn only.
8. **Actions** (start, stop, reboot): one string `desired_action`, never in spec. Runs only when value changes in a plan, never
   on refresh. Fails: keep old value in state so next apply retries.
9. **Import.** `ImportState` parses ID, sets `metadata.*` + `id`; `Read` fills rest. Add `examples/resources/gpupaas_<kind>/import.sh`.
10. **Data sources.** By name: copy `workspace_data_source.go`. List: add a line to table in `list_data_sources.go`.
    A list for that kind already exists? Extend its fields (additive only); do not add a second list.
11. **Register** in `Resources()` / `DataSources()` in `provider.go`.
12. **Examples + docs.** `examples/resources/gpupaas_<kind>/resource.tf` (+ `import.sh`); data sources: `examples/data-sources/gpupaas_<name>/data-source.tf`.
    Must really work. Then `make generate-docs`.
13. **Tests, checks** (section 8).

**Change existing resource.** Never remove or rename attribute, resource, import-ID format. Ask first; default keep with
`DeprecationMessage`. New attribute: `Optional` (or `Optional`+`Computed` if platform has default). Cannot change in place:
`RequiresReplace`, say so in description.

---

## 6. Rules that prevent real bugs

### 6.1 Unknown values, decoding
Terraform may not know a value until apply (`ssh_key = other.thing.output`). Our models cannot hold "unknown". Use helpers (`resources/common.go`):

| Helper | Use |
|---|---|
| `decodePlan` | Create, Update. Unknown -> empty. Null block stays null; explicit `{}` stays empty. |
| `decodePlanKeepingUnknowns` | Checks that must skip not-yet-known values |
| `decodeConfig` | Raw config inside `ModifyPlan` |
| `decodeState` | Read, Delete. Null block -> empty, except `timeouts` (stays null). |
| `planCredentialPending`, `planListPending` | "Could this still become known?" |

- Credential unknown at plan: never "No way to log in". Check `planCredentialPending` before rejecting.
- Size / preset unknown: leave **unknown** in what you validate. Null = "not set", wrongly rejects GPU SKUs.
- Whole `spec`, `timeouts`, `sharing` built from another resource must plan clean.

### 6.2 State = plan
- **Empty is not null.** `worker_pools = []`, `sharing = { workspaces = [] }` store as empty list, though platform drops them.
  See `sharingFromSDK(ctx, srv, configured, diags)`: return configured form when platform holds same data.
- **Order.** Same items, other order from platform: keep configured order.
- **Optional blocks.** No `timeouts` stays null. `timeouts = {}` stays empty. Do not repair per resource.
- **Removing a block** platform would otherwise keep (`sharing` on a volume): send explicit "none".

### 6.3 Fields platform does not store
API accepts value, never returns it (VM `description`, passwords): keep configured value when platform says nothing
(`vmModelFromServer`, `keepOrDrift`, `keptPassword`). Drift only when it returns something different. Absent != changed.

### 6.4 Errors
- `handleAPIError`; never read status codes yourself.
- Show platform cause. `truncate` cuts by character, not byte.
- Normal teardown quirks: first delete of a failed object may say "Please try again" (retry works); delete may park as
  `delete_pending` for an admin. Explain, keep object in state.
- Never put secret, key, kubeconfig in error or log.

### 6.5 Envelope, status, actions
- A resource has `metadata` / `spec` / `status`. SDK `apiVersion` and `kind` are set in `<kind>ToSDK`; they are not attributes.
- `status` is computed and read-only: never send it. Secret values in it (`status.output`: passwords, S3 keys) are `Sensitive`.
- Separate status data sources only where the SDK has a live status read the resource lacks. Today only the cluster
  (`gpupaas_kubernetes_cluster_status`, `_audit_events`, `_kubeconfig`). No `_status` suffix for other kinds.
- `desired_action` carries no payload today; there is no `action_inputs`. Add one only for a verb that needs inputs.

### 6.6 Rest
- No HTTP, retries, auth in provider. SDK owns them.
- Never remove / rename public things. Never change scope.
- Actions only via `desired_action`. Never on refresh.
- `snake_case` names. `ctx context.Context` first. Comments say *why*.

---

## 7. Partner (white-label) builds
One source, many brands (`brands.yml`). Partner build swaps `gpupaas_` for its own name.
- Write `gpupaas_*` / `GPUPAAS_*` in descriptions and errors: wrapper rewrites at runtime.
- Same in `examples/`: `brandctl release` rewrites per brand, **fails** if an official name is left.
- Do not build resource names by joining strings. Do not put official registry address where a partner sees it.
- Known gap: partner docs still show default endpoint `https://console.gpupaas.ai`.
- Release steps: `RELEASING.md`.

---

## 8. Test and check

Three layers:
1. **Unit** (`internal/provider/resources/*_test.go`): converters, import parsers, helpers.
2. **Acceptance** (`internal/provider/*_acc_test.go`): real `terraform`/`tofu` binary vs **mock platform API** from SDK
   (`mock/vmapi`, `bmapi`, `devapi`, `clusterapi`). No credentials. **Run only with `TF_ACC=1`.** Plain `go test ./...`
   silently skips them: green proves little.
3. **Live run** on a test env: timings, real errors, host capacity. Own names (`tf-<you>-...`). Touch nothing you did not create. Clean up.

Small acceptance test (helpers in `acc_helpers_test.go`, `*_acc_test.go`):
```go
func TestAccVM_example(t *testing.T) {
    srv, _ := newVMMock(t, vmapi.Options{})
    cfg := vmConfig(srv.URL, "", "")
    resource.Test(t, resource.TestCase{
        ProtoV6ProviderFactories: protoFactories(),
        CheckDestroy:             checkVMGone(srv),
        Steps: []resource.TestStep{
            {Config: cfg, Check: resource.TestCheckResourceAttr(vmAddr, "status.phase", "running")},
            {Config: cfg, PlanOnly: true}, // second plan must be empty
        },
    })
}
```

**Fix needs a test that fails without it.** Write test, stash only the source change (`git stash push -- <source files>`),
run, see fail, `git stash pop`. Passes both ways = proves nothing.

| Bug | Test |
|---|---|
| Value known only after apply | feed from `terraform_data.x.output`; apply; plan again |
| Whole block unknown at plan | `spec = terraform_data.s.output` (`input` is an object) |
| Null vs empty | `timeouts = {}`, `worker_pools = []`, `workspaces = []`; apply; `PlanOnly` step |
| Order | write list, then same items reordered; apply; next plan empty |
| Absent optional block | omit; `TestCheckNoResourceAttr` on `block.%` (explicit `{}` adds `block.%`) |
| Field platform drops | mock option that stores nothing (`DropMetadataText`); clean second plan |
| Write to platform | check mock record (`srv.Storage(...)`), not only Terraform state |

`TestCheckResourceAttr(addr, "x.#", "0")` also passes for null list: pair with `TestCheckResourceAttrSet`.
Mock lacks behaviour you need: add it to SDK mock as an option, with its own test.

**Checks** (run here, SDK beside it). Docs steps need `tfplugindocs`
(`go install github.com/hashicorp/terraform-plugin-docs/cmd/tfplugindocs@v0.20.1`); not on `PATH`: `TFPLUGINDOCS=/path make ...`.
```bash
gofmt -l .                       # prints nothing
go vet ./...
go build ./...
TF_ACC=1 go test ./... -count=1  # all green (terraform on PATH; only tofu: also set TF_ACC_TERRAFORM_PATH=$(command -v tofu); `make testacc` sets both)
make docs-check                  # regenerates docs/, fails if docs/ changed or has new files (looks at docs/ only)
go run ./cmd/brandctl release -allow-local -brands acme -version 0.0.0-test -platforms darwin/arm64 -tfplugindocs tfplugindocs
                                 # must say "all documented", no official names left
```

Before handover:
- [ ] Every attribute described; secrets `Sensitive`; replace-vs-in-place stated
- [ ] Plan after apply empty; import round trip clean
- [ ] Unknown-after-apply and empty-vs-null tested where resource has them
- [ ] Every fix has a failing-without-fix test
- [ ] Examples run; docs regenerated; brand build passes
- [ ] No secret in code, logs, docs, commits
- [ ] No local `replace` in a release commit.

---

## 9. AI agents

**Read first:** this file, rule 3 files, SDK types of the resource you change.

**Do**
- Find similar resource, copy structure + names. Small change, one concern.
- Run section 8 checks, report real output. Say what you did **not** verify (no live run, no `TF_ACC`).
- Verify what you write: run the command or read the code. Never guess an attribute name.

**Ask a human before**
- removing / renaming anything public; changing scope, type, import-ID format
- changing behaviour proven live (timings, waits, teardown) without another live run
- creating, changing, deleting anything on a shared test env, or starting its machines
- commit, push, force-push, PR (unless asked)

**Never**
- credential, key, kubeconfig in code, committed file, log, message
- skip failing check, weaken test to pass, work around a permission or sandbox denial
- add HTTP / retry / auth to the provider
- add AI attribution lines (`Co-Authored-By`, "Generated with ...") to commits, PR text or comments (team rule)

**Traps**
- `go test ./...` without `TF_ACC=1` skips acceptance tests.
- Parallel commands share a working directory: `git -C <repo>`; run repo-changing commands one at a time.
- `docs/` generated: change schema or examples, regenerate.

---

## 10. Quirks you will meet

- **Hidden compute instance.** VM / bare metal / cluster each have a backing instance that outlives the record. Workspace
  cannot delete until it is gone, so delete waits for it.
- **Stale `failed`.** After a failed run instance may read `failed` while next teardown starts. Tolerate a few polls; report
  failed `destroy` at once.
- **First delete may fail once** ("Please try again"). `delete_pending` needs an admin.
- **502 can mean not found.** Use SDK `IsNotFound`; never status codes.
- **Capacity.** Lab host may hold one VM. Second ends `No Capacity for VM allocation`: environment limit, not a bug.
- **Actions stay in state.** After `desired_action = "reboot"`, plan without it shows `"reboot" -> null`: harmless update, sends nothing.
