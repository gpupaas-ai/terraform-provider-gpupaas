---
name: add-resource
description: Add or change a resource, data source or attribute in terraform-provider-gpupaas, or fix a defect in it. SDK first, then provider, tests, docs and brand build. Use when asked to add, extend or fix a provider resource.
---

# add-resource

Procedure only. **The rules live in `CONTRIBUTING.md`: read it first** (sections 1, 5, 6, 8). Do not restate or override them here.
The user's request follows this text.

## 1. Preflight

Run `.claude/skills/add-resource/preflight.sh`. It checks the SDK checkout, Go, terraform/tofu, tfplugindocs, branch and build.
`MISSING` = fix it or tell the user, then stop. `NOTE` = mention it in the report. Uncommitted changes you did not make: keep
them out of your change, tell the user, and do not stash, commit or discard them.

## 2. Gather (ask only what is missing)

- New resource, new data source, new attribute, or a fix? Which kind? Scope (project / workspace)?
- Does the SDK already model it? Read `../gpupaas-go/apis/v1alpha1/types_*.go` and `../gpupaas-go/clientset/typed/v1alpha1/interfaces.go`.
  **No: stop.** The SDK comes first: follow `../gpupaas-go/CONTRIBUTING.md` (skill `add-kind` there), then return.
- Fix: reproduce it first with a failing test (step 5).

## 3. Pick a template

Small, project-scoped: `workspace_resource.go`. Slow, workspace-scoped, validated, with actions: `vm_resource.go`, `vm_convert.go`, `vm_validate.go`.
Say which one you copy.

## 4. Implement

Follow `CONTRIBUTING.md` section 5 in order (names, schema, converters, CRUD, waits, `ModifyPlan`, actions, import, data
sources, register in `internal/provider/provider.go`, examples, `make generate-docs`). Apply section 6 as you go: decode helpers, state equals
plan, empty is not null, unknown-after-apply, fields the platform does not store. Mirror new SDK interface methods in
`internal/client/fake.go` and `fake_compute.go`.

## 5. Tests

Unit tests for converters and helpers. An acceptance test against the SDK mock for every behaviour (create, second empty
plan, in-place update, replace, import, destroy, failure). For a fix: write the test, stash only the source change, see it
fail, restore (section 8).

## 6. Checks

Run the checks in `CONTRIBUTING.md` section 8 exactly, including `TF_ACC=1 go test ./... -count=1`. Plain `go test ./...`
skips the acceptance tests. Fix failures; never weaken a test to pass.

## 7. Report

State: what changed (files), each check with its real result, and a **Not verified** list (live run on a real controller,
anything skipped). Open questions last.

## Stop and ask first

Removing or renaming anything public; changing a scope, type or import-ID format; changing behaviour proven in a live run;
touching a shared test environment; committing, pushing or opening a PR. Never: secrets anywhere, AI attribution lines in
commits or PRs, working around a permission denial. No commit unless asked.
