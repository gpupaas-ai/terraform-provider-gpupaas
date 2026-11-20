data "gpupaas_object_storage" "backups" {
  project = "system-catalog"
  name    = "team-backups"
}

output "status" {
  value = data.gpupaas_object_storage.backups.status.status
}
