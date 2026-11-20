# The kubeconfig of a running cluster, as the console's "Download Kubeconfig" gives it.
data "gpupaas_kubernetes_cluster_kubeconfig" "k" {
  name      = "demo-cluster"
  project   = "system-catalog"
  workspace = "team-a"
}

# It is sensitive. Write it to a file only where that is acceptable.
resource "local_sensitive_file" "kubeconfig" {
  content         = data.gpupaas_kubernetes_cluster_kubeconfig.k.kubeconfig
  filename        = "${path.module}/kubeconfig.yaml"
  file_permission = "0600"
}
