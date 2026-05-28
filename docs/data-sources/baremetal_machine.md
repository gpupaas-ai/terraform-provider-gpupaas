---
page_title: "gpupaas_baremetal_machine Data Source"
subcategory: ""
description: |-
  Look up a baremetal machine by name within a project.
---

# `gpupaas_baremetal_machine`

Look up a GPU PaaS baremetal machine by name within a project. Returns the full
`spec` and `status` of the machine.

## Example Usage

```terraform
data "gpupaas_baremetal_machine" "host" {
  project = "demo"
  name    = "bm-host-01"
}

output "host_hostname" {
  value = data.gpupaas_baremetal_machine.host.spec.hostname
}
```

## Schema

### Required

- `project` (String)
- `name` (String)

### Read-Only

- `id` (String) — `<project>/<name>`.
- `api_version`, `kind` (String)
- `metadata` (Attributes)
- `spec` (Attributes) — mirrors the `spec` of `gpupaas_baremetal_machine`.
- `status` (Attributes) — `conditions` list of `type`, `status`, `reason`,
  `last_updated`.
