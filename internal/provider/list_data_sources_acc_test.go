package provider_test

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// The list data sources run through real terraform against the dev API mock: a project list covers its workspaces,
// a workspace list only that workspace, and the entries are sorted by name. An empty list works with for_each.

func listTestConfig(url string) string {
	return devConfig(url, fmt.Sprintf(`
locals { project = %q }

resource "gpupaas_workspace" "a" {
  metadata = { name = "team-a", project = local.project }
}

resource "gpupaas_workspace" "b" {
  metadata = { name = "team-b", project = local.project }
}

resource "gpupaas_ssh_key" "z" {
  metadata = { name = "zeta", project = local.project, workspace = gpupaas_workspace.a.metadata.name }
  spec     = { public_key = %q }
}

resource "gpupaas_ssh_key" "a" {
  metadata = { name = "alpha", project = local.project, workspace = gpupaas_workspace.b.metadata.name }
  spec     = { public_key = %q }
}

resource "gpupaas_security_group" "g" {
  metadata = { name = "web", project = local.project, workspace = gpupaas_workspace.a.metadata.name }
  spec = {
    rules = [{ source_cidr = "0.0.0.0/0", application = "ssh", protocol = "tcp", action = "allow" }]
  }
}

resource "gpupaas_high_speed_storage" "fs" {
  metadata = { name = "fs", project = local.project, workspace = gpupaas_workspace.a.metadata.name }
  spec     = { type = "weka-fs", size = "50" }
}

resource "gpupaas_object_storage" "bucket" {
  metadata = { name = "bucket", project = local.project }
  spec     = { type = "s3-small", size = "5" }
}

data "gpupaas_workspaces" "all" {
  project    = local.project
  depends_on = [gpupaas_workspace.a, gpupaas_workspace.b]
}

data "gpupaas_ssh_keys" "project" {
  project    = local.project
  depends_on = [gpupaas_ssh_key.z, gpupaas_ssh_key.a]
}

data "gpupaas_ssh_keys" "team_a" {
  project    = local.project
  workspace  = "team-a"
  depends_on = [gpupaas_ssh_key.z, gpupaas_ssh_key.a]
}

data "gpupaas_security_groups" "all" {
  project    = local.project
  depends_on = [gpupaas_security_group.g]
}

data "gpupaas_high_speed_storages" "all" {
  project    = local.project
  depends_on = [gpupaas_high_speed_storage.fs, gpupaas_object_storage.bucket]
}

data "gpupaas_object_storages" "all" {
  project    = local.project
  depends_on = [gpupaas_high_speed_storage.fs, gpupaas_object_storage.bucket]
}
`, testProject, testPublicKey, testPublicKey+"2"))
}

func TestAccListDataSources_projectWorkspaceAndSorting(t *testing.T) {
	srv := newStorageMock(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		Steps: []resource.TestStep{
			{
				Config: listTestConfig(srv.URL),
				Check: resource.ComposeAggregateTestCheckFunc(
					// workspaces
					resource.TestCheckResourceAttr("data.gpupaas_workspaces.all", "id", "system-catalog"),
					resource.TestCheckResourceAttr("data.gpupaas_workspaces.all", "names.#", "2"),
					resource.TestCheckResourceAttr("data.gpupaas_workspaces.all", "names.0", "team-a"),
					resource.TestCheckResourceAttr("data.gpupaas_workspaces.all", "items.1.name", "team-b"),
					resource.TestCheckResourceAttr("data.gpupaas_workspaces.all", "items.1.display_name", "team-b"),
					// a project list holds the keys of its workspaces, sorted by name; items say where each lives
					resource.TestCheckResourceAttr("data.gpupaas_ssh_keys.project", "names.#", "2"),
					resource.TestCheckResourceAttr("data.gpupaas_ssh_keys.project", "names.0", "alpha"),
					resource.TestCheckResourceAttr("data.gpupaas_ssh_keys.project", "items.0.workspace", "team-b"),
					resource.TestCheckResourceAttr("data.gpupaas_ssh_keys.project", "items.1.name", "zeta"),
					resource.TestCheckResourceAttr("data.gpupaas_ssh_keys.project", "items.1.public_key", testPublicKey),
					// a workspace list holds only its own
					resource.TestCheckResourceAttr("data.gpupaas_ssh_keys.team_a", "id", "system-catalog/team-a"),
					resource.TestCheckResourceAttr("data.gpupaas_ssh_keys.team_a", "names.#", "1"),
					resource.TestCheckResourceAttr("data.gpupaas_ssh_keys.team_a", "names.0", "zeta"),
					// security groups
					resource.TestCheckResourceAttr("data.gpupaas_security_groups.all", "names.0", "web"),
					resource.TestCheckResourceAttr("data.gpupaas_security_groups.all", "items.0.workspace", "team-a"),
					// each storage family lists its own volumes, with the profile, size and status but no credentials
					resource.TestCheckResourceAttr("data.gpupaas_high_speed_storages.all", "names.#", "1"),
					resource.TestCheckResourceAttr("data.gpupaas_high_speed_storages.all", "items.0.name", "fs"),
					resource.TestCheckResourceAttr("data.gpupaas_high_speed_storages.all", "items.0.sku", "weka-fs"),
					resource.TestCheckResourceAttr("data.gpupaas_high_speed_storages.all", "items.0.size", "50"),
					resource.TestCheckResourceAttr("data.gpupaas_high_speed_storages.all", "items.0.status", "success"),
					resource.TestCheckNoResourceAttr("data.gpupaas_high_speed_storages.all", "items.0.output"),
					resource.TestCheckResourceAttr("data.gpupaas_object_storages.all", "names.#", "1"),
					resource.TestCheckResourceAttr("data.gpupaas_object_storages.all", "names.0", "bucket"),
					resource.TestCheckResourceAttr("data.gpupaas_object_storages.all", "items.0.workspace", ""),
				),
			},
			{Config: listTestConfig(srv.URL), PlanOnly: true},
		},
	})
}

func TestAccListDataSources_anEmptyProjectGivesEmptyListsThatForEachAccepts(t *testing.T) {
	srv := newStorageMock(t)
	cfg := devConfig(srv.URL, fmt.Sprintf(`
data "gpupaas_workspaces" "none" {
  project = %q
}

data "gpupaas_ssh_keys" "none" {
  project = %q
}

data "gpupaas_ssh_key" "each" {
  for_each = toset(data.gpupaas_ssh_keys.none.names)
  project  = %q
  name     = each.value
}

output "count" {
  value = length(data.gpupaas_workspaces.none.items) + length(data.gpupaas_ssh_key.each)
}
`, testProject, testProject, testProject))
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		Steps: []resource.TestStep{
			{
				Config: cfg,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.gpupaas_workspaces.none", "names.#", "0"),
					resource.TestCheckResourceAttr("data.gpupaas_workspaces.none", "items.#", "0"),
					resource.TestCheckOutput("count", "0"),
				),
			},
		},
	})
}
