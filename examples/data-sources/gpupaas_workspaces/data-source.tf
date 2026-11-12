# Every workspace of a project, sorted by name.
data "gpupaas_workspaces" "all" {
  project = "system-catalog"
}

output "workspace_names" {
  value = data.gpupaas_workspaces.all.names
}
