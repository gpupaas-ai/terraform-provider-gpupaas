resource "gpupaas_mks_cluster" "demo" {
  metadata = {
    name         = "mks-demo"
    project      = gpupaas_project.demo.metadata.name
    display_name = "MKS Demo Cluster"
    description  = "Managed Kubernetes cluster for team A"
  }

  spec = {
    kubernetes_version = "1.31"
    cni                = "calico"
    os                 = "ubuntu22.04"
    ha_enabled         = true
    location           = "us-west-2"

    blueprint = {
      name    = "minimal"
      version = "v1"
    }

    networking = {
      vpc          = "vpc-1"
      subnet       = "subnet-1"
      pod_cidr     = "192.168.0.0/16"
      service_cidr = "10.96.0.0/12"
      ip_family    = "IPv4"
    }

    storage = {
      block = {
        type        = "ceph"
        access_mode = "ReadWriteOnce"
      }
      default_storage_class = "block"
    }

    tags = {
      team = "a"
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

  # Imperative lifecycle action. Changing this value (e.g. "none" -> "upgrade")
  # triggers a single backend action call during the next apply. Allowed values:
  # "none" (default, no-op), "upgrade", "scale_node_group", "add_node_group",
  # "remove_node_group". Each verb requires its matching action_inputs payload.
  #
  # Node-level operations (drain/cordon/uncordon) are managed at the node level,
  # not on the cluster resource.
  desired_action = "none"

  # Examples of action payloads (set desired_action and the matching block):
  #
  # desired_action = "upgrade"
  # action_inputs = {
  #   upgrade = {
  #     k8s_version = "1.32"
  #   }
  # }
  #
  # desired_action = "scale_node_group"
  # action_inputs = {
  #   scale_node_group = {
  #     node_group_name = "ng-1"
  #     desired_count   = 4
  #   }
  # }
  #
  # desired_action = "add_node_group"
  # action_inputs = {
  #   add_node_group = {
  #     node_group = {
  #       id            = "ng-2"
  #       sku           = "g4dn.2xlarge"
  #       desired_nodes = 1
  #     }
  #   }
  # }
  #
  # desired_action = "remove_node_group"
  # action_inputs = {
  #   remove_node_group = {
  #     node_group_name = "ng-2"
  #   }
  # }
}

# API server endpoint reported by the platform once the cluster is ready.
output "cluster_api_server" {
  value = try(gpupaas_mks_cluster.demo.status.output.api_server_endpoint, null)
}

# Observed cluster condition.
output "cluster_condition" {
  value = try(gpupaas_mks_cluster.demo.status.condition, null)
}

# Import example (project-scoped):
# terraform import gpupaas_mks_cluster.demo demo/mks-demo
