data "gpupaas_baremetal_machine_status" "host" {
  project = "demo"
  name    = "bm-host-01"
}

# The platform returns free-form runtime info (hardware, network, ...) as a
# JSON-encoded string. Decode it with jsondecode() to read individual fields.
output "host_runtime_info" {
  value = try(jsondecode(data.gpupaas_baremetal_machine_status.host.data_json), {})
}
