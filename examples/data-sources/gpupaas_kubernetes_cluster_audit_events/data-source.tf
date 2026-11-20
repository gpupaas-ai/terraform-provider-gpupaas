# Who did what to the cluster, newest first.
data "gpupaas_kubernetes_cluster_audit_events" "upgrades" {
  project = "system-catalog"
  cluster = "my-cluster"
  action  = "cluster.upgrade"
  limit   = 20
}

output "upgrades" {
  value = [for e in data.gpupaas_kubernetes_cluster_audit_events.upgrades.events : "${e.timestamp} ${e.actor} ${e.result}"]
}
