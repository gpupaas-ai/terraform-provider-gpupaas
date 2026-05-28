resource "gpupaas_storage" "scratch" {
  metadata = {
    name      = "scratch"
    project   = gpupaas_project.demo.metadata.name
    workspace = gpupaas_workspace.team_a.metadata.name
  }

  spec = {
    storage = {
      name           = "nvme-shared"
      system_catalog = true
    }
    size                          = "1Ti"
    storage_type                  = "SHARED"
    access_policy                 = "RW"
    contract_term                 = "MONTHLY"
    enable_encryption_at_rest     = "true"
    enable_encryption_in_transit  = "true"
    datacenter                    = "us-west-2"
  }
}

# Import example:
# terraform import gpupaas_storage.scratch demo/team-a/scratch
