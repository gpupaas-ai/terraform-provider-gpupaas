# rules / ip_rules / port_forward_rules are SETs — reordering them (or the
# backend returning them in a different order) plans no changes.
# sharing is also DAY-2 MUTABLE. security_group (catalog ref), type, and
# metadata.{name,project,workspace} are immutable after creation.
resource "gpupaas_security_group" "default" {
  metadata = {
    name      = "default"                              # IMMUTABLE
    project   = gpupaas_project.demo.metadata.name     # IMMUTABLE
    workspace = gpupaas_workspace.team_a.metadata.name # IMMUTABLE
  }

  spec = {
    security_group = { # IMMUTABLE (catalog reference)
      name           = "default"
      system_catalog = true
    }
    type = "default" # IMMUTABLE

    # --- DAY-2 MUTABLE from here down (set semantics: order never matters) ---
    ip_rules = [
      {
        source_cidr = "10.0.0.0/8"
        application = "SSH"
        action      = "ACCEPT"
      },
      {
        source_cidr = "0.0.0.0/0"
        application = "HTTPS"
        action      = "ACCEPT"
      },
    ]

    port_forward_rules = [
      {
        source_cidr      = "10.0.0.0/8"
        application      = "JUPYTER"
        application_port = "8888"
        protocol         = "TCP"
      },
    ]

    sharing = {
      share_mode = "SPECIFIC_WORKSPACES"
      workspaces = ["team-a", "team-b"]
    }
  }
}

# Import example:
# terraform import gpupaas_security_group.default demo/team-a/default
