---
page_title: "gpupaas_project Resource"
subcategory: ""
description: |-
  Top-level GPU PaaS project (cluster-scoped).
---

# `gpupaas_project`

Top-level container that groups workspaces, virtual machines, storage, and
other resources. Cluster-scoped — no parent project/workspace.

## Example Usage

```terraform
resource "gpupaas_project" "demo" {
  metadata = {
    name = "demo"
    labels = {
      team = "ml-platform"
    }
  }

  spec = {
    display_name = "Demo Project"
    description  = "Created via Terraform"
  }
}
```

## Schema

### Required

- `metadata` (Attributes) Standard metadata block.
  - `name` (String, required) Project name. Used as the unique key.
  - `labels`, `annotations` (Map of String, optional/computed)
- `spec` (Attributes)
  - `display_name` (String, optional)
  - `description` (String, optional)
  - `default` (Bool, optional)

### Read-Only

- `id` (String) Computed unique identifier (equal to `metadata.name`).
- `api_version` (String) Always `gpupaas.ai/v1alpha1`.
- `kind` (String) Always `Project`.
- `status` (Attributes)
  - `phase` (String)

## Import

```bash
terraform import gpupaas_project.demo demo
```
