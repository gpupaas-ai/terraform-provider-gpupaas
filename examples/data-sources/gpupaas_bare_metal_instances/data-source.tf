data "gpupaas_bare_metal_instances" "all" {
  project = "system-catalog"
}

output "bare_metal_by_sku" {
  value = { for b in data.gpupaas_bare_metal_instances.all.items : b.name => b.sku }
}
