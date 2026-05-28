---
page_title: "gpupaas_ssh_key Resource"
subcategory: ""
description: |-
  Register an SSH public key for use by virtual machines.
---

# `gpupaas_ssh_key`

Register an SSH public key that can be attached to virtual machines.

## Example Usage

```terraform
resource "gpupaas_ssh_key" "dev" {
  metadata = {
    name      = "dev-key"
    project   = "demo"
    workspace = "team-a"
  }

  spec = {
    ssh_key = {
      name           = "dev-key"
      system_catalog = false
    }
    public_key = file("~/.ssh/id_ed25519.pub")
  }
}
```

## Schema

### Required

- `metadata` — `name`, `project` (required), optional `workspace`.
- `spec`
  - `ssh_key` (Attributes, required) `{ name, system_catalog }`.
  - `type` (String, optional)
  - `name` (String, optional) Optional logical name; falls back to `metadata.name`.
  - `public_key` (String, optional) OpenSSH public key string.
  - `sharing` (Attributes, optional)

### Read-Only

- `id` (String) `<project>/<name>` or `<project>/<workspace>/<name>`.
- `status.status`, `status.reason`, `status.action` (String)

## Import

```bash
terraform import gpupaas_ssh_key.dev demo/team-a/dev-key
```
