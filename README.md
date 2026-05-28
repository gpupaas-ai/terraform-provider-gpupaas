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

## Supported Resources

| Resource                          | Scope                | Notes                                  |
|-----------------------------------|----------------------|----------------------------------------|
| `gpupaas_project`                 | Cluster              | Top-level container                    |
| `gpupaas_workspace`               | Project              | Workspace partition                    |
| `gpupaas_workspace_collaborator`  | Workspace            | Assign existing user or invite new     |
| `gpupaas_virtual_machine`         | Project or Workspace | VM lifecycle                           |
| `gpupaas_storage`                 | Project or Workspace | Block or shared storage                |
| `gpupaas_security_group`          | Project or Workspace | IP, port-forward, and generic rules    |
| `gpupaas_ssh_key`                 | Project or Workspace | Register SSH public keys               |

## Data Sources

| Data Source                  | Lookup                            |
|------------------------------|-----------------------------------|
| `gpupaas_project`            | `name`                            |
| `gpupaas_workspace`          | `project`, `name`                 |
| `gpupaas_virtual_machine`    | `project`, optional `workspace`, `name` |

## Installation

### From the Terraform Registry

> Coming soon. After publication, declare the source as shown below.

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

resource "gpupaas_project" "demo" {
  metadata = { name = "demo" }
  spec     = { display_name = "Demo Project" }
}

resource "gpupaas_workspace" "team_a" {
  metadata = {
    name    = "team-a"
    project = gpupaas_project.demo.metadata.name
  }
  spec = { display_name = "Team A" }
}

resource "gpupaas_ssh_key" "dev" {
  metadata = {
    name      = "dev-key"
    project   = gpupaas_project.demo.metadata.name
    workspace = gpupaas_workspace.team_a.metadata.name
  }
  spec = {
    ssh_key    = { name = "dev-key", system_catalog = false }
    public_key = file("~/.ssh/id_ed25519.pub")
  }
}

resource "gpupaas_virtual_machine" "trainer" {
  metadata = {
    name      = "trainer-01"
    project   = gpupaas_project.demo.metadata.name
    workspace = gpupaas_workspace.team_a.metadata.name
  }
  spec = {
    virtual_machine = { name = "A100-80G-x1", system_catalog = true }
    type            = "VM"
    cpu_count       = "16"
    memory          = "128Gi"
    image           = "ubuntu-22.04"
    boot_disk_size  = 200
    ssh_key         = gpupaas_ssh_key.dev.spec.ssh_key.name
  }
}
```

See [`examples/`](examples/) for complete configurations covering every
resource and data source.

## Import

Import IDs encode resource scope:

| Resource                          | Import ID                                |
|-----------------------------------|------------------------------------------|
| `gpupaas_project`                 | `<name>`                                 |
| `gpupaas_workspace`               | `<project>/<name>`                       |
| `gpupaas_workspace_collaborator`  | `<project>/<workspace>/<name>`           |
| `gpupaas_virtual_machine`         | `<project>/<name>` or `<project>/<workspace>/<name>` |
| `gpupaas_storage`                 | `<project>/<name>` or `<project>/<workspace>/<name>` |
| `gpupaas_security_group`          | `<project>/<name>` or `<project>/<workspace>/<name>` |
| `gpupaas_ssh_key`                 | `<project>/<name>` or `<project>/<workspace>/<name>` |

Example:

```bash
terraform import gpupaas_workspace.team_a demo/team-a
terraform import gpupaas_virtual_machine.trainer demo/team-a/trainer-01
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

### Project Layout

```
.
├── docs/                          # Registry-formatted provider docs
├── examples/                      # HCL examples (provider, resources, data sources)
├── internal/
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
