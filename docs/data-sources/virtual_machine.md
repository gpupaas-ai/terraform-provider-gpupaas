---
page_title: "gpupaas_virtual_machine Data Source"
subcategory: ""
description: |-
  Look up a virtual machine by name.
---

# `gpupaas_virtual_machine`

Look up a virtual machine by name within a project (and optionally a
workspace).

## Example Usage

```terraform
data "gpupaas_virtual_machine" "trainer" {
  project   = "demo"
  workspace = "team-a"
  name      = "trainer-01"
}
```

## Schema

### Required

- `project` (String)
- `name` (String)

### Optional

- `workspace` (String) Required when the VM is workspace-scoped.

### Read-Only

- `id`, `api_version`, `kind` (String)
- `metadata` (Attributes)
- `spec` (Attributes, mirrors the resource attributes)
- `status` (Attributes, mirrors the resource status with `output` nested)
