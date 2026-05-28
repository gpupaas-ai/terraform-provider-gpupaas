---
page_title: "gpupaas_virtual_machine_status Data Source"
subcategory: ""
description: |-
  Look up the live provisioning status of a virtual machine.
---

# `gpupaas_virtual_machine_status`

Returns the live `status` block of a virtual machine without fetching the full
resource. Backed by `GET .../virtualmachines/{name}/status`. Useful when you
only need the current lifecycle state (e.g. for outputs, conditions, or
monitoring) and want to avoid permission requirements for reading the full
resource definition.

## Example Usage

```terraform
data "gpupaas_virtual_machine_status" "trainer" {
  project   = "demo"
  workspace = "team-a"
  name      = "trainer-01"
}

output "trainer_status" {
  value = data.gpupaas_virtual_machine_status.trainer.status.status
}
```

## Schema

### Required

- `project` (String)
- `name` (String)

### Optional

- `workspace` (String) Required when the VM is workspace-scoped.

### Read-Only

- `id` (String) `<project>/<name>` or `<project>/<workspace>/<name>`.
- `status` (Attributes) Mirrors the `status` block of `gpupaas_virtual_machine`.
  - `status`, `reason`, `action` (String)
  - `provisioned_at`, `last_connected_at` (String)
  - `output` (Attributes)
    - `host_name`, `os_name`, `private_ip`, `public_ip`, `server_host`,
      `user_name`, `disk_mount_path` (String)
