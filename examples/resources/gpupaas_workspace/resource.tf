# A workspace is a partition of a project. Put instances, SSH keys and security groups in it with
# metadata.workspace. A workspace that still holds instances cannot be deleted: delete them first. Its SSH keys and security groups go with it.
resource "gpupaas_workspace" "team_a" {
  metadata = {
    name         = "team-a"
    project      = "system-catalog"
    display_name = "Team A"
    description  = "Shared by the team A engineers"
    labels       = { team = "a" }
  }
}
