data "gpupaas_virtual_machine" "demo" {
  project   = "system-catalog"
  workspace = "team-a"
  name      = "demo-vm"
}

output "phase" {
  value = data.gpupaas_virtual_machine.demo.status.phase
}

output "private_ip" {
  value = data.gpupaas_virtual_machine.demo.status.private_ip
}
