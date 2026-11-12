data "gpupaas_bare_metal_instance" "demo" {
  project   = "system-catalog"
  workspace = "team-a"
  name      = "demo-machine"
}

output "phase" {
  value = data.gpupaas_bare_metal_instance.demo.status.phase
}

output "private_ip" {
  value = data.gpupaas_bare_metal_instance.demo.status.private_ip
}
