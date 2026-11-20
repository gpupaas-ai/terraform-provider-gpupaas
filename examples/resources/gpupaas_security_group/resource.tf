# A security group in a workspace: the rules say which traffic may reach the instances that use it. The platform
# has no update for a group: other rules replace it, so detach it from instances first.
resource "gpupaas_security_group" "web" {
  metadata = {
    name      = "web"
    project   = "system-catalog"
    workspace = "team-a"
  }

  spec = {
    rules = [
      { source_cidr = "0.0.0.0/0", application = "ssh", protocol = "tcp", action = "allow" },
      { source_cidr = "10.0.0.0/8", application = "https", application_port = "443", protocol = "tcp", action = "allow" },
    ]
  }
}
