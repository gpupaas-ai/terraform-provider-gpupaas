data "gpupaas_baremetal_machine" "host" {
  project = "demo"
  name    = "bm-host-01"
}

output "host_hostname" {
  value = data.gpupaas_baremetal_machine.host.spec.hostname
}

output "host_online" {
  value = data.gpupaas_baremetal_machine.host.spec.online
}

output "host_conditions" {
  value = data.gpupaas_baremetal_machine.host.status.conditions
}
