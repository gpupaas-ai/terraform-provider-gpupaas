data "gpupaas_high_speed_storage" "shared" {
  project   = "system-catalog"
  workspace = "team-a"
  name      = "training-data"
}

output "size" {
  value = data.gpupaas_high_speed_storage.shared.spec.size
}
