data "gpupaas_security_groups" "all" {
  project = "system-catalog"
}

output "groups_by_workspace" {
  value = { for g in data.gpupaas_security_groups.all.items : g.name => g.workspace }
}
