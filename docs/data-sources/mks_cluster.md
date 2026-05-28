---
page_title: "gpupaas_mks_cluster Data Source"
subcategory: ""
description: |-
  Look up a managed Kubernetes cluster by name within a project.
---

# `gpupaas_mks_cluster`

Look up a GPU PaaS managed Kubernetes cluster by name within a project. Returns
the full `spec` and `status` of the cluster.

## Example Usage

```terraform
data "gpupaas_mks_cluster" "demo" {
  project = "demo"
  name    = "mks-demo"
}

output "cluster_kubernetes_version" {
  value = data.gpupaas_mks_cluster.demo.spec.kubernetes_version
}

output "cluster_api_server" {
  value = data.gpupaas_mks_cluster.demo.status.output.api_server_endpoint
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
- `spec` (Attributes) — mirrors the `spec` of `gpupaas_mks_cluster`.
- `status` (Attributes) — `condition`, `condition_reason`, `action`, and
  `output` (`api_server_endpoint`, `cluster_id_edgesrv`).
