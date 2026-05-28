resource "gpupaas_project" "demo" {
  metadata = {
    name = "demo"
    labels = {
      team = "ml-platform"
    }
  }

  spec = {
    display_name = "Demo Project"
    description  = "Created via Terraform"
  }
}

output "project_phase" {
  value = gpupaas_project.demo.status.phase
}

# Import example:
# terraform import gpupaas_project.demo demo
