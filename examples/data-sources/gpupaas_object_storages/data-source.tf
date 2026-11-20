data "gpupaas_object_storages" "all" {
  project = "system-catalog"
}

output "bucket_names" {
  value = data.gpupaas_object_storages.all.names
}
