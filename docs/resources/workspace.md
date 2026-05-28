---
page_title: "gpupaas_workspace Resource"
subcategory: ""
description: |-
  Workspace partition within a GPU PaaS project (project-scoped).
---

# `gpupaas_workspace`

Workspace partition that groups dev resources (VMs, storage, security groups,
SSH keys) and collaborators within a project.

## Example Usage

```terraform
resource "gpupaas_workspace" "team_a" {
  metadata = {
    name    = "team-a"
    project = gpupaas_project.demo.metadata.name
  }

  spec = {
    display_name = "Team A"
    description  = "Shared workspace for Team A"
    icon_url     = "https://example.com/logo.png"
    readme       = "# Team A"
  }
}
```

## Schema

### Required

- `metadata` (Attributes)
  - `name` (String, required)
  - `project` (String, required) Parent project name.
  - `labels`, `annotations` (Map of String, optional/computed)
- `spec` (Attributes)
  - `display_name` (String, optional)
  - `description` (String, optional)
  - `icon_url` (String, optional)
  - `readme` (String, optional)

### Read-Only

- `id` (String) `<project>/<name>`.
- `api_version` (String) Always `gpupaas.ai/v1alpha1`.
- `kind` (String) Always `Workspace`.
- `status.phase` (String)

## Import

```bash
terraform import gpupaas_workspace.team_a demo/team-a
```
