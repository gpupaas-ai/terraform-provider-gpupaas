# A high-speed storage volume (a shared filesystem, for example) from a profile an administrator published.
# Instances attach it by name: `hss_storage` on a bare-metal instance, `shared_storage` on a virtual machine.
resource "gpupaas_high_speed_storage" "shared" {
  metadata = {
    name      = "training-data"
    project   = "system-catalog"
    workspace = "team-a"
  }

  spec = {
    type       = "weka-fs-profile" # the storage profile (SKU)
    size       = "500"             # in the unit the profile defines (GB unless it says GiB)
    datacenter = "dc-west"

    enable_encryption_at_rest = true
  }
}

# Mount details and credentials come back in status.output (sensitive, as strings).
output "filesystem_servers" {
  value     = gpupaas_high_speed_storage.shared.status.output["filesystem_servers"]
  sensitive = true
}

resource "gpupaas_bare_metal_instance" "trainer" {
  metadata = {
    name      = "trainer-1"
    project   = "system-catalog"
    workspace = "team-a"
  }

  spec = {
    type        = "bm-gpu-sku"
    image       = "ubuntu-24.04"
    ssh_key     = "admin-key"
    hss_storage = gpupaas_high_speed_storage.shared.metadata.name
  }
}
