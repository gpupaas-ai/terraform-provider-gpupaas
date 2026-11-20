data "gpupaas_workspace" "team_a" {
  project = "system-catalog"
  name    = "team-a"
}

output "display_name" {
  value = data.gpupaas_workspace.team_a.metadata.display_name
}
