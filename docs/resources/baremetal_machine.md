---
page_title: "gpupaas_baremetal_machine Resource"
subcategory: ""
description: |-
  Managed baremetal host with imperative lifecycle actions.
---

# `gpupaas_baremetal_machine`

Manage a GPU PaaS baremetal machine (`apiVersion: gpupaas.ai/v1alpha1`, `kind: BaremetalMachine`).
Baremetal machines are **project-scoped** (no workspace). Imperative lifecycle
actions (power on/off, reboot, provision, reinstall OS) are driven through the
`desired_action` attribute.

## Example Usage

```terraform
resource "gpupaas_baremetal_machine" "host" {
  metadata = {
    name    = "bm-host-01"
    project = "demo"
  }

  spec = {
    baremetal_provisioner_name = "provisioner-1"
    datacenter                 = "us-west-2"
    hostname                   = "bm-host-01"
    mac_address                = "00:11:22:33:44:55"
    boot_mode                  = "UEFI"
    online                     = true

    image = {
      url           = "https://images.example.com/ubuntu-22.04.qcow2"
      checksum      = "sha256:abcdef..."
      checksum_type = "sha256"
      format        = "qcow2"
    }
  }

  desired_action = "none"
}
```

## Imperative actions

`desired_action` models the imperative SDK sub-routes. Changing the value (from
its prior state) triggers a single API call on the next apply. Setting it back
to `none` (or omitting it) is a no-op — the provider never auto-re-triggers
actions on refresh.

| `desired_action` | Effect                                   |
|------------------|------------------------------------------|
| `none` (default) | No-op                                    |
| `power_on`       | Powers the host on                       |
| `power_off`      | Powers the host off                      |
| `reboot`         | Reboots the host                         |
| `provision`      | Deploys the host with its current spec   |
| `reinstall_os`   | Reimages the host with `action_inputs.image` |

`reinstall_os` requires `action_inputs.image`:

```terraform
desired_action = "reinstall_os"
action_inputs = {
  image = {
    url           = "https://images.example.com/ubuntu-24.04.qcow2"
    checksum      = "sha256:123456..."
    checksum_type = "sha256"
    format        = "qcow2"
  }
}
```

## Schema

### Required

- `metadata` — `name`, `project` (required). No `workspace`.
- `spec` (Attributes, required)

### Optional (within `spec`)

- `architecture` (String, computed when unset) — CPU architecture.
- `automated_cleaning_mode` (String) — set to `disabled` to skip cleaning.
- `baremetal_provisioner_name` (String) — provisioner that owns the host.
- `boot_mode` (String) — `UEFI`, `Legacy`, or `UEFISecureBoot`.
- `datacenter`, `device_id`, `hostname`, `mac_address` (String)
- `online` (Bool) — desired power state.
- `ssh_key`, `system_user_data`, `user_data` (String)
- `image` (Attributes) — `{ url, checksum, checksum_type, format }`.
- `root_device_hints` (Attributes) — disk selection hints.
- `raid` (Attributes) — `hardware_raid_volumes` and `software_raid_volumes`.

### Top-level Optional

- `desired_action` (String) — imperative action; one of `none`, `power_on`,
  `power_off`, `reboot`, `provision`, `reinstall_os`.
- `action_inputs` (Attributes) — payload for `desired_action`; currently
  `image` for `reinstall_os`.

### Read-Only

- `id` (String) — `<project>/<name>`.
- `api_version`, `kind` (String)
- `status.conditions` (List of Attributes) — `type`, `status`, `reason`,
  `last_updated`.

## Import

```bash
terraform import gpupaas_baremetal_machine.host demo/bm-host-01
```
