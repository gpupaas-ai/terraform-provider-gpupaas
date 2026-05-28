# Tasks: terraform-provider-gpupaas

**Prerequisite:** gpupaas-go SDK tasks complete (or local `replace` in go.mod for development).

Implement in order. Mark tasks complete as you finish them.

- [ ] 1. Repository bootstrap
  - [ ] 1.1 Initialize `go.mod` — `github.com/gpupaas-ai/terraform-provider-gpupaas`, Go 1.22+
  - [ ] 1.2 Add Plugin Framework dependencies and `github.com/gpupaas-ai/gpupaas-go`
  - [ ] 1.3 Add `main.go` with `providerserver.Serve` and `-debug` flag
  - [ ] 1.4 Add `Makefile` (build, test, testacc, fmt, lint, install-local)
  - [ ] 1.5 Add `LICENSE`, stub `README.md`
  - _Requirements: 1.1, 1.4, 10.1_

- [ ] 2. Provider skeleton (`internal/provider/`)
  - [ ] 2.1 Implement `GPUProvider` with Metadata, Schema, Configure, Resources, DataSources
  - [ ] 2.2 Implement provider schema: `endpoint`, `token`, `user_agent`
  - [ ] 2.3 Wire env fallbacks `GPUPAAS_ENDPOINT`, `GPUPAAS_TOKEN`; default endpoint `https://console.gpupaas.ai`
  - [ ] 2.4 Build `gpupaas.Config` and `clientset.NewForConfig`; store in configure data
  - [ ] 2.5 Add `diagnostics.go`, `validators.go` helpers
  - [ ] 2.6 Add provider unit tests (schema, configure from attrs and env)
  - _Requirements: 3.1–3.6, 2.4_

- [ ] 3. Client wrapper (`internal/client/`)
  - [ ] 3.1 Define configure data type holding `clientset.Interface`
  - [ ] 3.2 Add `fake.go` for unit tests
  - _Requirements: 2.1–2.4_

- [ ] 4. Shared resource utilities (`internal/resources/`)
  - [ ] 4.1 Implement `common.go` — import ID parsing (1-, 2-, and 3-segment)
  - [ ] 4.2 Implement `metadata.go` — labels/annotations/project/workspace helpers
  - [ ] 4.3 Implement `converters.go` — model ↔ SDK for all kinds
  - [ ] 4.4 Add conversion unit tests
  - _Requirements: 5.1–5.6, 7.1–7.5, 8.4_

- [ ] **Checkpoint A:** provider configures; converters tested

- [ ] 5. gpupaas_project
  - [ ] 5.1 Implement `project_resource.go` (CRUD, ImportState)
  - [ ] 5.2 Implement `project_data_source.go`
  - [ ] 5.3 Add resource unit tests
  - _Requirements: 4.1, 5.1, 6.1, 7.2_

- [ ] 6. gpupaas_workspace
  - [ ] 6.1 Implement `workspace_resource.go`
  - [ ] 6.2 Implement `workspace_data_source.go`
  - [ ] 6.3 Add tests
  - _Requirements: 4.1, 5.1, 6.1, 7.2_

- [ ] 7. gpupaas_workspace_collaborator
  - [ ] 7.1 Implement `workspace_collaborator_resource.go` (CRUD, ImportState)
  - [ ] 7.2 Import ID: `<project>/<workspace>/<name>`
  - [ ] 7.3 Add unit tests
  - _Requirements: 4.1, 5.1, 7.2_

- [ ] 8. gpupaas_virtual_machine
  - [ ] 8.1 Implement `virtual_machine_resource.go` (project and workspace scope)
  - [ ] 8.2 Implement `virtual_machine_data_source.go`
  - [ ] 8.3 Import ID: 2-segment (project-scoped) or 3-segment (workspace-scoped)
  - [ ] 8.4 Add unit tests
  - _Requirements: 4.1, 5.1, 6.1, 7.3_

- [ ] 9. gpupaas_storage
  - [ ] 9.1 Implement `storage_resource.go` (dual scope CRUD + ImportState)
  - [ ] 9.2 Add unit tests
  - _Requirements: 4.1, 5.1, 7.3_

- [ ] 10. gpupaas_security_group
  - [ ] 10.1 Implement `security_group_resource.go` (nested rule schemas)
  - [ ] 10.2 Add unit tests
  - _Requirements: 4.1, 5.1, 7.3_

- [ ] 11. gpupaas_ssh_key
  - [ ] 11.1 Implement `ssh_key_resource.go`
  - [ ] 11.2 Add unit tests
  - _Requirements: 4.1, 5.1, 7.3_

- [ ] **Checkpoint B:** all resources and data sources compile; unit tests pass

- [ ] 12. Acceptance tests (`internal/acctest/`)
  - [ ] 12.1 Add acctest config helper
  - [ ] 12.2 Add CRUD + import tests per managed resource (skip unless `TF_ACC=1`)
  - [ ] 12.3 Add data source acceptance tests
  - _Requirements: 9.1–9.4_

- [ ] 13. Examples and documentation
  - [ ] 13.1 Add `examples/provider/`, `examples/resources/*`, `examples/data-sources/*`
  - [ ] 13.2 Add `docs/index.md`, resource and data source docs for every exposed kind
  - [ ] 13.3 Complete README: Terraform/OpenTofu, install, dev override, import patterns, testing, SDK link
  - [ ] 13.4 Document kind-specific import rules on each resource docs page
  - _Requirements: 10.1–10.4_

- [ ] 14. Final verification
  - [ ] 14.1 `go mod tidy`
  - [ ] 14.2 `go fmt ./...`
  - [ ] 14.3 `go test ./...`
  - [ ] 14.4 `go build -o bin/terraform-provider-gpupaas .`
  - [ ] 14.5 Manual smoke test with dev override and sample HCL (project + workspace + dev resources)
  - [ ] 14.6 Verify import works for every managed resource
  - _Requirements: acceptance criteria_

- [ ] **Checkpoint C:** build green; local Terraform/OpenTofu apply and import work

## Future SDK kinds

When gpupaas-go adds a new `Kind*`, add tasks here following the same pattern: converters → resource → optional data source → tests → docs → examples.

Reference: [gpupaas-go README appendix](https://github.com/gpupaas-ai/gpupaas-go/blob/main/README.md#appendix-resource-reference).
