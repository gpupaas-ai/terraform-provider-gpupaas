data "gpupaas_virtual_machine_status" "trainer" {
  project   = "demo"
  workspace = "team-a"
  name      = "trainer-01"
}

output "trainer_status" {
  value = data.gpupaas_virtual_machine_status.trainer.status.status
}

output "trainer_public_ip" {
  value = try(data.gpupaas_virtual_machine_status.trainer.status.output.public_ip, null)
}
