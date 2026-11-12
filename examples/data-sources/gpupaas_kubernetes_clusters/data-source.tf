data "gpupaas_kubernetes_clusters" "all" {
  project = "system-catalog"
}

output "clusters" {
  value = [for c in data.gpupaas_kubernetes_clusters.all.items : "${c.name} (${c.kubernetes_version}, ${c.status})"]
}
