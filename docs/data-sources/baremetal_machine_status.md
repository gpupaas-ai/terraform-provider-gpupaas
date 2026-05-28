---
page_title: "gpupaas_baremetal_machine_status Data Source"
subcategory: ""
description: |-
  Look up live runtime info for a baremetal machine.
---

# `gpupaas_baremetal_machine_status`

Returns the live runtime info for a baremetal machine. Backed by
`GET .../baremetalmachines/{name}/status`. The payload is free-form
(hardware, network, ...) and is surfaced as a JSON-encoded string in
`data_json`. This is distinct from the resource's embedded
`status.conditions`.

## Example Usage

```terraform
data "gpupaas_baremetal_machine_status" "host" {
  project = "demo"
  name    = "bm-host-01"
}

output "host_runtime_info" {
  value = try(jsondecode(data.gpupaas_baremetal_machine_status.host.data_json), {})
}
```

## Schema

### Required

- `project` (String)
- `name` (String)

### Read-Only

- `id` (String) — `<project>/<name>`.
- `data_json` (String) — JSON-encoded free-form runtime info returned by the
  platform. Decode with Terraform's `jsondecode()`.
