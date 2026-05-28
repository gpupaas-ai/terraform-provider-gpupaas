---
page_title: "gpupaas_storage Resource"
subcategory: ""
description: |-
  GPU PaaS storage volume. Project- or workspace-scoped.
---

# `gpupaas_storage`

Manage block or shared storage volumes attached to a project or workspace.

## Example Usage

```terraform
resource "gpupaas_storage" "scratch" {
  metadata = {
    name      = "scratch"
    project   = "demo"
    workspace = "team-a"
  }

  spec = {
    storage = {
      name           = "nvme-shared"
      system_catalog = true
    }
    size                         = "1Ti"
    storage_type                 = "SHARED"
    access_policy                = "RW"
    contract_term                = "MONTHLY"
    enable_encryption_at_rest    = "true"
    enable_encryption_in_transit = "true"
    datacenter                   = "us-west-2"
  }
}
```

## Schema

### Required

- `metadata` — `name`, `project` (required), optional `workspace`.
- `spec`
  - `storage` (Attributes, required) `{ name, system_catalog }`.
  - Optional: `type`, `size`, `datacenter`, `access_policy`, `contract_term`,
    `enable_encryption_at_rest`, `enable_encryption_in_transit`,
    `storage_type`.
  - `sharing` (Attributes, optional) Cross-workspace/project sharing.

### Read-Only

- `id` (String) `<project>/<name>` or `<project>/<workspace>/<name>`.
- `status.status`, `status.reason`, `status.action` (String)

## Import

```bash
terraform import gpupaas_storage.scratch demo/team-a/scratch
```
