# The virtual machines of a workspace that are running.
data "gpupaas_virtual_machines" "team_a" {
  project   = "system-catalog"
  workspace = "team-a"
}

output "running_vms" {
  value = [for vm in data.gpupaas_virtual_machines.team_a.items : vm.name if vm.status == "running"]
}
