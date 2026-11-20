# See what the bare-metal SKU allows first: images, datacenters, public IP policy, contract terms.
data "gpupaas_bare_metal_sku" "gpu" {
  project = "system-catalog"
  name    = "bmaas-l40s"
}

# The workspace (team-a) and the SSH key (admin-key) must exist: create them with the gpupaas_workspace and
# gpupaas_ssh_key resources, or in the console.

# One machine from the SKU. Creating it provisions real hardware and waits until it runs,
# which takes many minutes. Changing `type` or `image` replaces the machine.
resource "gpupaas_bare_metal_instance" "demo" {
  metadata = {
    name      = "demo-machine"
    project   = "system-catalog"
    workspace = "team-a"
  }

  spec = {
    type    = data.gpupaas_bare_metal_sku.gpu.name
    image   = data.gpupaas_bare_metal_sku.gpu.default_image
    ssh_key = "admin-key"

    # Day-2 (running machine, updated in place; the workflow re-runs for about a minute):
    # security_group  = "web-sg"   # attach, change or remove a security group of the workspace
    # assign_public_ip = true      # Public IP Assignment, when the SKU allows it
    datacenter = data.gpupaas_bare_metal_sku.gpu.datacenters[0]
  }

  # Power actions: "start" and "stop" run when the machine is not already in that state,
  # "power_cycle" and "power_reset" run when the value changes. Leave unset or "none" to do nothing.
  # desired_action = "stop"

  timeouts = {
    create = "60m"
    delete = "30m"
  }
}

output "machine_address" {
  description = "Where to log in."
  value       = gpupaas_bare_metal_instance.demo.status.public_ip != null ? gpupaas_bare_metal_instance.demo.status.public_ip : gpupaas_bare_metal_instance.demo.status.private_ip
}

output "login_user" {
  value = gpupaas_bare_metal_instance.demo.status.user_name
}
