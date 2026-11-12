data "gpupaas_kubernetes_sku" "k8s" {
  project = "system-catalog"
  name    = "mks-oneclick-1"
}

output "supported_versions" {
  value = data.gpupaas_kubernetes_sku.k8s.supported_kubernetes_versions
}

output "control_plane_skus" {
  value = data.gpupaas_kubernetes_sku.k8s.control_plane.sku_names
}

output "datacenters" {
  value = data.gpupaas_kubernetes_sku.k8s.datacenters
}
