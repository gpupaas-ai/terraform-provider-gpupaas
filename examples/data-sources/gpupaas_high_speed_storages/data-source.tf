# The high-speed volumes of a project, with their profile, size and status. Credentials are not part of the list:
# look one volume up by name with the gpupaas_high_speed_storage data source.
data "gpupaas_high_speed_storages" "all" {
  project = "system-catalog"
}

output "volume_sizes" {
  value = { for v in data.gpupaas_high_speed_storages.all.items : v.name => v.size }
}
