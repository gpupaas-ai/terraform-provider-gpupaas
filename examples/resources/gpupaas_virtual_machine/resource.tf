resource "gpupaas_virtual_machine" "trainer" {
  metadata = {
    name         = "trainer-01"
    project      = gpupaas_project.demo.metadata.name
    workspace    = gpupaas_workspace.team_a.metadata.name
    display_name = "Trainer 01"
    description  = "Primary training VM for team A"
  }

  spec = {
    virtual_machine = {
      name           = "A100-80G-x1"
      system_catalog = true
    }
    type             = "VM"
    cpu_count        = "16"
    memory           = "128Gi"
    security_group   = gpupaas_security_group.default.metadata.name
    ssh_key          = gpupaas_ssh_key.dev.metadata.name
    shared_storage   = gpupaas_storage.scratch.metadata.name
    assign_public_ip = true
    datacenter       = "us-west-2"
    image            = "ubuntu-22.04"
    boot_disk_size   = 200

    sharing = {
      share_mode = "SPECIFIC_WORKSPACES"
      workspaces = ["team-a", "team-b"]
    }
  }

  # Imperative lifecycle action. Changing this value (e.g. "none" -> "stop")
  # triggers a single backend action call during the next apply. Allowed
  # values: "none" (default, no-op), "start", "stop", "reboot".
  desired_action = "none"

  # Optional inputs forwarded with the action call (envs/variables).
  # action_inputs = {
  #   envs = {
  #     DEBUG = "1"
  #   }
  # }
}

output "trainer_public_ip" {
  value = try(gpupaas_virtual_machine.trainer.status.output.public_ip, null)
}

# Read-only audit metadata populated by the backend.
output "trainer_created_by" {
  value = try(gpupaas_virtual_machine.trainer.metadata.created_by.username, null)
}

# Import example (workspace-scoped):
# terraform import gpupaas_virtual_machine.trainer demo/team-a/trainer-01
# Or project-scoped:
# terraform import gpupaas_virtual_machine.trainer demo/trainer-01
