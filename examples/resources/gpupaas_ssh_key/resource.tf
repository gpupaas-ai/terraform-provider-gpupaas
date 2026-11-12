# An SSH key in a workspace. Instances in that workspace name it with `ssh_key`. The platform has no update
# for a key: a new public key replaces it.
resource "gpupaas_ssh_key" "admin" {
  metadata = {
    name      = "admin-key"
    project   = "system-catalog"
    workspace = "team-a"
  }

  spec = {
    public_key = file("~/.ssh/id_ed25519.pub")
  }
}
