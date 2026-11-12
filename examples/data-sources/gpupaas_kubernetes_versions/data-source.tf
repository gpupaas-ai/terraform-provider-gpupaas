data "gpupaas_kubernetes_versions" "offered" {
  project = "system-catalog"
}

# The minors a cluster may ask for, newest first, and the one the controller preselects.
output "kubernetes_minors" {
  value = data.gpupaas_kubernetes_versions.offered.minors
}

output "default_kubernetes_version" {
  value = data.gpupaas_kubernetes_versions.offered.default_version
}
