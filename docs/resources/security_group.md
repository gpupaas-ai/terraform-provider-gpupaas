---
page_title: "gpupaas_security_group Resource"
subcategory: ""
description: |-
  Network security group with IP, port-forward, and generic rules.
---

# `gpupaas_security_group`

Manage security groups that gate VM network access.

## Example Usage

```terraform
resource "gpupaas_security_group" "default" {
  metadata = {
    name      = "default"
    project   = "demo"
    workspace = "team-a"
  }

  spec = {
    security_group = {
      name           = "default"
      system_catalog = true
    }

    ip_rules = [
      {
        source_cidr = "10.0.0.0/8"
        application = "SSH"
        action      = "ACCEPT"
      },
    ]

    port_forward_rules = [
      {
        source_cidr      = "10.0.0.0/8"
        application      = "JUPYTER"
        application_port = "8888"
        protocol         = "TCP"
      },
    ]
  }
}
```

## Schema

### Required

- `metadata` — `name`, `project` (required), optional `workspace`.
- `spec`
  - `security_group` (Attributes, required) `{ name, system_catalog }`.
  - `type` (String, optional)
  - `ip_rules` (List of Attributes, optional)
    - `source_cidr`, `application`, `action` (String)
  - `port_forward_rules` (List of Attributes, optional)
    - `source_cidr`, `application`, `application_port`, `protocol` (String)
  - `rules` (List of Attributes, optional)
    - `source_cidr`, `application`, `application_port`, `protocol`, `action` (String)
  - `sharing` (Attributes, optional)

### Read-Only

- `id` (String) `<project>/<name>` or `<project>/<workspace>/<name>`.
- `status.status`, `status.reason`, `status.action` (String)

## Import

```bash
terraform import gpupaas_security_group.default demo/team-a/default
```
