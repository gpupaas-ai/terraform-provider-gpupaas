# The SSH keys of one workspace. Without `workspace`, the whole project: its workspaces' keys too, and each
# item says which workspace it lives in.
data "gpupaas_ssh_keys" "team_a" {
  project   = "system-catalog"
  workspace = "team-a"
}

# Read each one back by name for the rest.
data "gpupaas_ssh_key" "each" {
  for_each  = toset(data.gpupaas_ssh_keys.team_a.names)
  project   = "system-catalog"
  workspace = "team-a"
  name      = each.value
}
