---
page_title: "gpupaas_project Data Source"
subcategory: ""
description: |-
  Look up a GPU PaaS project by name.
---

# `gpupaas_project`

Look up a project by name.

## Example Usage

```terraform
data "gpupaas_project" "existing" {
  name = "demo"
}

output "project_phase" {
  value = data.gpupaas_project.existing.status.phase
}
```

## Schema

### Required

- `name` (String) Project name.

### Read-Only

- `id`, `api_version`, `kind` (String)
- `metadata` (Attributes)
- `spec` (Attributes)
- `status.phase` (String)
