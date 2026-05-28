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

output "cluster_condition" {
  value = data.gpupaas_mks_cluster.demo.status.condition
}
