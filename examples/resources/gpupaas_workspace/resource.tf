resource "gpupaas_workspace" "team_a" {
  metadata = {
    name    = "team-a"
    project = gpupaas_project.demo.metadata.name
    labels = {
      env = "dev"
    }
  }

  spec = {
    display_name = "Team A"
    description  = "Shared workspace for Team A"
  }
}

# Import example (project-scoped):
# terraform import gpupaas_workspace.team_a demo/team-a
