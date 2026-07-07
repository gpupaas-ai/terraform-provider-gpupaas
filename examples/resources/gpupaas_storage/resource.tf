# Only `sharing` is DAY-2 MUTABLE; every other spec field (including the new
# `storage_type` — devpb storage.proto field 10, one of block|file|object)
# and metadata.{name,project,workspace} are immutable after creation.
resource "gpupaas_storage" "scratch" {
  metadata = {
    name      = "scratch"                              # IMMUTABLE
    project   = gpupaas_project.demo.metadata.name     # IMMUTABLE
    workspace = gpupaas_workspace.team_a.metadata.name # IMMUTABLE
  }

  spec = {
    storage = { # IMMUTABLE (catalog reference)
      name           = "nvme-shared"
      system_catalog = true
    }
    type                         = "premium-nvme" # IMMUTABLE (profile/tier)
    storage_type                 = "block"        # IMMUTABLE (block | file | object)
    size                         = "1Ti"          # IMMUTABLE
    access_policy                = "RW"           # IMMUTABLE
    contract_term                = "MONTHLY"      # IMMUTABLE
    enable_encryption_at_rest    = "true"         # IMMUTABLE
    enable_encryption_in_transit = "true"         # IMMUTABLE
    datacenter                   = "us-west-2"    # IMMUTABLE

    # --- DAY-2 MUTABLE ---
    sharing = {
      share_mode = "SPECIFIC_WORKSPACES"
      workspaces = ["team-a", "team-b"]
    }
  }
}

# Import example:
# terraform import gpupaas_storage.scratch demo/team-a/scratch
