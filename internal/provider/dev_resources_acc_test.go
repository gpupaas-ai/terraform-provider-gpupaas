package provider_test

import (
	"fmt"
	"testing"

	"github.com/gpupaas-ai/gpupaas-go/mock/devapi"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// These tests run the real terraform binary against the provider and an in-process mock of the
// workspace (paas.envmgmt.io/v1), SSH key and security group (dev.envmgmt.io/v1) APIs. Run them with TF_ACC=1.

const (
	wsAddr  = "gpupaas_workspace.w"
	keyAddr = "gpupaas_ssh_key.k"
	sgAddr  = "gpupaas_security_group.g"

	testPublicKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExampleKeyMaterialOne tf@example"
)

func newDevMock(t *testing.T) *devapi.Server {
	t.Helper()
	srv := devapi.New()
	t.Cleanup(srv.Close)
	return srv
}

func devConfig(url, body string) string { return providerBlock(url) + body }

func wsConfig(url, description string) string {
	return devConfig(url, fmt.Sprintf(`
resource "gpupaas_workspace" "w" {
  metadata = {
    name         = "team-a"
    project      = %q
    display_name = "Team A"
    description  = %q
    labels       = { team = "a" }
  }
}
`, testProject, description))
}

func TestAccWorkspace_lifecycleAndUpdate(t *testing.T) {
	srv := newDevMock(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy: func(_ *terraform.State) error {
			if srv.Workspace(testProject, "team-a") != nil {
				return fmt.Errorf("workspace team-a still exists")
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: wsConfig(srv.URL, "shared by team a"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(wsAddr, "id", "system-catalog/team-a"),
					resource.TestCheckResourceAttr(wsAddr, "metadata.display_name", "Team A"),
					resource.TestCheckResourceAttr(wsAddr, "metadata.description", "shared by team a"),
					resource.TestCheckResourceAttr(wsAddr, "metadata.labels.team", "a"),
					func(_ *terraform.State) error {
						w := srv.Workspace(testProject, "team-a")
						if w == nil || w.Metadata.DisplayName != "Team A" || w.Metadata.Description != "shared by team a" {
							return fmt.Errorf("stored workspace = %+v", w)
						}
						return nil
					},
				),
			},
			{
				// an in-place update: the description changes, the workspace is not replaced
				Config: wsConfig(srv.URL, "shared by teams a and b"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(wsAddr, plancheck.ResourceActionUpdate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(wsAddr, "metadata.description", "shared by teams a and b"),
					func(_ *terraform.State) error {
						if w := srv.Workspace(testProject, "team-a"); w == nil || w.Metadata.Description != "shared by teams a and b" {
							return fmt.Errorf("the update did not reach the platform: %+v", w)
						}
						return nil
					},
				),
			},
			{
				// no change on the next plan
				Config:   wsConfig(srv.URL, "shared by teams a and b"),
				PlanOnly: true,
			},
			{
				ResourceName:      wsAddr,
				ImportState:       true,
				ImportStateId:     "system-catalog/team-a",
				ImportStateVerify: true,
			},
		},
	})
}

func TestAccWorkspace_minimalIsStable(t *testing.T) {
	srv := newDevMock(t)
	cfg := devConfig(srv.URL, fmt.Sprintf(`
resource "gpupaas_workspace" "w" {
  metadata = {
    name    = "plain"
    project = %q
  }
}

data "gpupaas_workspace" "w" {
  name    = gpupaas_workspace.w.metadata.name
  project = gpupaas_workspace.w.metadata.project
}
`, testProject))
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		Steps: []resource.TestStep{
			{
				Config: cfg,
				Check: resource.ComposeAggregateTestCheckFunc(
					// an unset display name defaults to the name on the platform
					resource.TestCheckResourceAttr(wsAddr, "metadata.display_name", "plain"),
					resource.TestCheckResourceAttr("data.gpupaas_workspace.w", "id", "system-catalog/plain"),
					resource.TestCheckResourceAttr("data.gpupaas_workspace.w", "metadata.display_name", "plain"),
				),
			},
			{Config: cfg, PlanOnly: true},
		},
	})
}

func keyConfig(url, workspaceBlock, publicKey string) string {
	return devConfig(url, workspaceBlock+fmt.Sprintf(`
resource "gpupaas_ssh_key" "k" {
  metadata = {
    name      = "admin-key"
    project   = %q
    workspace = gpupaas_workspace.w.metadata.name
  }
  spec = {
    public_key = %q
  }
}

data "gpupaas_ssh_key" "k" {
  name      = gpupaas_ssh_key.k.metadata.name
  project   = gpupaas_ssh_key.k.metadata.project
  workspace = gpupaas_ssh_key.k.metadata.workspace
}
`, testProject, publicKey))
}

const wsOnly = `
resource "gpupaas_workspace" "w" {
  metadata = {
    name    = "team-a"
    project = "system-catalog"
  }
}
`

func TestAccSshKey_lifecycleAndReplace(t *testing.T) {
	srv := newDevMock(t)
	const otherKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExampleKeyMaterialTwo tf@example"
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy: func(_ *terraform.State) error {
			if srv.SshKey(testProject, "team-a", "admin-key") != nil || srv.Workspace(testProject, "team-a") != nil {
				return fmt.Errorf("the key or its workspace still exists")
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: keyConfig(srv.URL, wsOnly, testPublicKey),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(keyAddr, "id", "system-catalog/team-a/admin-key"),
					resource.TestCheckResourceAttr(keyAddr, "spec.public_key", testPublicKey),
					resource.TestCheckResourceAttr(keyAddr, "status.status", "success"),
					// the platform's default sharing is not shown as a change
					resource.TestCheckNoResourceAttr(keyAddr, "spec.sharing.share_mode"),
					resource.TestCheckResourceAttr("data.gpupaas_ssh_key.k", "spec.public_key", testPublicKey),
					resource.TestCheckResourceAttr("data.gpupaas_ssh_key.k", "status.status", "success"),
					func(_ *terraform.State) error {
						k := srv.SshKey(testProject, "team-a", "admin-key")
						if k == nil || k.Spec.PublicKey != testPublicKey {
							return fmt.Errorf("stored key = %+v", k)
						}
						return nil
					},
				),
			},
			{Config: keyConfig(srv.URL, wsOnly, testPublicKey), PlanOnly: true},
			{
				// the platform cannot update a key: a new key text replaces it
				Config: keyConfig(srv.URL, wsOnly, otherKey),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(keyAddr, plancheck.ResourceActionDestroyBeforeCreate)},
				},
				Check: func(_ *terraform.State) error {
					if k := srv.SshKey(testProject, "team-a", "admin-key"); k == nil || k.Spec.PublicKey != otherKey {
						return fmt.Errorf("the replacement did not reach the platform: %+v", k)
					}
					return nil
				},
			},
			{
				ResourceName:      keyAddr,
				ImportState:       true,
				ImportStateId:     "system-catalog/team-a/admin-key",
				ImportStateVerify: true,
			},
		},
	})
}

func TestAccSshKey_projectScope(t *testing.T) {
	srv := newDevMock(t)
	cfg := devConfig(srv.URL, fmt.Sprintf(`
resource "gpupaas_ssh_key" "k" {
  metadata = {
    name    = "proj-key"
    project = %q
  }
  spec = {
    public_key = %q
  }
}
`, testProject, testPublicKey))
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		Steps: []resource.TestStep{
			{
				Config: cfg,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(keyAddr, "id", "system-catalog/proj-key"),
					resource.TestCheckNoResourceAttr(keyAddr, "metadata.workspace"),
				),
			},
			{ResourceName: keyAddr, ImportState: true, ImportStateId: "system-catalog/proj-key", ImportStateVerify: true},
		},
	})
}

func sgConfig(url, rules string) string {
	return devConfig(url, wsOnly+fmt.Sprintf(`
resource "gpupaas_security_group" "g" {
  metadata = {
    name      = "web"
    project   = %q
    workspace = gpupaas_workspace.w.metadata.name
  }
  spec = {
    rules = [%s]
  }
}

data "gpupaas_security_group" "g" {
  name      = gpupaas_security_group.g.metadata.name
  project   = gpupaas_security_group.g.metadata.project
  workspace = gpupaas_security_group.g.metadata.workspace
}
`, testProject, rules))
}

const (
	ruleSSH   = `{ source_cidr = "0.0.0.0/0", application = "ssh", protocol = "tcp", action = "allow" }`
	ruleHTTPS = `{ source_cidr = "10.0.0.0/8", application = "https", application_port = "443", protocol = "tcp", action = "allow" }`
)

func TestAccSecurityGroup_lifecycleAndReplace(t *testing.T) {
	srv := newDevMock(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy: func(_ *terraform.State) error {
			if srv.SecurityGroup(testProject, "team-a", "web") != nil {
				return fmt.Errorf("security group web still exists")
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: sgConfig(srv.URL, ruleSSH+", "+ruleHTTPS),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(sgAddr, "id", "system-catalog/team-a/web"),
					resource.TestCheckResourceAttr(sgAddr, "spec.rules.#", "2"),
					resource.TestCheckResourceAttr(sgAddr, "spec.rules.0.application", "ssh"),
					resource.TestCheckResourceAttr(sgAddr, "spec.rules.1.application_port", "443"),
					resource.TestCheckNoResourceAttr(sgAddr, "spec.rules.0.application_port"),
					resource.TestCheckResourceAttr(sgAddr, "status.status", "success"),
					resource.TestCheckResourceAttr("data.gpupaas_security_group.g", "spec.rules.#", "2"),
					resource.TestCheckResourceAttr("data.gpupaas_security_group.g", "spec.rules.1.source_cidr", "10.0.0.0/8"),
					func(_ *terraform.State) error {
						g := srv.SecurityGroup(testProject, "team-a", "web")
						if g == nil || len(g.Spec.Rules) != 2 || g.Spec.Rules[1].ApplicationPort != "443" || g.Spec.Rules[0].SourceCIDR != "0.0.0.0/0" {
							return fmt.Errorf("stored group = %+v", g)
						}
						return nil
					},
				),
			},
			{Config: sgConfig(srv.URL, ruleSSH+", "+ruleHTTPS), PlanOnly: true},
			{
				// the platform cannot update a group: other rules replace it
				Config: sgConfig(srv.URL, ruleSSH),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(sgAddr, plancheck.ResourceActionDestroyBeforeCreate)},
				},
				Check: func(_ *terraform.State) error {
					if g := srv.SecurityGroup(testProject, "team-a", "web"); g == nil || len(g.Spec.Rules) != 1 {
						return fmt.Errorf("the replacement did not reach the platform: %+v", g)
					}
					return nil
				},
			},
			{
				ResourceName:      sgAddr,
				ImportState:       true,
				ImportStateId:     "system-catalog/team-a/web",
				ImportStateVerify: true,
			},
		},
	})
}

func TestAccSecurityGroup_platformRejectsInvalidRule(t *testing.T) {
	srv := newDevMock(t)
	bad := `{ source_cidr = "0.0.0.0/0", application = "ssh", protocol = "tcp", action = "maybe" }`
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		Steps: []resource.TestStep{
			{Config: sgConfig(srv.URL, bad), ExpectError: errRe(`rule action "maybe" must be allow or deny`)},
		},
	})
}

func TestAccSshKey_existingNameIsNeverAdopted(t *testing.T) {
	srv := newDevMock(t)
	// a key of that name already exists on the platform, created outside Terraform
	cfg := devConfig(srv.URL, wsOnly+fmt.Sprintf(`
resource "gpupaas_ssh_key" "k" {
  metadata = {
    name      = "admin-key"
    project   = %q
    workspace = gpupaas_workspace.w.metadata.name
  }
  spec = {
    public_key = %q
  }
}
`, testProject, testPublicKey))
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		Steps: []resource.TestStep{
			{
				// the workspace and the foreign key are created first, by the first apply of the workspace only
				Config: devConfig(srv.URL, wsOnly),
				Check: func(_ *terraform.State) error {
					srv.SeedSshKey(testProject, "team-a", "admin-key", "ssh-ed25519 AAAAForeign foreign@example")
					return nil
				},
			},
			{Config: cfg, ExpectError: errRe(`ssh key "admin-key" already exists`)},
			{
				// leave nothing behind for the workspace delete
				Config: devConfig(srv.URL, wsOnly),
				PreConfig: func() {
					_ = srv.DeleteSshKey(testProject, "team-a", "admin-key")
				},
			},
		},
	})
}
