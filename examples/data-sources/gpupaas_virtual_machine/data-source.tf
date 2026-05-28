data "gpupaas_virtual_machine" "trainer" {
  project   = "demo"
  workspace = "team-a"
  name      = "trainer-01"
}

output "trainer_status" {
  value = data.gpupaas_virtual_machine.trainer.status.status
}
