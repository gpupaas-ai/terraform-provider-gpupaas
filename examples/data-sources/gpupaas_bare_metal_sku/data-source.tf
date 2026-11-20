data "gpupaas_bare_metal_sku" "gpu" {
  project = "system-catalog"
  name    = "bmaas-l40s"
}

output "images" {
  value = data.gpupaas_bare_metal_sku.gpu.images
}

output "datacenters" {
  value = data.gpupaas_bare_metal_sku.gpu.datacenters
}

output "public_ip_policy" {
  value = data.gpupaas_bare_metal_sku.gpu.assign_public_ip
}
