package provider_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/gpupaas-ai/gpupaas-go/mock/bmapi"
	"github.com/gpupaas-ai/gpupaas-go/mock/clusterapi"
	"github.com/gpupaas-ai/gpupaas-go/mock/vmapi"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// Regression tests for bugs found in review. Run them with TF_ACC=1.

// A bare-metal machine whose only credential comes from another resource has an unknown password at plan time.
// The "No way to log in" check must not read that as missing.
func TestAccBareMetal_aPasswordKnownOnlyAfterApplyIsAccepted(t *testing.T) {
	srv, _ := newBMMock(t, bmapi.Options{})
	cfg := providerBlock(srv.URL) + fmt.Sprintf(`
resource "terraform_data" "pw" {
  input = "from-another-resource"
}

resource "gpupaas_bare_metal_instance" "b" {
  metadata = { name = "b1", project = %q, workspace = %q }
  spec = {
    type          = "bm-l40s"
    image         = "ubuntu-22"
    user_password = terraform_data.pw.output
  }
}
`, testProject, bmWorkspace)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy:             checkBMGone(srv),
		Steps: []resource.TestStep{
			{Config: cfg},
			{Config: cfg, PlanOnly: true},
		},
	})
}

// A module may pass `timeouts = {}`. The state must keep the empty block, or Terraform reports an inconsistent
// result after apply.
func TestAccVM_anExplicitEmptyTimeoutsBlockIsKept(t *testing.T) {
	srv, _ := newVMMock(t, vmapi.Options{})
	cfg := vmConfig(srv.URL, "", "  timeouts = {}")
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy:             checkVMGone(srv),
		Steps: []resource.TestStep{
			{Config: cfg, Check: resource.TestCheckResourceAttrSet(vmAddr, "timeouts.%")},
			{Config: cfg, PlanOnly: true},
		},
	})
}

func TestAccBareMetal_anExplicitEmptyTimeoutsBlockIsKept(t *testing.T) {
	srv, _ := newBMMock(t, bmapi.Options{})
	cfg := bmConfig(srv.URL, "", "  timeouts = {}")
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy:             checkBMGone(srv),
		Steps: []resource.TestStep{
			{Config: cfg, Check: resource.TestCheckResourceAttrSet(bmAddr, "timeouts.%")},
			{Config: cfg, PlanOnly: true},
		},
	})
}

func TestAccMKSCluster_anExplicitEmptyTimeoutsBlockIsKept(t *testing.T) {
	srv, _ := newMKSMock(t, clusterapi.Options{})
	cfg := clusterConfig(srv.URL, "1.36", "", "  timeouts = {}")
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy:             checkMKSGone(srv),
		Steps: []resource.TestStep{
			{Config: cfg, Check: resource.TestCheckResourceAttrSet(mksAddr, "timeouts.%")},
			{Config: cfg, PlanOnly: true},
		},
	})
}

// An absent timeouts block must stay absent after a refresh (the other half of the rule above).
func TestAccVM_anAbsentTimeoutsBlockStaysAbsent(t *testing.T) {
	srv, _ := newVMMock(t, vmapi.Options{})
	cfg := vmConfig(srv.URL, "", "")
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy:             checkVMGone(srv),
		Steps: []resource.TestStep{
			// an explicit `timeouts = {}` adds "timeouts.%"; an absent block must add nothing
			{Config: cfg, Check: resource.TestCheckNoResourceAttr(vmAddr, "timeouts.%")},
			{Config: cfg, PlanOnly: true},
		},
	})
}

// `worker_pools = []` is an empty list. The state must hold an empty list, not null.
func TestAccMKSCluster_anEmptyWorkerPoolsListIsKept(t *testing.T) {
	srv, _ := newMKSMock(t, clusterapi.Options{})
	cfg := clusterConfig(srv.URL, "1.36", "    worker_pools = []", "")
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy:             checkMKSGone(srv),
		Steps: []resource.TestStep{
			// a null list has no "#" key at all; an empty one has "#" = 0
			{Config: cfg, Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttrSet(mksAddr, "spec.worker_pools.#"),
				resource.TestCheckResourceAttr(mksAddr, "spec.worker_pools.#", "0"),
			)},
			{Config: cfg, PlanOnly: true},
		},
	})
}

// Removing the sharing block makes the volume private again: the platform must be told, and the plan after the
// apply must be empty.
func TestAccStorage_removingTheSharingBlockMakesTheVolumePrivate(t *testing.T) {
	srv := newStorageMock(t)
	cfg := func(sharing string) string {
		return devConfig(srv.URL, fmt.Sprintf(`
resource "gpupaas_high_speed_storage" "v" {
  metadata = {
    name    = "shared-fs"
    project = %q
  }
  spec = {
    type = "weka-fs"
%s
  }
}
`, testProject, sharing))
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy:             noStorageLeft(srv, "", "shared-fs"),
		Steps: []resource.TestStep{
			{
				Config: cfg(`    sharing = { share_mode = "ALL_WORKSPACES" }`),
				Check:  resource.TestCheckResourceAttr(hssAddr, "spec.sharing.share_mode", "ALL_WORKSPACES"),
			},
			{
				Config: cfg(""),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckNoResourceAttr(hssAddr, "spec.sharing.share_mode"),
					// the platform must have been told: the state alone cannot tell "sent None" from "sent nothing"
					func(_ *terraform.State) error {
						v := srv.Storage(testProject, "", "shared-fs")
						if v == nil {
							return fmt.Errorf("volume shared-fs does not exist on the API")
						}
						if sh := v.Spec.Sharing; sh != nil && sh.ShareMode != "" && sh.ShareMode != "None" {
							return fmt.Errorf("the platform still shares the volume (%s)", sh.ShareMode)
						}
						return nil
					},
				),
			},
			{Config: cfg(""), PlanOnly: true},
		},
	})
}

// The credential may come from another resource: ssh_key alone, then a whole spec built from another resource.
func TestAccBareMetal_anSSHKeyKnownOnlyAfterApplyIsAccepted(t *testing.T) {
	srv, _ := newBMMock(t, bmapi.Options{})
	cfg := providerBlock(srv.URL) + fmt.Sprintf(`
resource "terraform_data" "k" {
  input = "mine"
}

resource "gpupaas_bare_metal_instance" "b" {
  metadata = { name = "b1", project = %q, workspace = %q }
  spec = {
    type    = "bm-l40s"
    image   = "ubuntu-22"
    ssh_key = terraform_data.k.output
  }
}
`, testProject, bmWorkspace)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy:             checkBMGone(srv),
		Steps:                    []resource.TestStep{{Config: cfg}, {Config: cfg, PlanOnly: true}},
	})
}

func TestAccBareMetal_aWholeSpecKnownOnlyAfterApplyIsAccepted(t *testing.T) {
	srv, _ := newBMMock(t, bmapi.Options{})
	cfg := providerBlock(srv.URL) + fmt.Sprintf(`
resource "terraform_data" "s" {
  input = { type = "bm-l40s", image = "ubuntu-22", user_password = "from-another-resource" }
}

resource "gpupaas_bare_metal_instance" "b" {
  metadata = { name = "b1", project = %q, workspace = %q }
  spec     = terraform_data.s.output
}
`, testProject, bmWorkspace)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy:             checkBMGone(srv),
		Steps:                    []resource.TestStep{{Config: cfg}, {Config: cfg, PlanOnly: true}},
	})
}

func TestAccVM_aCredentialKnownOnlyAfterApplyIsAccepted(t *testing.T) {
	for name, repl := range map[string][2]string{
		"ssh key":  {`ssh_key = "mine"`, `ssh_key = terraform_data.c.output`},
		"password": {`ssh_key = "mine"`, `guest_password = terraform_data.c.output`},
	} {
		t.Run(name, func(t *testing.T) {
			srv, _ := newVMMock(t, vmapi.Options{})
			cfg := strings.Replace(vmConfig(srv.URL, "", ""), repl[0], repl[1], 1) + `
resource "terraform_data" "c" {
  input = "mine"
}
`
			resource.Test(t, resource.TestCase{
				ProtoV6ProviderFactories: protoFactories(),
				CheckDestroy:             checkVMGone(srv),
				Steps:                    []resource.TestStep{{Config: cfg}, {Config: cfg, PlanOnly: true}},
			})
		})
	}
}

// A GPU SKU whose preset (or whose cpu_count) is known only after apply must not be refused for a missing gpu_count.
func TestAccVM_aGPUSizeKnownOnlyAfterApplyIsAccepted(t *testing.T) {
	srv, _ := newVMMock(t, vmapi.Options{})
	cfg := strings.NewReplacer(`type    = "vm-small"`, `type    = "vm-gpu"`, `preset  = "Small"`, `preset  = terraform_data.size.output`).Replace(vmConfig(srv.URL, "", "")) + `
resource "terraform_data" "size" {
  input = "G1"
}
`
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy:             checkVMGone(srv),
		Steps:                    []resource.TestStep{{Config: cfg}, {Config: cfg, PlanOnly: true}},
	})
}

// A timeouts block built from another resource is unknown at plan time and must not break the plan.
func TestAccVM_aTimeoutsBlockKnownOnlyAfterApplyIsAccepted(t *testing.T) {
	srv, _ := newVMMock(t, vmapi.Options{})
	cfg := vmConfig(srv.URL, "", `  timeouts = terraform_data.t.output == "y" ? { create = "30m" } : null`) + `
resource "terraform_data" "t" {
  input = "y"
}
`
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy:             checkVMGone(srv),
		Steps:                    []resource.TestStep{{Config: cfg}, {Config: cfg, PlanOnly: true}},
	})
}

// A sharing list the user wrote as [] is kept as [] (the platform drops it), on a VM and on a volume.
func TestAccVM_anEmptySharingListIsKept(t *testing.T) {
	srv, _ := newVMMock(t, vmapi.Options{})
	cfg := vmConfig(srv.URL, "    sharing = { share_mode = \"ALL_WORKSPACES\", workspaces = [] }", "")
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy:             checkVMGone(srv),
		Steps: []resource.TestStep{
			{Config: cfg, Check: resource.TestCheckResourceAttr(vmAddr, "spec.sharing.workspaces.#", "0")},
			{Config: cfg, PlanOnly: true},
		},
	})
}

func TestAccStorage_anEmptySharingListIsKept(t *testing.T) {
	srv := newStorageMock(t)
	cfg := devConfig(srv.URL, fmt.Sprintf(`
resource "gpupaas_high_speed_storage" "v" {
  metadata = {
    name    = "shared-fs"
    project = %q
  }
  spec = {
    type    = "weka-fs"
    sharing = { share_mode = "ALL_WORKSPACES", workspaces = [] }
  }
}
`, testProject))
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy:             noStorageLeft(srv, "", "shared-fs"),
		Steps: []resource.TestStep{
			{Config: cfg, Check: resource.TestCheckResourceAttr(hssAddr, "spec.sharing.workspaces.#", "0")},
			{Config: cfg, PlanOnly: true},
		},
	})
}

// Writing the same workspaces in another order is not a change the platform applies: the apply must not fail on the
// order the platform returns, and the next plan must be empty.
func TestAccStorage_aReorderedSharingListIsAcceptedAndKeptAsWritten(t *testing.T) {
	srv := newStorageMock(t)
	cfg := func(list string) string {
		return devConfig(srv.URL, fmt.Sprintf(`
resource "gpupaas_high_speed_storage" "v" {
  metadata = {
    name    = "shared-fs"
    project = %q
  }
  spec = {
    type    = "weka-fs"
    sharing = { share_mode = "SPECIFIC_WORKSPACES", workspaces = %s }
  }
}
`, testProject, list))
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy:             noStorageLeft(srv, "", "shared-fs"),
		Steps: []resource.TestStep{
			{Config: cfg(`["a", "b"]`)},
			{
				Config: cfg(`["b", "a"]`),
				Check:  resource.TestCheckResourceAttr(hssAddr, "spec.sharing.workspaces.0", "b"),
			},
			{Config: cfg(`["b", "a"]`), PlanOnly: true},
		},
	})
}

// A provider setting that is only known after apply must be reported, not silently dropped (a partner key would send
// no organization, a self-signed endpoint would be verified).
func TestAccProvider_anUnknownSettingIsReported(t *testing.T) {
	srv, _ := newBMMock(t, bmapi.Options{})
	for _, c := range []struct{ attr, value, want string }{
		{"organization_id", `terraform_data.v.output`, `Unknown organization_id`},
		{"user_agent", `terraform_data.v.output`, `Unknown user_agent`},
		{"insecure", `terraform_data.b.output`, `Unknown insecure`},
	} {
		t.Run(c.attr, func(t *testing.T) {
			cfg := strings.Replace(bmConfig(srv.URL, "", ""), `token    = "test"`, "token    = \"test\"\n  "+c.attr+" = "+c.value, 1) + `
resource "terraform_data" "v" {
  input = "x"
}

resource "terraform_data" "b" {
  input = true
}
`
			resource.Test(t, resource.TestCase{
				ProtoV6ProviderFactories: protoFactories(),
				Steps:                    []resource.TestStep{{Config: cfg, PlanOnly: true, ExpectError: errRe(c.want)}},
			})
		})
	}
}

// The platform may store no description for a bare-metal machine: the configured value is kept, so the plan after the
// apply stays empty and an edit still goes through.
func TestAccBareMetal_aDescriptionThePlatformDoesNotStoreIsKeptAsConfigured(t *testing.T) {
	srv, _ := newBMMock(t, bmapi.Options{DropMetadataText: true})
	with := func(desc string) string {
		return strings.Replace(bmConfig(srv.URL, "", ""), `    workspace = "ws"`, fmt.Sprintf("    workspace = \"ws\"\n    description  = %q", desc), 1)
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy:             checkBMGone(srv),
		Steps: []resource.TestStep{
			{Config: with("one"), Check: resource.TestCheckResourceAttr(bmAddr, "metadata.description", "one")},
			{Config: with("one"), PlanOnly: true},
			{Config: with("two"), Check: resource.TestCheckResourceAttr(bmAddr, "metadata.description", "two")},
		},
	})
}
