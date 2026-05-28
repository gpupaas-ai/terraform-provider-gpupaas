data "gpupaas_project" "existing" {
  name = "demo"
}

output "project_status" {
  value = data.gpupaas_project.existing.status
}
