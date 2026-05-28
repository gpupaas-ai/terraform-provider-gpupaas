---
page_title: "gpupaas_workspace_collaborator Resource"
subcategory: ""
description: |-
  Assign a Rafay user (or invite an external user) to a workspace.
---

# `gpupaas_workspace_collaborator`

Workspace-scoped collaborator membership. Supports two modes:

- **Assign existing**: set `spec.username` (or `metadata.name`) and `spec.role`.
- **Invite external**: set `spec.email`, `spec.first_name`, `spec.last_name`,
  and `spec.role`.

Updates re-issue the same assign/invite call, which is idempotent on the
backend.

## Example Usage

```terraform
resource "gpupaas_workspace_collaborator" "alice" {
  metadata = {
    name      = "alice@example.com"
    project   = gpupaas_project.demo.metadata.name
    workspace = gpupaas_workspace.team_a.metadata.name
  }

  spec = {
    username = "alice@example.com"
    role     = "PAAS_WORKSPACE_COLLABORATOR"
  }
}
```

## Schema

### Required

- `metadata`
  - `name` (String) Username or email — combined with project/workspace forms the unique key.
  - `project` (String)
  - `workspace` (String)
- `spec`
  - `role` (String, required) `PAAS_WORKSPACE_COLLABORATOR` or
    `PAAS_WORKSPACE_COLLABORATOR_READ_ONLY`.
  - `username` (String, optional)
  - `email` (String, optional)
  - `first_name`, `last_name` (String, optional)
  - `user_type` (String, optional)
  - `is_sso_user` (Bool, optional)

### Read-Only

- `id` (String) `<project>/<workspace>/<name>`.
- `api_version`, `kind` (String)
- `status.phase`, `status.role` (String)

## Import

```bash
terraform import gpupaas_workspace_collaborator.alice demo/team-a/alice@example.com
```
