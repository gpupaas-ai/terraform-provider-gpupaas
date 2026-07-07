# VM LCM walkthrough configuration — see README.md in this directory.
# Reflects the VMAAS first-class design in plan.md (Day-2 mutable set:
# guest_password / security_group / sharing; everything else immutable).

variable "api_token" {
  type      = string
  sensitive = true
}

variable "vm_password" {
  type      = string
  sensitive = true
}

provider "gpupaas" {
  endpoint = "https://console.gpupaas.ai" # or GPUPAAS_ENDPOINT
  token    = var.api_token                # or GPUPAAS_API_KEY
}

resource "gpupaas_security_group" "web" {
  metadata = {
    name    = "web-sg"
    project = "demo-project"
  }
  spec = {
    type = "default"
    # rules is a SET — order never matters, reordering plans no changes
    rules = [
      {
        source_cidr      = "0.0.0.0/0"
        application      = "ssh"
        application_port = "22"
        protocol         = "tcp"
        action           = "allow"
      },
      {
        source_cidr      = "0.0.0.0/0"
        application      = "http"
        application_port = "8080"
        protocol         = "tcp"
        action           = "allow"
      },
    ]
  }
}

resource "gpupaas_virtual_machine" "app" {
  metadata = {
    name      = "app-vm-1"
    project   = "demo-project"
    workspace = "demo-ws"
  }

  spec = {
    type       = "standard-4cpu-16gb" # VmProfile (immutable)
    cpu_count  = "4"                  # immutable
    memory     = "16"                 # immutable
    image      = "ubuntu-22-04"       # immutable (pre-existing Image)
    datacenter = "dc1"                # immutable (admin-preconfigured)
    vpc        = "vpc1"               # immutable
    subnet     = "subnet1"            # immutable
    ssh_key    = "my-ssh-key"         # immutable

    security_group = gpupaas_security_group.web.metadata.name # DAY-2 MUTABLE
    guest_password = var.vm_password                          # DAY-2 MUTABLE (write-only)

    sharing = {             # DAY-2 MUTABLE, order-agnostic
      share_mode = "Custom" # None | All | Custom
      projects = [
        { name = "project-b" },
      ]
    }
  }

  # Declarative power LCM: on | off. OOB stop/start shows as drift.
  # reboot is not a state — use `paasctl vm reboot` for that.
  power_state = "on"

  timeouts = {
    create = "30m"
    delete = "15m"
  }
}
