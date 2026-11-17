# AGENTS.md

Read **[CONTRIBUTING.md](CONTRIBUTING.md)** first. It is the whole guide: map, rules, how to add a resource, tests, checks, agent rules.
Releases and partner builds: [RELEASING.md](RELEASING.md).

Core rules:
- SDK first (`../gpupaas-go`). No HTTP, retries or auth in the provider.
- Copy a real resource: `internal/provider/resources/workspace_resource.go` (small), `vm_resource.go` (full).
- Decode with `decodePlan` / `decodeState` / `decodeConfig`. State after apply must equal the plan.
- Every fix gets a test that fails without it. Real test run: `TF_ACC=1 go test ./... -count=1`.
- Official names in examples and descriptions (`gpupaas_*`, `GPUPAAS_*`).
- No secrets anywhere. No commit, push or shared-environment change unless asked.
- No AI attribution lines (`Co-Authored-By`, "Generated with ...") in commits, PRs or comments (team rule).
