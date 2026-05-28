---
page_title: "GPU PaaS Provider"
subcategory: ""
description: |-
  Manage GPU PaaS infrastructure declaratively with Terraform/OpenTofu.
---

# GPU PaaS Provider

The `gpupaas` provider lets you manage GPU PaaS platform resources — projects,
workspaces, virtual machines, storage, security groups, SSH keys, and
workspace collaborators — through a Kubernetes-style declarative model.

The provider is a thin wrapper around the
[`gpupaas-go`](https://github.com/gpupaas-ai/gpupaas-go) SDK and works with both
HashiCorp Terraform (>= 1.5) and OpenTofu (>= 1.6).

## Example Usage

```terraform
terraform {
  required_providers {
    gpupaas = {
      source  = "gpupaas-ai/gpupaas"
      version = "~> 0.1"
    }
  }
}

provider "gpupaas" {
  endpoint = "https://console.gpupaas.ai"
  token    = var.gpupaas_token
}

resource "gpupaas_project" "demo" {
  metadata = { name = "demo" }
  spec     = { display_name = "Demo Project" }
}
```

## Schema

### Optional

- `endpoint` (String) Base URL of the GPU PaaS API. Defaults to
  `https://console.gpupaas.ai`. Falls back to the `GPUPAAS_ENDPOINT`
  environment variable.
- `token` (String, Sensitive) API token used for authentication. Falls back to
  `GPUPAAS_API_KEY`, then `GPUPAAS_TOKEN`.
- `user_agent` (String) Custom User-Agent suffix appended to outbound API
  requests. Falls back to `GPUPAAS_USER_AGENT`.

## Authentication

| Source                 | Variable                                          |
|------------------------|---------------------------------------------------|
| Explicit configuration | `provider "gpupaas" { token = "…" }`              |
| Environment            | `GPUPAAS_API_KEY` (preferred), then `GPUPAAS_TOKEN` |

If no token is found, the provider fails configuration with a clear error.

## Resource Naming

All resources follow `gpupaas_<snake_case_kind>`:

| Kind                    | Terraform resource              |
|-------------------------|---------------------------------|
| `Project`               | `gpupaas_project`               |
| `Workspace`             | `gpupaas_workspace`             |
| `WorkspaceCollaborator` | `gpupaas_workspace_collaborator`|
| `VirtualMachine`        | `gpupaas_virtual_machine`       |
| `Storage`               | `gpupaas_storage`               |
| `SecurityGroup`         | `gpupaas_security_group`        |
| `SshKey`                | `gpupaas_ssh_key`               |

## Import IDs

Import IDs encode the resource scope:

| Scope               | Format                                  | Example                       |
|---------------------|-----------------------------------------|-------------------------------|
| Cluster (Project)   | `<name>`                                | `demo`                        |
| Project             | `<project>/<name>`                      | `demo/team-a`                 |
| Workspace           | `<project>/<workspace>/<name>`          | `demo/team-a/trainer-01`      |
| Flexible (VM, dev)  | `<project>/<name>` or `<project>/<workspace>/<name>` | either form |

See each resource page for the exact import format.
