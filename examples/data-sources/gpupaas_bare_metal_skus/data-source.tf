data "gpupaas_bare_metal_skus" "all" {
  project = "system-catalog"
}

output "bare_metal_skus" {
  value = data.gpupaas_bare_metal_skus.all.names
}
