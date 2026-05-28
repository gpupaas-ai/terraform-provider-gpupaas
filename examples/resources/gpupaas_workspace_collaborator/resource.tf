resource "gpupaas_workspace_collaborator" "alice" {
  metadata = {
    name      = "alice@example.com"
    project   = gpupaas_project.demo.metadata.name
    workspace = gpupaas_workspace.team_a.metadata.name
  }

  spec = {
    username = "alice@example.com"
    role     = "PAAS_WORKSPACE_COLLABORATOR" # or PAAS_WORKSPACE_COLLABORATOR_READ_ONLY
  }
}

# Invite a brand-new external user by setting email instead of username:
resource "gpupaas_workspace_collaborator" "bob_invite" {
  metadata = {
    name      = "bob@example.com"
    project   = gpupaas_project.demo.metadata.name
    workspace = gpupaas_workspace.team_a.metadata.name
  }

  spec = {
    email      = "bob@example.com"
    first_name = "Bob"
    last_name  = "Builder"
    role       = "PAAS_WORKSPACE_COLLABORATOR_READ_ONLY"
  }
}

# Import example (workspace-scoped):
# terraform import gpupaas_workspace_collaborator.alice demo/team-a/alice@example.com
