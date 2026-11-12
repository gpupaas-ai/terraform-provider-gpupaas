data "gpupaas_virtual_machine_sku" "small" {
  project = "system-catalog"
  name    = "vm-test"
}

output "images" {
  value = data.gpupaas_virtual_machine_sku.small.images
}

output "presets" {
  value = data.gpupaas_virtual_machine_sku.small.presets
}

output "public_ip_mode" {
  value = data.gpupaas_virtual_machine_sku.small.public_ip
}
