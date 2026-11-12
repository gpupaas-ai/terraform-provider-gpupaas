# The virtual machine SKUs published in a project. Look one up by name for its images, sizes and datacenters.
data "gpupaas_virtual_machine_skus" "all" {
  project = "system-catalog"
}

output "vm_skus" {
  value = data.gpupaas_virtual_machine_skus.all.names
}
