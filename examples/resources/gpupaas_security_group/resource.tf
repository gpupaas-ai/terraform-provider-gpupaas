resource "gpupaas_security_group" "default" {
  metadata = {
    name      = "default"
    project   = gpupaas_project.demo.metadata.name
    workspace = gpupaas_workspace.team_a.metadata.name
  }

  spec = {
    security_group = {
      name           = "default"
      system_catalog = true
    }

    ip_rules = [
      {
        source_cidr = "10.0.0.0/8"
        application = "SSH"
        action      = "ACCEPT"
      },
      {
        source_cidr = "0.0.0.0/0"
        application = "HTTPS"
        action      = "ACCEPT"
      },
    ]

    port_forward_rules = [
      {
        source_cidr      = "10.0.0.0/8"
        application      = "JUPYTER"
        application_port = "8888"
        protocol         = "TCP"
      },
    ]
  }
}

# Import example:
# terraform import gpupaas_security_group.default demo/team-a/default
