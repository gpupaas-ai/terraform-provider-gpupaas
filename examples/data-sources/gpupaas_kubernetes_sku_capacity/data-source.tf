# The capacity left for the Kubernetes SKU. The platform's answer is returned as JSON.
data "gpupaas_kubernetes_sku_capacity" "k8s" {
  project = "system-catalog"
  name    = "mks-oneclick-1"
}

# Not every compute type reports capacity (bare metal does not): `supported` is then false and `json` is null.
output "capacity" {
  value = data.gpupaas_kubernetes_sku_capacity.k8s.supported ? jsondecode(data.gpupaas_kubernetes_sku_capacity.k8s.json) : null
}
