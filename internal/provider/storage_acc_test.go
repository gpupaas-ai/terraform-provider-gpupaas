package provider_test

import (
	"fmt"
	"testing"
	"time"

	apiv1 "github.com/gpupaas-ai/gpupaas-go/apis/v1alpha1"
	"github.com/gpupaas-ai/gpupaas-go/mock/devapi"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/gpupaas-ai/terraform-provider-gpupaas/internal/provider/resources"
)

// These tests run the real terraform binary against the provider and the in-process mock of the storage API
// (dev.envmgmt.io/v1 Storage). The mock provisions asynchronously and can fail, park a delete or ignore a resize,
// the way live controllers do for VMs and bare metal; what a live controller does for storage is still to be
// confirmed on a testbed with storage profiles. Run them with TF_ACC=1.

const (
	hssAddr = "gpupaas_high_speed_storage.v"
	objAddr = "gpupaas_object_storage.o"
)

func newStorageMock(t *testing.T) *devapi.Server {
	t.Helper()
	restore := resources.SetPollIntervalForTesting(10 * time.Millisecond)
	t.Cleanup(restore)
	srv := newDevMock(t)
	srv.SeedStorageProfile("weka-fs", apiv1.StorageServiceTypeHighSpeed)
	srv.SeedStorageProfile("s3-small", apiv1.StorageServiceTypeObject)
	return srv
}

func noStorageLeft(srv *devapi.Server, workspace, name string) resource.TestCheckFunc {
	return func(_ *terraform.State) error {
		if srv.Storage(testProject, workspace, name) != nil {
			return fmt.Errorf("storage %q still exists", name)
		}
		return nil
	}
}

func hssConfig(url, size, datacenter string) string {
	return devConfig(url, fmt.Sprintf(`
resource "gpupaas_high_speed_storage" "v" {
  metadata = {
    name      = "shared-fs"
    project   = %q
    workspace = "team-a"
  }
  spec = {
    type                         = "weka-fs"
    size                         = %q
    datacenter                   = %q
    enable_encryption_at_rest    = true
    enable_encryption_in_transit = false
  }
}
`, testProject, size, datacenter))
}

func TestAccHighSpeedStorage_lifecycleResizeReplaceAndImport(t *testing.T) {
	srv := newStorageMock(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy:             noStorageLeft(srv, "team-a", "shared-fs"),
		Steps: []resource.TestStep{
			{
				Config: hssConfig(srv.URL, "50", "dc1"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(hssAddr, "id", "system-catalog/team-a/shared-fs"),
					resource.TestCheckResourceAttr(hssAddr, "spec.type", "weka-fs"),
					resource.TestCheckResourceAttr(hssAddr, "spec.size", "50"),
					resource.TestCheckResourceAttr(hssAddr, "spec.enable_encryption_at_rest", "true"),
					resource.TestCheckResourceAttr(hssAddr, "spec.enable_encryption_in_transit", "false"),
					resource.TestCheckResourceAttr(hssAddr, "status.status", "success"),
					resource.TestCheckResourceAttr(hssAddr, "status.action", "deploy"),
					resource.TestCheckResourceAttr(hssAddr, "status.output.volume_size", "50000000000"),
					resource.TestCheckResourceAttrSet(hssAddr, "status.output.filesystem_servers"),
					func(_ *terraform.State) error {
						v := srv.Storage(testProject, "team-a", "shared-fs")
						if v == nil || v.Spec.Type != "weka-fs" || v.Spec.Size != "50" || v.Spec.EnableEncryptionAtRest != "true" || v.Spec.EnableEncryptionInTransit != "false" {
							return fmt.Errorf("stored volume = %+v", v)
						}
						return nil
					},
				),
			},
			{
				// nothing changes on the next plan: the fields the platform fills in do not drift
				Config:   hssConfig(srv.URL, "50", "dc1"),
				PlanOnly: true,
			},
			{
				// a resize is applied in place, not by replacing the volume
				Config: hssConfig(srv.URL, "80", "dc1"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(hssAddr, plancheck.ResourceActionUpdate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(hssAddr, "spec.size", "80"),
					resource.TestCheckResourceAttr(hssAddr, "status.action", "update"),
					resource.TestCheckResourceAttr(hssAddr, "status.output.volume_size", "80000000000"),
				),
			},
			{
				ResourceName:            hssAddr,
				ImportState:             true,
				ImportStateId:           "system-catalog/team-a/shared-fs",
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"timeouts"},
			},
			{
				// the datacenter is fixed at creation: changing it replaces the volume
				Config: hssConfig(srv.URL, "80", "dc2"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(hssAddr, plancheck.ResourceActionDestroyBeforeCreate)},
				},
				Check: resource.TestCheckResourceAttr(hssAddr, "spec.datacenter", "dc2"),
			},
		},
	})
}

func TestAccObjectStorage_projectScopeAndDataSource(t *testing.T) {
	srv := newStorageMock(t)
	cfg := devConfig(srv.URL, fmt.Sprintf(`
resource "gpupaas_object_storage" "o" {
  metadata = {
    name    = "backups"
    project = %q
  }
  spec = {
    type = "s3-small"
    size = "10"
  }
}

data "gpupaas_object_storage" "o" {
  name    = gpupaas_object_storage.o.metadata.name
  project = gpupaas_object_storage.o.metadata.project
}
`, testProject))
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy:             noStorageLeft(srv, "", "backups"),
		Steps: []resource.TestStep{
			{
				Config: cfg,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(objAddr, "id", "system-catalog/backups"),
					resource.TestCheckResourceAttr(objAddr, "status.status", "success"),
					resource.TestCheckResourceAttrSet(objAddr, "status.output.bucket_name"),
					resource.TestCheckResourceAttr("data.gpupaas_object_storage.o", "spec.type", "s3-small"),
					resource.TestCheckResourceAttr("data.gpupaas_object_storage.o", "spec.size", "10"),
					resource.TestCheckResourceAttr("data.gpupaas_object_storage.o", "status.status", "success"),
					resource.TestCheckResourceAttrSet("data.gpupaas_object_storage.o", "status.output.secret_access_key"),
				),
			},
			{Config: cfg, PlanOnly: true},
		},
	})
}

func TestAccStorage_aProfileOfTheOtherFamilyIsRejectedAndTheVolumeStaysTracked(t *testing.T) {
	srv := newStorageMock(t)
	cfg := devConfig(srv.URL, fmt.Sprintf(`
resource "gpupaas_high_speed_storage" "v" {
  metadata = {
    name    = "wrong"
    project = %q
  }
  spec = {
    type = "s3-small"
    size = "5"
  }
}
`, testProject))
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy:             noStorageLeft(srv, "", "wrong"),
		Steps: []resource.TestStep{
			{
				Config:      cfg,
				ExpectError: errRe("declare it with gpupaas_object_storage instead of gpupaas_high_speed_storage"),
			},
		},
	})
}

func TestAccStorage_theDataSourceOfTheOtherFamilyFails(t *testing.T) {
	srv := newStorageMock(t)
	cfg := devConfig(srv.URL, fmt.Sprintf(`
resource "gpupaas_object_storage" "o" {
  metadata = {
    name    = "backups"
    project = %q
  }
  spec = {
    type = "s3-small"
  }
}

data "gpupaas_high_speed_storage" "wrong" {
  name       = "backups"
  project    = %q
  depends_on = [gpupaas_object_storage.o]
}
`, testProject, testProject))
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy:             noStorageLeft(srv, "", "backups"),
		Steps: []resource.TestStep{
			{Config: cfg, ExpectError: errRe("declare it with gpupaas_object_storage")},
		},
	})
}

func TestAccStorage_provisioningFailureKeepsATaintedVolumeThatDestroyRemoves(t *testing.T) {
	srv := newStorageMock(t)
	srv.FailStorage("shared-fs")
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy:             noStorageLeft(srv, "team-a", "shared-fs"),
		Steps: []resource.TestStep{
			{
				Config:      hssConfig(srv.URL, "50", "dc1"),
				ExpectError: errRe(`Status "failed" after "deploy": Couldn't provision storage shared-fs; provisioning is temporarily unavailable`),
			},
		},
	})
}

func TestAccStorage_aResizeThePlatformDoesNotApplyFailsAndTheStateShowsTheKeptSize(t *testing.T) {
	srv := newStorageMock(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy:             noStorageLeft(srv, "team-a", "shared-fs"),
		Steps: []resource.TestStep{
			{Config: hssConfig(srv.URL, "50", "dc1")},
			{
				PreConfig:   srv.IgnoreStorageSizeChange,
				Config:      hssConfig(srv.URL, "80", "dc1"),
				ExpectError: errRe(`size is "50", not the requested "80"`),
			},
		},
	})
}

func TestAccStorage_deletedOutsideTerraformIsPlannedAgain(t *testing.T) {
	srv := newStorageMock(t)
	cfg := hssConfig(srv.URL, "50", "dc1")
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy:             noStorageLeft(srv, "team-a", "shared-fs"),
		Steps: []resource.TestStep{
			{Config: cfg},
			{
				PreConfig:          func() { srv.DeleteStorage(testProject, "team-a", "shared-fs") },
				Config:             cfg,
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
		},
	})
}

func TestAccStorage_aDeleteParkedForAnAdministratorIsReportedAndKeptInState(t *testing.T) {
	srv := newStorageMock(t)
	cfg := hssConfig(srv.URL, "50", "dc1")
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy:             noStorageLeft(srv, "team-a", "shared-fs"),
		Steps: []resource.TestStep{
			{Config: cfg},
			{
				PreConfig:   srv.ParkStorageDeletes,
				Config:      cfg,
				Destroy:     true,
				ExpectError: errRe(`Status "delete_pending" after "destroy"`),
			},
			{
				// an administrator removes it; the final destroy then finds nothing left
				PreConfig:          func() { srv.DeleteStorage(testProject, "team-a", "shared-fs") },
				Config:             cfg,
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
		},
	})
}

func TestAccStorage_aVolumeThatAlreadyExistsIsNeverAdoptedOrDeleted(t *testing.T) {
	srv := newStorageMock(t)
	srv.SeedStorage(testProject, "team-a", "shared-fs", "weka-fs", "10") // somebody else's volume with the name we want
	stillThere := func(_ *terraform.State) error {
		if v := srv.Storage(testProject, "team-a", "shared-fs"); v == nil || v.Spec.Size != "10" {
			return fmt.Errorf("the pre-existing volume was changed or deleted: %+v", v)
		}
		return nil
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy:             stillThere,
		Steps: []resource.TestStep{
			{
				Config:      hssConfig(srv.URL, "50", "dc1"),
				ExpectError: errRe(`A volume named "shared-fs" already exists`),
			},
			{
				// the check of the name fails during an outage: no volume is created and none is adopted
				PreConfig:   func() { srv.FailStorageReads(1) },
				Config:      hssConfig(srv.URL, "50", "dc1"),
				ExpectError: errRe("Check the volume name failed: server error"),
			},
		},
	})
}

func TestAccStorage_anExplicitEmptyTimeoutsBlockIsKept(t *testing.T) {
	srv := newStorageMock(t)
	cfg := devConfig(srv.URL, fmt.Sprintf(`
resource "gpupaas_high_speed_storage" "v" {
  metadata = {
    name    = "shared-fs"
    project = %q
  }
  spec = {
    type = "weka-fs"
  }
  timeouts = {}
}
`, testProject))
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy:             noStorageLeft(srv, "", "shared-fs"),
		Steps: []resource.TestStep{
			{Config: cfg},
			{Config: cfg, PlanOnly: true},
		},
	})
}

// Observed live: the first delete of a failed volume ends failed on destroy ("Please try again"), the next one works.
// Terraform keeps the volume in state after the failed teardown, so a second destroy finishes the job.
func TestAccStorage_aTeardownThatFailsOnceIsRetryableByDestroyingAgain(t *testing.T) {
	srv := newStorageMock(t)
	cfg := hssConfig(srv.URL, "50", "dc1")
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy:             noStorageLeft(srv, "team-a", "shared-fs"),
		Steps: []resource.TestStep{
			{Config: cfg},
			{
				PreConfig:   func() { srv.FailNextStorageDelete("shared-fs") },
				Config:      cfg,
				Destroy:     true,
				ExpectError: errRe(`Status "failed" after "destroy": Couldn't delete storage shared-fs. Please try again`),
			},
			{
				// the volume is still tracked, so destroying again removes it
				Config:  cfg,
				Destroy: true,
			},
		},
	})
}
