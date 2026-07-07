# Only `sharing` is DAY-2 MUTABLE; public_key/type/name and
# metadata.{name,project,workspace} are immutable after creation — rotate a
# key by destroying and recreating this resource (or creating a new one).
resource "gpupaas_ssh_key" "dev" {
  metadata = {
    name      = "dev-key"                              # IMMUTABLE
    project   = gpupaas_project.demo.metadata.name     # IMMUTABLE
    workspace = gpupaas_workspace.team_a.metadata.name # IMMUTABLE
  }

  spec = {
    ssh_key = { # IMMUTABLE (catalog reference)
      name           = "dev-key"
      system_catalog = false
    }
    public_key = file("~/.ssh/id_ed25519.pub") # IMMUTABLE

    # --- DAY-2 MUTABLE ---
    sharing = {
      share_mode = "SPECIFIC_WORKSPACES"
      workspaces = ["team-a", "team-b"]
    }
  }
}

# Import example:
# terraform import gpupaas_ssh_key.dev demo/team-a/dev-key
