# See what the VM SKU allows first: images, sizes (presets), datacenters, boot disk range, public IP mode.
data "gpupaas_virtual_machine_sku" "small" {
  project = "system-catalog"
  name    = "vm-test"
}

# The workspace (team-a) and the SSH key (admin-key) must exist: create them with the gpupaas_workspace and
# gpupaas_ssh_key resources, or in the console.

# One VM from the SKU. Creating it provisions the VM on a host of the SKU's datacenter and waits until it
# runs. Changing `type`, `image`, the size or the boot disk replaces the VM.
resource "gpupaas_virtual_machine" "demo" {
  metadata = {
    name      = "demo-vm"
    project   = "system-catalog"
    workspace = "team-a"
  }

  spec = {
    type = data.gpupaas_virtual_machine_sku.small.name
    # A size is one of the SKU's presets. Name it here, or set cpu_count and memory (and gpu_count)
    # to the values of one preset row. A SKU with a single preset needs neither.
    preset  = data.gpupaas_virtual_machine_sku.small.presets[0].name
    image   = data.gpupaas_virtual_machine_sku.small.default_image
    ssh_key = "admin-key"

    # Day-2 (running VM, updated in place): attach, change or remove a security group of the workspace.
    # security_group = "web-sg"
    boot_disk_size = 25 # GB, within the SKU's range

    # Honored only when the SKU's public IP mode is "Optional".
    # assign_public_ip = true
  }

  # Power actions: "start" and "stop" run when the VM is not already in that state, "reboot" runs when
  # the value changes. Leave unset or "none" to do nothing.
  # desired_action = "stop"

  timeouts = {
    create = "45m"
    delete = "30m"
  }
}

output "vm_address" {
  description = "Where to log in."
  value       = gpupaas_virtual_machine.demo.status.public_ip != null ? gpupaas_virtual_machine.demo.status.public_ip : gpupaas_virtual_machine.demo.status.private_ip
}

output "login_user" {
  value = gpupaas_virtual_machine.demo.status.user_name
}
