resource "gpupaas_ssh_key" "dev" {
  metadata = {
    name      = "dev-key"
    project   = gpupaas_project.demo.metadata.name
    workspace = gpupaas_workspace.team_a.metadata.name
  }

  spec = {
    ssh_key = {
      name           = "dev-key"
      system_catalog = false
    }
    public_key = file("~/.ssh/id_ed25519.pub")
  }
}

# Import example:
# terraform import gpupaas_ssh_key.dev demo/team-a/dev-key
