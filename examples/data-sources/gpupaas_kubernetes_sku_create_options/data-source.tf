# What the Kubernetes SKU allows when a cluster is created from it: the compute SKUs, images and shapes
# for the control plane and for the workers.
data "gpupaas_kubernetes_sku_create_options" "k8s" {
  project = "system-catalog"
  name    = "mks-oneclick-1"
}

output "worker_skus" {
  value = data.gpupaas_kubernetes_sku_create_options.k8s.workers.sku_names
}

output "worker_images" {
  value = data.gpupaas_kubernetes_sku_create_options.k8s.workers.images
}

output "worker_instance_types" {
  value = [for t in data.gpupaas_kubernetes_sku_create_options.k8s.workers.instance_types : t.display_label]
}
