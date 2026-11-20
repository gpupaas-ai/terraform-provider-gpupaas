data "gpupaas_security_group" "web" {
  project   = "system-catalog"
  workspace = "team-a"
  name      = "web"
}

output "rule_count" {
  value = length(data.gpupaas_security_group.web.spec.rules)
}
