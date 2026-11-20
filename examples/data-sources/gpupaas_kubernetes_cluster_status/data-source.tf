data "gpupaas_kubernetes_cluster_status" "c" {
  project = "system-catalog"
  cluster = "my-cluster"
}

output "cluster_health" {
  value = data.gpupaas_kubernetes_cluster_status.c.health
}

# The private IP of every control-plane node.
output "control_plane_ips" {
  value = [for n in data.gpupaas_kubernetes_cluster_status.c.control_plane.nodes : n.private_ip]
}

# The nodes that are not ready, per worker pool.
output "unready_nodes" {
  value = {
    for p in data.gpupaas_kubernetes_cluster_status.c.worker_pools :
    p.name => [for n in p.nodes : n.hostname if n.state["ClusterNodeReady"] != "true"]
  }
}
