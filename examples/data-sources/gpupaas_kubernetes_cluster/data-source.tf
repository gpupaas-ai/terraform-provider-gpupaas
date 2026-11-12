data "gpupaas_kubernetes_cluster" "demo" {
  project   = "system-catalog"
  workspace = "team-a" # omit for a project-scoped cluster
  name      = "demo-cluster"
}

output "kubernetes_version" {
  value = data.gpupaas_kubernetes_cluster.demo.status.kubernetes_version
}

output "cluster_phase" {
  value = data.gpupaas_kubernetes_cluster.demo.status.phase
}

output "node_count" {
  value = data.gpupaas_kubernetes_cluster.demo.status.node_count
}
