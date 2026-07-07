# Day-2 mutable set (D4): guest_password, security_group, sharing.
# Every other spec field and metadata.{name,project,workspace} are immutable
# after creation — editing them fails `terraform plan` with a hard error
# ("<attr> cannot be modified on an existing resource") unless the provider
# is switched to ImmutableReplace mode (see internal/provider/resources/immutable.go).
resource "gpupaas_virtual_machine" "trainer" {
  metadata = {
    name         = "trainer-01"                           # IMMUTABLE
    project      = gpupaas_project.demo.metadata.name     # IMMUTABLE
    workspace    = gpupaas_workspace.team_a.metadata.name # IMMUTABLE
    display_name = "Trainer 01"
    description  = "Primary training VM for team A"
  }

  spec = {
    virtual_machine = { # IMMUTABLE (VM profile / catalog reference)
      name           = "A100-80G-x1"
      system_catalog = true
    }
    type             = "VM"                                  # IMMUTABLE
    cpu_count        = "16"                                  # IMMUTABLE
    memory           = "128Gi"                               # IMMUTABLE
    ssh_key          = gpupaas_ssh_key.dev.metadata.name     # IMMUTABLE
    shared_storage   = gpupaas_storage.scratch.metadata.name # IMMUTABLE
    assign_public_ip = true                                  # IMMUTABLE
    datacenter       = "us-west-2"                           # IMMUTABLE
    image            = "ubuntu-22.04"                        # IMMUTABLE
    boot_disk_size   = 200                                   # IMMUTABLE

    # dns_servers is a SET — reordering it plans no changes (order-agnostic).
    dns_servers = ["1.1.1.1", "8.8.8.8"] # IMMUTABLE

    # --- DAY-2 MUTABLE from here down ---
    security_group = gpupaas_security_group.default.metadata.name # DAY-2 MUTABLE
    # write-only + sensitive: never read back from the backend, so it never
    # false-drifts; changing it plans a clean in-place update.
    guest_password = var.trainer_password # DAY-2 MUTABLE

    sharing = { # DAY-2 MUTABLE — workspaces/projects are SETs, order-agnostic
      share_mode = "SPECIFIC_WORKSPACES"
      workspaces = ["team-a", "team-b"]
    }
  }

  # Declarative power state: "on" (default) or "off". Read reconciles this
  # from the backend-observed status/action, so an out-of-band stop/start
  # shows up as drift and the next apply dispatches Start/Stop accordingly.
  # "reboot" is intentionally not representable here (a one-shot verb, not a
  # convergeable state) — use `paasctl vm reboot` out-of-band.
  power_state = "on"

  timeouts = {
    create = "30m"
    update = "30m"
    delete = "15m"
  }
}

variable "trainer_password" {
  type      = string
  sensitive = true
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
