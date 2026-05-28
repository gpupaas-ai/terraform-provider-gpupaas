---
page_title: "gpupaas_virtual_machine Resource"
subcategory: ""
description: |-
  GPU PaaS virtual machine. Lives at project or workspace scope.
---

# `gpupaas_virtual_machine`

Provision and manage virtual machines. Can be project-scoped (omit
`metadata.workspace`) or workspace-scoped (set `metadata.workspace`).

The backend treats Create as idempotent, so the provider routes Update through
Create. Delete is best-effort and treats NotFound as success.

Imperative lifecycle actions (`start`, `stop`, `reboot`, ...) are driven via
the `desired_action` attribute. When the planned value differs from the
prior-state value, the provider issues a single SDK action call as part of
the Update phase. The attribute is otherwise inert -- it does not modify spec
fields, and resetting it to `"none"` does not trigger any backend call.

## Example Usage

```terraform
resource "gpupaas_virtual_machine" "trainer" {
  metadata = {
    name         = "trainer-01"
    project      = "demo"
    workspace    = "team-a"
    display_name = "Trainer 01"
    description  = "Primary training VM for team A"
  }

  spec = {
    virtual_machine = {
      name           = "A100-80G-x1"
      system_catalog = true
    }
    type             = "VM"
    cpu_count        = "16"
    memory           = "128Gi"
    security_group   = "default"
    ssh_key          = "dev-key"
    assign_public_ip = true
    image            = "ubuntu-22.04"
    boot_disk_size   = 200

    sharing = {
      share_mode = "SPECIFIC_WORKSPACES"
      workspaces = ["team-a", "team-b"]
    }
  }

  # Trigger a stop on next apply by changing this to "stop".
  desired_action = "none"
}
```

## Schema

### Required

- `metadata`
  - `name` (String)
  - `project` (String) Required for VMs.
  - `workspace` (String, optional) Set for workspace-scoped VMs.
  - `display_name` (String, optional)
  - `description` (String, optional)
- `spec`
  - `virtual_machine` (Attributes, required) Catalog reference `{ name, system_catalog }`.
  - Many optional fields: `type`, `cpu_count`, `memory`, `security_group`,
    `ssh_key`, `vpc`, `subnet`, `assign_public_ip`, `datacenter`,
    `guest_password` (sensitive), `dns_servers` (list), `user_data`,
    `timezone`, `shared_storage`, `block_storage_type`, `image`,
    `boot_disk_size`, `create_additional_block`, `additional_block_size`,
    `vm_id` (computed inventory ID; can be supplied on import).
  - `sharing` (Attributes, optional) Cross-workspace/project sharing config.

### Optional

- `desired_action` (String, defaults to `"none"`). Allowed values: `none`,
  `start`, `stop`, `reboot`. Changing the value triggers one SDK action call
  during Update; the same value across applies is a no-op.
- `action_inputs` (Attributes, optional)
  - `envs` (Map of String) — environment variables forwarded with the action.
  - `variables` (Map of String) — generic key/value parameters forwarded with the action.

### Read-Only

- `id` (String) `<project>/<name>` or `<project>/<workspace>/<name>`.
- `api_version`, `kind` (String)
- `metadata.created_by`, `metadata.modified_by` (Attributes)
  - `username` (String)
  - `is_sso_user` (Bool)
  - `options` (Attributes)
- `status`
  - `status`, `reason`, `action` (String)
  - `provisioned_at`, `last_connected_at` (String)
  - `output` (Attributes)
    - `host_name`, `os_name`, `private_ip`, `public_ip`, `server_host`,
      `user_name`, `disk_mount_path` (String)

To read just the live status without managing the resource, use the
[`gpupaas_virtual_machine_status`](../data-sources/virtual_machine_status.md)
data source.

## Import

```bash
# Project-scoped
terraform import gpupaas_virtual_machine.trainer demo/trainer-01

# Workspace-scoped
terraform import gpupaas_virtual_machine.trainer demo/team-a/trainer-01
```
