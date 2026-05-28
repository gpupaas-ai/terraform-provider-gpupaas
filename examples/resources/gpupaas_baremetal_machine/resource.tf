resource "gpupaas_baremetal_machine" "host" {
  metadata = {
    name         = "bm-host-01"
    project      = gpupaas_project.demo.metadata.name
    display_name = "Baremetal Host 01"
    description  = "GPU baremetal host for team A"
  }

  spec = {
    baremetal_provisioner_name = "provisioner-1"
    datacenter                 = "us-west-2"
    device_id                  = "dev-abc123"
    hostname                   = "bm-host-01"
    mac_address                = "00:11:22:33:44:55"
    boot_mode                  = "UEFI"
    online                     = true
    ssh_key                    = "ssh-ed25519 AAAA..."
    user_data                  = "#cloud-config\npackages:\n  - htop\n"

    image = {
      url           = "https://images.example.com/ubuntu-22.04.qcow2"
      checksum      = "sha256:abcdef..."
      checksum_type = "sha256"
      format        = "qcow2"
    }

    root_device_hints = {
      device_name        = "/dev/sda"
      min_size_gigabytes = 200
      rotational         = false
    }

    raid = {
      hardware_raid_volumes = [
        {
          name                     = "vol-0"
          level                    = "1"
          number_of_physical_disks = 2
          physical_disks           = ["sda", "sdb"]
          size_gibibytes           = 500
        }
      ]
    }
  }

  # Imperative lifecycle action. Changing this value (e.g. "none" -> "reboot")
  # triggers a single backend action call during the next apply. Allowed
  # values: "none" (default, no-op), "power_on", "power_off", "reboot",
  # "provision", "reinstall_os".
  desired_action = "none"

  # `reinstall_os` requires an image payload:
  # desired_action = "reinstall_os"
  # action_inputs = {
  #   image = {
  #     url           = "https://images.example.com/ubuntu-24.04.qcow2"
  #     checksum      = "sha256:123456..."
  #     checksum_type = "sha256"
  #     format        = "qcow2"
  #   }
  # }
}

# Observed conditions reported by the platform.
output "host_conditions" {
  value = try(gpupaas_baremetal_machine.host.status.conditions, [])
}

# Read-only audit metadata populated by the backend.
output "host_created_by" {
  value = try(gpupaas_baremetal_machine.host.metadata.created_by.username, null)
}

# Import example (project-scoped):
# terraform import gpupaas_baremetal_machine.host demo/bm-host-01
