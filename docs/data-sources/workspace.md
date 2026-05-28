---
page_title: "gpupaas_workspace Data Source"
subcategory: ""
description: |-
  Look up a workspace within a project.
---

# `gpupaas_workspace`

Look up a workspace by name within a project.

## Example Usage

```terraform
data "gpupaas_workspace" "team_a" {
  project = "demo"
  name    = "team-a"
}
```

## Schema

### Required

- `project` (String)
- `name` (String)

### Read-Only

- `id`, `api_version`, `kind` (String)
- `metadata` (Attributes)
- `spec` (Attributes)
- `status.phase` (String)
