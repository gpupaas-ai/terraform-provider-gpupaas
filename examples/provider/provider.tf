terraform {
  required_version = ">= 1.5.0"

  required_providers {
    gpupaas = {
      source  = "gpupaas-ai/gpupaas"
      version = "~> 0.1"
    }
  }
}

# All attributes are optional; explicit values override the corresponding
# environment variables (GPUPAAS_ENDPOINT, GPUPAAS_API_KEY / GPUPAAS_TOKEN,
# GPUPAAS_USER_AGENT). Endpoint defaults to https://console.gpupaas.ai.
provider "gpupaas" {
  endpoint   = "https://console.gpupaas.ai"
  token      = var.gpupaas_token
  user_agent = "my-team/1.0"
}

variable "gpupaas_token" {
  type      = string
  sensitive = true
}
