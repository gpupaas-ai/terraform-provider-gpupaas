# Steering: terraform-provider-gpupaas

AI steering documents for implementing `github.com/gpupaas-ai/terraform-provider-gpupaas`.

## Always read first

1. [project-overview.md](./project-overview.md) — provider identity, SDK-driven resource catalog, config
2. [architecture-patterns.md](./architecture-patterns.md) — Framework layering, scope, lifecycle, conversion, import
3. [coding-standards.md](./coding-standards.md) — schema, naming, testing, security

## Spec workflow

Before coding, read in order:

1. [../requirements.md](../requirements.md)
2. [../design.md](../design.md)
3. [../tasks.md](../tasks.md)

## Prerequisites

Implement [gpupaas-go](https://github.com/gpupaas-ai/gpupaas-go) first (including dev resources: VirtualMachine, Storage, SecurityGroup, SshKey). The provider depends entirely on the SDK.

**SDK resource reference:** [gpupaas-go README — Appendix](https://github.com/gpupaas-ai/gpupaas-go/blob/main/README.md#appendix-resource-reference)

## Source prompt

Original implementation prompt (authoritative for acceptance criteria and implementation order):

[../../terraform_provider_gpupaas_cursor_prompt.md](../../terraform_provider_gpupaas_cursor_prompt.md)

Key updates in the prompt:

- Default endpoint: `https://console.gpupaas.ai`
- SDK-driven resource catalog (no fixed resource list in specs)
- Additional resources: `gpupaas_workspace_collaborator`, `gpupaas_virtual_machine`, `gpupaas_storage`, `gpupaas_security_group`, `gpupaas_ssh_key`
- Import IDs by metadata scope (1-, 2-, and 3-segment)
- Import must work for every managed resource

## Reference architecture

- [gpupaas-go README appendix](https://github.com/gpupaas-ai/gpupaas-go/blob/main/README.md#appendix-resource-reference) — resource shapes and field tables
- `paasctl/specs/kubectl_style_cli_architecture_prompt.md` — kubectl-style declarative CLI/SDK model

## IDE wiring

Copy steering files to `.cursor/rules/` or `.kiro/steering/` at repo root for persistent AI context. Suggested auto-included rules:

- `project-overview.md` (`inclusion: auto`)
- `architecture-patterns.md` (`inclusion: auto`)
- `coding-standards.md` (`inclusion: auto`)

Link spec workflow from `CLAUDE.md` or project rules:

```markdown
## Active Specs

- [terraform-provider](../terraform-provider/tasks.md)
- gpupaas-go (dependency): github.com/gpupaas-ai/gpupaas-go
```
