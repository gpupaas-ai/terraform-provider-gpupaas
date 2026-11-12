data "gpupaas_ssh_key" "admin" {
  project   = "system-catalog"
  workspace = "team-a"
  name      = "admin-key"
}

output "public_key" {
  value = data.gpupaas_ssh_key.admin.spec.public_key
}
