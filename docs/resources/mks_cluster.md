---
page_title: "gpupaas_mks_cluster Resource"
subcategory: ""
description: |-
  Managed Kubernetes cluster with imperative lifecycle actions.
---

# `gpupaas_mks_cluster`

Manage a GPU PaaS managed Kubernetes cluster (`apiVersion: gpupaas.ai/v1alpha1`, `kind: MKSCluster`).
MKS clusters are **project-scoped** (no workspace). Imperative lifecycle actions
(upgrade, scale/add/remove worker node groups) are driven through the
`desired_action` attribute. Node-level operations (drain/cordon/uncordon) are
managed separately at the node level, not on the cluster resource.

## Example Usage

```terraform
resource "gpupaas_mks_cluster" "demo" {
  metadata = {
    name    = "mks-demo"
    project = "demo"
  }

  spec = {
    kubernetes_version = "1.31"
    cni                = "calico"
    os                 = "ubuntu22.04"
    ha_enabled         = true
    location           = "us-west-2"

    networking = {
      pod_cidr     = "192.168.0.0/16"
      service_cidr = "10.96.0.0/12"
      ip_family    = "IPv4"
    }

    control_plane_node_group = {
      id         = "cp"
      sku        = "m5.large"
      node_count = 3
    }

    worker_node_groups = [
      {
        id            = "ng-1"
        sku           = "g4dn.xlarge"
        scaling_mode  = "auto"
        min_nodes     = 1
        max_nodes     = 5
        desired_nodes = 2
      }
    ]
  }

  desired_action = "none"
}
```

## Imperative actions

`desired_action` models the imperative SDK sub-routes. Changing the value (from
its prior state) triggers a single API call on the next apply. Setting it back
to `none` (or omitting it) is a no-op — the provider never auto-re-triggers
actions on refresh. Each verb requires its matching `action_inputs.<verb>`
payload.

| `desired_action`    | Effect                                                | Required `action_inputs`                                   |
|---------------------|-------------------------------------------------------|------------------------------------------------------------|
| `none` (default)    | No-op                                                 | —                                                          |
| `upgrade`           | Upgrades the cluster Kubernetes / platform version    | `upgrade` (`k8s_version`, `platform_version`)              |
| `scale_node_group`  | Scales an existing worker node group                  | `scale_node_group` (`node_group_name`, `desired_count`, …) |
| `add_node_group`    | Adds a new worker node group                          | `add_node_group` (`node_group`)                            |
| `remove_node_group` | Removes a worker node group                           | `remove_node_group` (`node_group_name`)                    |

Examples:

```terraform
# Upgrade
desired_action = "upgrade"
action_inputs = {
  upgrade = {
    k8s_version = "1.32"
  }
}

# Scale a worker node group
desired_action = "scale_node_group"
action_inputs = {
  scale_node_group = {
    node_group_name = "ng-1"
    desired_count   = 4
  }
}

# Add a worker node group
desired_action = "add_node_group"
action_inputs = {
  add_node_group = {
    node_group = {
      id            = "ng-2"
      sku           = "g4dn.2xlarge"
      desired_nodes = 1
    }
  }
}

# Remove a worker node group
desired_action = "remove_node_group"
action_inputs = {
  remove_node_group = {
    node_group_name = "ng-2"
  }
}
```

## Schema

### Required

- `metadata` — `name`, `project` (required). No `workspace`.
- `spec` (Attributes, required)

### Optional (within `spec`)

- `kubernetes_version`, `platform_version` (String) — cluster versions.
- `cni`, `cni_version` (String) — CNI plugin and version (e.g. `calico`).
- `os` (String) — node operating system (e.g. `ubuntu22.04`).
- `ha_enabled`, `dedicated_control_plane` (Bool).
- `location` (String) — deployment location / region.
- `blueprint` (Attributes) — `{ name, version }`.
- `networking` (Attributes) — `vpc`, `subnet`, `pod_cidr`, `service_cidr`,
  `ip_family`, `pod_cidr_v6`, `service_cidr_v6`, `security_groups`.
- `proxy` (Attributes) — `http_proxy`, `https_proxy`, `no_proxy`,
  `proxy_root_ca`, `tls_terminate`.
- `storage` (Attributes) — `block`, `shared_fs`, `object`, `high_speed`
  (each `{ type, access_mode, reclaim_policy, config }`) and
  `default_storage_class`.
- `tags` (Map of String).
- `control_plane_node_group` (Attributes) — node group configuration.
- `worker_node_groups` (List of Attributes) — node group configurations.
- `nodes` (List of Attributes) — node specifications for device-based clusters.

Each node group supports `id`, `sku`, `scaling_mode`, `node_count`,
`min_nodes`, `max_nodes`, `desired_nodes`, `public_ip`, `ssh_key`, `user_data`,
`node_labels`, `node_annotations`, and `kubelet_config` (`{ key_value, yaml }`).

### Top-level Optional

- `desired_action` (String) — imperative action; one of `none`, `upgrade`,
  `scale_node_group`, `add_node_group`, `remove_node_group`.
- `action_inputs` (Attributes) — per-verb payload accompanying
  `desired_action`; sub-blocks `upgrade`, `scale_node_group`, `add_node_group`,
  `remove_node_group`.

### Read-Only

- `id` (String) — `<project>/<name>`.
- `api_version`, `kind` (String)
- `status` (Attributes) — `condition`, `condition_reason`, `action`, and
  `output` (`api_server_endpoint`, `cluster_id_edgesrv`).

## Import

```bash
terraform import gpupaas_mks_cluster.demo demo/mks-demo
```
