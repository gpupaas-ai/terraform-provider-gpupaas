# Terraform Provider for GPU PaaS

[![Go](https://img.shields.io/badge/go-%3E=1.22-blue)]()
[![License: Apache 2.0](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)

A declarative Terraform/OpenTofu provider for the GPU PaaS platform. The
provider exposes Kubernetes-style resources (`apiVersion`, `kind`, `metadata`,
`spec`, `status`) and is a thin adapter over the
[`gpupaas-go`](https://github.com/gpupaas-ai/gpupaas-go) SDK.

## Compatibility

| Tool                       | Version  |
|----------------------------|----------|
| HashiCorp Terraform        | >= 1.5   |
| OpenTofu                   | >= 1.6   |
| Go (for building)          | >= 1.22  |
| Terraform Plugin Framework | v1.13.x  |

## Controller compatibility

The API of the GPU PaaS controller changes between controller generations, so every provider release line is tied to
one controller line. A provider `vX.Y.Z` is built against the SDK [`gpupaas-go`](https://github.com/gpupaas-ai/gpupaas-go)
`vX.Y.*` and speaks to the controller line in the same row. Use the provider line that matches your controller and
move to the next line only after the controller is upgraded: an older controller rejects the kinds it does not know,
and a stale provider leaves the resources of a newer controller out of reach. `terraform-provider-gpupaas -brand`
prints the controller API line a binary targets.

| Line   | Controller                                                              | Status         |
|--------|-------------------------------------------------------------------------|----------------|
| v0.1.x | Pre-release: verified against development controllers only, no release | In development |

A patch release (`vX.Y.Z+1`) fixes a defect inside its line and never changes the controller it targets. A controller
generation that changes the API gets its own line and its own `release/vX.Y` branch.

## Supported Resources

An administrator publishes a SKU (a profile); a developer asks for an instance of it. The three instance
resources cover the first-class SKU families, and the workspace, SSH key and security group they use are
resources too.

| Resource                      | Scope                | Notes                                                        |
|-------------------------------|----------------------|--------------------------------------------------------------|
| `gpupaas_workspace`           | Project              | Partition of a project that holds instances, keys and groups. Metadata only |
| `gpupaas_ssh_key`             | Project or Workspace | Public key an instance installs. No update: a new key replaces it |
| `gpupaas_security_group`      | Project or Workspace | Access rules for instances. No update: other rules replace it |
| `gpupaas_high_speed_storage`  | Project or Workspace | High-speed volume (shared filesystem, block) from a storage SKU; waits until it is provisioned; resize in place |
| `gpupaas_object_storage`      | Project or Workspace | Object storage bucket from a storage SKU; S3 details in `status.output` (no instance argument attaches it yet) |
| `gpupaas_virtual_machine`     | Project or Workspace | Virtual machine from a VM SKU; power actions; waits until it runs |
| `gpupaas_bare_metal_instance` | Project or Workspace | Bare-metal machine from a bare-metal SKU; power actions      |
| `gpupaas_kubernetes_cluster`  | Project or Workspace | Kubernetes (MKS) cluster from a cluster SKU: node pools, tags, version and blueprint upgrades |

### Kubernetes cluster node groups

The control plane and every worker pool take, besides the SKU, image and size:

- `network` (`vpc`, `subnet`, optional `security_group`): controllers of release v3.1.41 and later need the VPC and subnet for every
  new pool, and fix the network once the pool exists. To use another network, add a new pool and remove the old one; the plan
  says so instead of failing during the apply.
- `boot_disk_size_gb` (virtual-machine pools only, fixed after creation, checked against the boot disk range of the VM SKU),
  `assign_public_ip` (the same value on every worker pool) and `user_password` (sensitive; the platform never returns it, so
  Terraform cannot see a change made elsewhere).
- `ssh_key`: the name of an SSH key resource, never the key itself. The platform installs one key per cluster and uses the control
  plane's. A new cluster needs an `ssh_key` or a `user_password` (on the control plane or on any pool) when the SKU asks for one
  (`expose_ssh_key_choice` of `gpupaas_kubernetes_sku`) and has no default.
- `datacenter` is optional and must equal the cluster's `location`: nodes are always placed there.

Worker pools also take `labels`, `annotations`, `taints` and `scaling_mode = "automatic"` with `min_nodes` and `max_nodes`
(`node_count` is then the size the pool starts with; the autoscaler moves it inside the bounds and Terraform does not set it back).
Changing a pool's `os_image` re-images its nodes, and the plan warns about it.

The control plane has 1 node, or 3 for high availability, and keeps that size: the platform cannot convert a running cluster to
high availability, so create the cluster with 3 nodes if you need it.

The cluster's `tags` are edited in place through the platform's metadata route, without redeploying the cluster; a controller that
predates the route gets a full update instead. Tags set outside Terraform (in the console) and the labels the platform puts on the
cluster are left alone, and a pool's labels and annotations are part of the pool, so they go through the full update. `status.available_kubernetes_versions` lists the versions the cluster
can be upgraded to, and `status.nodes` carries the private IP, roles, operating system, architecture and status of every node.

These settings follow the v3.1.41 API reference ("MKS First-Class - API Docs") and a mock that enforces its rules; they are not
yet proven on a live controller.

## Data Sources

| Data Source                             | Lookup                                   |
|-----------------------------------------|------------------------------------------|
| `gpupaas_workspace`                     | `project`, `name`                        |
| `gpupaas_ssh_key`                       | `project`, optional `workspace`, `name`  |
| `gpupaas_security_group`                | `project`, optional `workspace`, `name`  |
| `gpupaas_high_speed_storage`            | `project`, optional `workspace`, `name`  |
| `gpupaas_object_storage`                | `project`, optional `workspace`, `name`  |
| `gpupaas_virtual_machine_skus`          | `project`: the names and descriptions of the VM SKUs |
| `gpupaas_bare_metal_skus`               | `project`: the names and descriptions of the bare-metal SKUs |
| `gpupaas_kubernetes_skus`               | `project`: the names and descriptions of the cluster SKUs |
| `gpupaas_virtual_machine`               | `project`, optional `workspace`, `name`  |
| `gpupaas_bare_metal_instance`           | `project`, optional `workspace`, `name`  |
| `gpupaas_kubernetes_cluster`            | `project`, optional `workspace`, `name`  |
| `gpupaas_kubernetes_cluster_kubeconfig` | `project`, optional `workspace`, `name` of the cluster |
| `gpupaas_virtual_machine_sku`           | `project`, `name` (images, sizes, datacenters, boot disk range, public IP mode) |
| `gpupaas_bare_metal_sku`                | `project`, `name` (images, datacenters, public IP policy) |
| `gpupaas_kubernetes_sku`                | `project`, `name` (versions, CNIs, SKUs, images, datacenters, whether a node password or SSH key must be chosen) |
| `gpupaas_kubernetes_sku_create_options` | `project`, `name`: what the SKU allows for the control plane and the workers (compute SKUs, images, shapes) |
| `gpupaas_kubernetes_sku_capacity`       | `project`, `name`: the capacity left for the SKU, as JSON |
| `gpupaas_kubernetes_versions`           | `project`: the Kubernetes versions the controller offers |
| `gpupaas_kubernetes_cluster_status`     | `project`, `cluster`: lifecycle, health, upgrade targets and the nodes behind every pool |
| `gpupaas_kubernetes_cluster_audit_events` | `project`, `cluster`, optional filters: the audit trail of the cluster, newest first |

### List data sources

Every kind can also be listed, to discover what exists or to `for_each` over it: `gpupaas_workspaces`, `gpupaas_ssh_keys`,
`gpupaas_security_groups`, `gpupaas_virtual_machines`, `gpupaas_bare_metal_instances`, `gpupaas_kubernetes_clusters`,
`gpupaas_high_speed_storages`, `gpupaas_object_storages`, and the SKU lists `gpupaas_virtual_machine_skus`,
`gpupaas_bare_metal_skus`, `gpupaas_kubernetes_skus`. They take `project` (and, except for workspaces and SKUs, an optional
`workspace`; unset lists the whole project, its workspaces included) and return `names` and `items`, both sorted by name. An item
carries only a few identifying fields (name, workspace, SKU, status, ...) and never credentials: look one up by name with the
singular data source for the rest.

The storage resources take the name of a storage SKU (a service profile an administrator published) in `spec.type`; there is no
SKU data source for them, because the profile carries platform credentials. A volume's mount or S3 details, including
credentials, are in `status.output`, a sensitive map. Storage is verified against a mock of the API and the shapes
of existing volumes on controllers. A first live attempt on an object storage SKU was accepted by the platform, but its
backend could not provision (a controller-side outage), so a successful provision, resizing and the contents of
`status.output` are not yet proven on a live controller. A failed volume's first delete can fail once on `destroy`
("Please try again"); running `terraform destroy` again removes it.

## Installation

### From the Terraform Registry

> Not published to a registry yet. Until it is, install a build with the local development override below, or from a release zip as described in `DEVELOPER_GUIDE.md`. Once published, declare the source as shown here.

```terraform
terraform {
  required_providers {
    gpupaas = {
      source  = "gpupaas-ai/gpupaas"
      version = "~> 0.1"
    }
  }
}
```

### Local Development Override

To test a locally built provider, use a `dev_overrides` block in
`~/.terraformrc`:

```hcl
provider_installation {
  dev_overrides {
    "gpupaas-ai/gpupaas" = "/absolute/path/to/this/repo/bin"
  }
  direct {}
}
```

Then build and install:

```bash
make install
```

`make install` builds the provider binary to `./bin/terraform-provider-gpupaas`.

## Provider Configuration

```terraform
provider "gpupaas" {
  endpoint   = "https://console.gpupaas.ai"  # optional, this is the default
  token      = var.gpupaas_token             # required, sensitive
  user_agent = "myteam-terraform/1.0"        # optional
}
```

Environment fallbacks:

| Setting     | Environment variables                                  |
|-------------|--------------------------------------------------------|
| `endpoint`  | `GPUPAAS_ENDPOINT`                                     |
| `token`     | `GPUPAAS_API_KEY` (preferred), then `GPUPAAS_TOKEN`    |
| `user_agent`| `GPUPAAS_USER_AGENT`                                   |
| `organization_id` | `GPUPAAS_ORGANIZATION_ID` (optional)             |

### Which organization the provider acts in

- **Organization-level API key** (bound to one organization): leave `organization_id` unset. The platform ignores
  the value for such a key, even a wrong one.
- **Partner-level API key** (not bound to an organization): set `organization_id` to the organization's id or its
  exact, case-sensitive name. The organization must belong to the key's partner.

```terraform
provider "gpupaas" {
  organization_id = "acme-corp" # partner-level key only: the organization's id or exact name
}
```

Without it a partner-level key gets `502 ... id or partnerID/orgID/name not set`, and an unknown id or name gets
`500 ... get organization by name returned error`. The provider keeps those messages and adds what to set.

## Quick Start

```terraform
terraform {
  required_providers {
    gpupaas = {
      source  = "gpupaas-ai/gpupaas"
      version = "~> 0.1"
    }
  }
}

provider "gpupaas" {}

# See what the SKU allows first: images, sizes, datacenters, boot disk range.
data "gpupaas_virtual_machine_sku" "small" {
  project = "system-catalog"
  name    = "vm-test"
}

resource "gpupaas_virtual_machine" "demo" {
  metadata = {
    name      = "demo-vm"
    project   = "system-catalog"
    workspace = "team-a" # create it with gpupaas_workspace or in the console
  }
  spec = {
    type           = data.gpupaas_virtual_machine_sku.small.name
    preset         = data.gpupaas_virtual_machine_sku.small.presets[0].name
    image          = data.gpupaas_virtual_machine_sku.small.default_image
    ssh_key        = "admin-key" # a gpupaas_ssh_key in the workspace, or one made in the console
    boot_disk_size = 25
  }
}
```

See [`examples/`](examples/) for complete configurations of the three resources and every data source.

## Import

Import IDs encode resource scope:

| Resource                      | Import ID                                            |
|-------------------------------|------------------------------------------------------|
| `gpupaas_workspace`           | `<project>/<name>`                                   |
| `gpupaas_ssh_key`             | `<project>/<name>` or `<project>/<workspace>/<name>` |
| `gpupaas_security_group`      | `<project>/<name>` or `<project>/<workspace>/<name>` |
| `gpupaas_virtual_machine`     | `<project>/<name>` or `<project>/<workspace>/<name>` |
| `gpupaas_bare_metal_instance` | `<project>/<name>` or `<project>/<workspace>/<name>` |
| `gpupaas_kubernetes_cluster`  | `<project>/<name>` or `<project>/<workspace>/<name>` |

Example:

```bash
terraform import gpupaas_workspace.team_a system-catalog/team-a
terraform import gpupaas_virtual_machine.demo system-catalog/team-a/demo-vm
terraform import gpupaas_kubernetes_cluster.demo system-catalog/team-a/demo-cluster
terraform import gpupaas_high_speed_storage.shared system-catalog/team-a/training-data
```

## Development

### Build

```bash
make build       # builds ./bin/terraform-provider-gpupaas
make install     # alias for build (used with dev_overrides)
```

### Format and Vet

```bash
go fmt ./...
go vet ./...
```

### Unit Tests

Unit tests use an in-memory fake clientset and do not require API access:

```bash
make test
# or
go test ./...
```

### Acceptance Tests

Acceptance tests exercise the real GPU PaaS API and require credentials:

```bash
export TF_ACC=1
export GPUPAAS_ENDPOINT="https://console.gpupaas.ai"
export GPUPAAS_API_KEY="..."
go test -v ./internal/provider/...
```

### Release builds and white-label brands

One source builds the official provider and every partner brand of `brands.yml`; a brand is a line in that file, never a
branch. A release is pinned to an immutable SDK tag of its controller line and cut from `main` or `release/vX.Y`:

```bash
make pin-sdk SDK_VERSION=v0.2.1            # drop the local replace, pin the SDK tag
make check RELEASE_VERSION=0.2.1           # the release gate, without building
make release RELEASE_VERSION=0.2.1         # gate, then build and package every brand into dist/<type>/
```

`go run ./cmd/brandctl release -allow-local -version ...` skips the gate for local testing. The rules, the branch and tag
scheme and the steps are in [DEVELOPER_GUIDE.md](DEVELOPER_GUIDE.md), section 9.

### Project Layout

```
.
├── brands.yml                     # The brands built from this source (official + partners)
├── cmd/brandctl/                  # Release tool: gate, build and package every brand
├── docs/                          # Registry-formatted provider docs (official brand)
├── examples/                      # HCL examples (provider, resources, data sources)
├── internal/
│   ├── brand/                     # Brand identity: type name, env prefix, text rewriting
│   ├── brandctl/                  # Release gate, docs/examples rendering, packaging
│   ├── client/                    # gpupaas-go client + fakes for tests
│   └── provider/
│       ├── provider.go            # GPUProvider definition
│       └── resources/             # All resources, data sources, and shared utilities
├── main.go                        # Provider entry point
├── go.mod
└── Makefile
```

The provider is intentionally thin: each resource maps schema to a
`gpupaas-go` `apis/v1alpha1` struct, calls a typed clientset, and converts the
response back to state. All HTTP, retry, and authentication concerns live in
the SDK.

## License

Apache License 2.0 — see [LICENSE](LICENSE).

## Testing

`go test ./...` runs the unit tests. `make testacc` (`TF_ACC=1`) additionally runs the end-to-end tests, which drive
the real `terraform` binary against the provider and in-process mocks of the VM, bare-metal and cluster APIs
(`github.com/gpupaas-ai/gpupaas-go/mock/...`); no controller or credentials are needed.
