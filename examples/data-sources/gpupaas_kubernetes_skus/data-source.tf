data "gpupaas_kubernetes_skus" "all" {
  project = "system-catalog"
}

output "kubernetes_skus" {
  value = data.gpupaas_kubernetes_skus.all.names
}
