# An object storage bucket from a profile an administrator published. Applications reach it with the S3
# details in status.output.
resource "gpupaas_object_storage" "backups" {
  metadata = {
    name    = "team-backups"
    project = "system-catalog"
  }

  spec = {
    type = "s3-profile" # the object storage profile (SKU)
    size = "100"        # quota, in the unit the profile defines
  }
}

# The bucket and its S3 access keys come back in status.output (sensitive, as strings); which keys it holds
# depends on the profile.
output "backup_output_keys" {
  value = nonsensitive(keys(gpupaas_object_storage.backups.status.output))
}
