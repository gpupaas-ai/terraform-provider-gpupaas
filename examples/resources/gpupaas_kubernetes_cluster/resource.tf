# See what the Kubernetes SKU allows first: versions, CNIs, SKUs, images, datacenters.
data "gpupaas_kubernetes_sku" "k8s" {
  project = "system-catalog"
  name    = "mks-oneclick-1"
}

# The workspace (team-a) and the SSH key (cluster-key) must exist: create them with the gpupaas_workspace and
# gpupaas_ssh_key resources, or in the console.

# A one-node cluster. Leave out what the profile already decides (SKU, image, networking,
# blueprint): the provider fills them from the profile and Terraform shows what the platform
# picked as computed attributes.
resource "gpupaas_kubernetes_cluster" "demo" {
  metadata = {
    name      = "demo-cluster"
    project   = "system-catalog"
    workspace = "team-a"
  }

  spec = {
    profile_name       = data.gpupaas_kubernetes_sku.k8s.name
    kubernetes_version = "1.36" # the platform resolves the latest patch; change it to upgrade

    control_plane = {
      node_count = 1             # 1, or 3 for high availability; fixed once the cluster exists
      ssh_key    = "cluster-key" # the name of an SSH key resource, never the key itself
      # user_password = var.node_password # the alternative to ssh_key; the platform never returns it

      # Controllers that place nodes per pool need a VPC and subnet for every pool. The network is fixed
      # once the pool exists.
      # network = { vpc = "vpc-a", subnet = "subnet-control" }
    }

    # Add, remove or resize pools by editing this list. Pools are identified by name.
    # worker_pools = [
    #   {
    #     name        = "gpu-pool"
    #     node_count  = 2
    #     sku_name    = "bmaas-test-1"
    #     sku_type    = "BMAAS"
    #     network     = { vpc = "vpc-a", subnet = "subnet-gpu", security_group = "workers" }
    #     labels      = { team = "ml" }
    #     annotations = { owner = "ml-platform" }
    #     taints      = [{ key = "nvidia.com/gpu", value = "true", effect = "NoSchedule" }]
    #   },
    #   {
    #     # Between 1 and 5 nodes; node_count is the size the pool starts with.
    #     name              = "cpu-pool"
    #     scaling_mode      = "automatic"
    #     min_nodes         = 1
    #     max_nodes         = 5
    #     node_count        = 2
    #     sku_name          = "vm-small"
    #     sku_type          = "VMAAS"
    #     boot_disk_size_gb = 60 # virtual-machine pools only, fixed after creation
    #     network           = { vpc = "vpc-a", subnet = "subnet-cpu" }
    #   },
    # ]

    # Tags are edited in place, without redeploying the cluster; tags set elsewhere are left alone.
    tags = { team = "platform" }
  }

  timeouts = {
    create = "60m"
    delete = "30m"
  }
}

output "kubernetes_version" {
  description = "Exact version the cluster runs."
  value       = gpupaas_kubernetes_cluster.demo.status.kubernetes_version
}

output "nodes" {
  value = gpupaas_kubernetes_cluster.demo.status.nodes
}

output "upgrade_targets" {
  description = "Versions the cluster can be upgraded to."
  value       = gpupaas_kubernetes_cluster.demo.status.available_kubernetes_versions
}

output "node_addresses" {
  description = "Private IP and role of every node (reported by controllers of release v3.1.41 and later)."
  value       = [for n in gpupaas_kubernetes_cluster.demo.status.nodes : "${n.hostname} ${n.private_ip} ${try(join(",", n.roles), "")}"]
}
