package provider_test

import (
	"fmt"
	"strings"
	"testing"

	apiv1 "github.com/gpupaas-ai/gpupaas-go/apis/v1alpha1"
	"github.com/gpupaas-ai/gpupaas-go/mock/clusterapi"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// Per-pool settings of the v3.1.41 API reference (network, boot disk, public IP, password, labels, taints,
// automatic scaling), against a mock that enforces that release's rules (PerPoolNetwork).

// poolsProfile allows both virtual-machine and bare-metal pools.
func poolsProfile() apiv1.MKSClusterProfile {
	p := mksTestProfile()
	p.Spec.ControlPlane = &apiv1.MKSGroupProfile{DefaultSKUName: "bmaas-test-1", DefaultImage: "ubuntu-26", SKUNames: []string{"bmaas-test-1"}, SKUTypes: []string{"BMAAS"}, Images: []string{"ubuntu-26"}}
	p.Spec.Workers = &apiv1.MKSGroupProfile{DefaultSKUName: "vm-small", DefaultImage: "ubuntu-26", SKUNames: []string{"bmaas-test-1", "vm-small"}, SKUTypes: []string{"BMAAS", "VMAAS"}, Images: []string{"ubuntu-26"}}
	return p
}

func newPoolsMock(t *testing.T, opts clusterapi.Options) *clusterapi.Server {
	t.Helper()
	opts.PerPoolNetwork = true
	opts.Profiles = []apiv1.MKSClusterProfile{poolsProfile()}
	srv, _ := newMKSMock(t, opts)
	return srv
}

// poolsConfig renders a cluster whose control plane and pools carry the given extra settings.
func poolsConfig(url, version, cpExtra, pools string) string {
	return poolsConfigWith(url, version, cpExtra, pools, "", "")
}

// poolsConfigWith also takes lines for the spec block (specExtra) and for the resource (extra), and
// anything to put before the resource (before).
func poolsConfigWith(url, version, cpExtra, pools, specExtra, extra string) string {
	spec := specExtra
	if pools != "" {
		spec += "\n    worker_pools = [" + pools + "]"
	}
	return providerBlock(url) + fmt.Sprintf(`
resource "gpupaas_kubernetes_cluster" "c" {
  metadata = {
    name      = "c1"
    project   = %q
    workspace = %q
  }
  spec = {
    profile_name       = "mks-oneclick-1"
    kubernetes_version = %q
    control_plane = {
      node_count = 1
      sku_name   = "bmaas-test-1"
      sku_type   = "BMAAS"
      os_image   = "ubuntu-26"
      ssh_key    = "mine"
%s
    }
%s
  }
%s
}
`, testProject, mksWorkspace, version, cpExtra, spec, extra)
}

const cpNetwork = `      network    = { vpc = "vpc-a", subnet = "sub-0" }`

func gpuPool(extra string) string {
	return `{
      name     = "gpu"
      node_count = 2
      sku_name = "vm-small"
      sku_type = "VMAAS"
      os_image = "ubuntu-26"
      network  = { vpc = "vpc-a", subnet = "sub-1", security_group = "sg-1" }
` + extra + `
    }`
}

const gpuPoolSettings = `      boot_disk_size_gb = 60
      assign_public_ip  = false
      user_password     = "s3cret-pw"
      labels            = { team = "ml" }
      taints            = [{ key = "nvidia.com/gpu", value = "true", effect = "NoSchedule" }]`

func TestAccMKSCluster_perPoolSettings(t *testing.T) {
	// The mock behaves like the live platform: it adds a label and a taint of its own, reorders taints and
	// omits assignPublicIp when false. None of that may show as a change.
	srv := newPoolsMock(t, clusterapi.Options{PlatformAdditions: true})
	var posts int
	countPosts := func(_ *terraform.State) error { posts = countMKS(srv, "POST "); return nil }
	same := func(_ *terraform.State) error {
		if n := countMKS(srv, "POST "); n != posts {
			return fmt.Errorf("an unchanged configuration was sent to the API again (%d POSTs, was %d)", n, posts)
		}
		return nil
	}
	cfg := poolsConfig(srv.URL, "1.35", cpNetwork, gpuPool(gpuPoolSettings))
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy:             checkMKSGone(srv),
		Steps: []resource.TestStep{
			{
				Config: cfg,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(mksAddr, "spec.control_plane.network.subnet", "sub-0"),
					resource.TestCheckResourceAttr(mksAddr, "spec.worker_pools.0.network.vpc", "vpc-a"),
					resource.TestCheckResourceAttr(mksAddr, "spec.worker_pools.0.network.security_group", "sg-1"),
					resource.TestCheckResourceAttr(mksAddr, "spec.worker_pools.0.boot_disk_size_gb", "60"),
					resource.TestCheckResourceAttr(mksAddr, "spec.worker_pools.0.assign_public_ip", "false"),
					resource.TestCheckResourceAttr(mksAddr, "spec.worker_pools.0.labels.team", "ml"),
					resource.TestCheckResourceAttr(mksAddr, "spec.worker_pools.0.taints.0.effect", "NoSchedule"),
					// The password is kept as configured although the API only shows "******".
					resource.TestCheckResourceAttr(mksAddr, "spec.worker_pools.0.user_password", "s3cret-pw"),
					// The cluster-level network is not used on this release: the pool network is.
					resource.TestCheckResourceAttr(mksAddr, "status.available_kubernetes_versions.0", "1.36"),
					checkCluster(srv, func(c *apiv1.MKSCluster) error {
						w := c.Spec.WorkerPools[0]
						if w.Network == nil || w.Network.Subnet != "sub-1" || w.BootDiskSizeGb != 60 || w.AssignPublicIP != nil && *w.AssignPublicIP {
							return fmt.Errorf("worker pool on the API: %+v", w)
						}
						if got := srv.PoolPassword(testProject, mksWorkspace, "c1", "gpu"); got != "s3cret-pw" {
							return fmt.Errorf("the password did not reach the API")
						}
						return nil
					}),
					countPosts,
				),
			},
			{
				// Nothing changed: the plan is empty, so no write and no redeploy.
				Config: cfg,
				Check:  resource.ComposeAggregateTestCheckFunc(same, resource.TestCheckResourceAttr(mksAddr, "spec.worker_pools.0.user_password", "s3cret-pw")),
			},
			{
				// A change that is only Terraform's (a timeout) runs Update, which must not send the cluster
				// again although the platform shows the password masked and holds labels and taints of its own.
				Config: poolsConfigWith(srv.URL, "1.35", cpNetwork, gpuPool(gpuPoolSettings), "", `  timeouts = { update = "30m" }`),
				Check: resource.ComposeAggregateTestCheckFunc(
					same,
					resource.TestCheckResourceAttr(mksAddr, "spec.worker_pools.0.labels.%", "1"),
					resource.TestCheckResourceAttr(mksAddr, "spec.worker_pools.0.taints.#", "1"),
					resource.TestCheckResourceAttr(mksAddr, "spec.worker_pools.0.user_password", "s3cret-pw"),
				),
			},
			{
				// Labels, taints and the size change in place; the password stays what it was.
				Config: poolsConfig(srv.URL, "1.35", cpNetwork, strings.NewReplacer(
					`labels            = { team = "ml" }`, `labels            = { team = "ml", tier = "gpu" }`,
					`effect = "NoSchedule"`, `effect = "NoExecute"`,
					"node_count = 2", "node_count = 3").Replace(gpuPool(gpuPoolSettings))),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(mksAddr, "spec.worker_pools.0.labels.tier", "gpu"),
					resource.TestCheckResourceAttr(mksAddr, "spec.worker_pools.0.taints.0.effect", "NoExecute"),
					resource.TestCheckResourceAttr(mksAddr, "spec.worker_pools.0.node_count", "3"),
					checkCluster(srv, func(c *apiv1.MKSCluster) error {
						if got := srv.PoolPassword(testProject, mksWorkspace, "c1", "gpu"); got != "s3cret-pw" {
							return fmt.Errorf("an update changed the stored password to %q", got)
						}
						return nil
					}),
					countPosts,
				),
			},
			{
				// A new password is sent.
				Config: poolsConfig(srv.URL, "1.35", cpNetwork, strings.NewReplacer(
					`labels            = { team = "ml" }`, `labels            = { team = "ml", tier = "gpu" }`,
					`effect = "NoSchedule"`, `effect = "NoExecute"`,
					"node_count = 2", "node_count = 3",
					`"s3cret-pw"`, `"another-pw"`).Replace(gpuPool(gpuPoolSettings))),
				Check: checkCluster(srv, func(*apiv1.MKSCluster) error {
					if got := srv.PoolPassword(testProject, mksWorkspace, "c1", "gpu"); got != "another-pw" {
						return fmt.Errorf("the new password did not reach the API")
					}
					return nil
				}),
			},
			{
				// Removing the labels and the taints clears them on the pool.
				Config: poolsConfig(srv.URL, "1.35", cpNetwork, strings.NewReplacer(
					`labels            = { team = "ml" }`, "",
					`taints            = [{ key = "nvidia.com/gpu", value = "true", effect = "NoSchedule" }]`, "",
					"node_count = 2", "node_count = 3",
					`"s3cret-pw"`, `"another-pw"`).Replace(gpuPool(gpuPoolSettings))),
				Check: checkCluster(srv, func(c *apiv1.MKSCluster) error {
					// Ours are gone; the platform's own label and taint stay.
					w := c.Spec.WorkerPools[0]
					if len(w.Labels) != 1 || w.Labels["platform/owned"] != "yes" || len(w.Taints) != 1 {
						return fmt.Errorf("labels %v taints %v: ours were not cleared and the platform's kept", w.Labels, w.Taints)
					}
					return nil
				}),
			},
			{
				// A pool's network is fixed: the plan says so before anything is sent.
				Config:      poolsConfig(srv.URL, "1.35", cpNetwork, strings.Replace(gpuPool(gpuPoolSettings), `subnet = "sub-1"`, `subnet = "sub-2"`, 1)),
				PlanOnly:    true,
				ExpectError: errRe("network of worker pool"),
			},
			{
				Config:      poolsConfig(srv.URL, "1.35", cpNetwork, strings.Replace(gpuPool(gpuPoolSettings), "boot_disk_size_gb = 60", "boot_disk_size_gb = 80", 1)),
				PlanOnly:    true,
				ExpectError: errRe("boot disk size of worker pool"),
			},
			{
				// An import takes the pool settings from the platform; it never knows the password.
				ResourceName:      mksAddr,
				ImportState:       true,
				ImportStateId:     "system-catalog/ws/c1",
				ImportStateVerify: false,
				ImportStateCheck: func(states []*terraform.InstanceState) error {
					a := states[0].Attributes
					for k, v := range map[string]string{
						"spec.worker_pools.0.network.subnet":    "sub-1",
						"spec.worker_pools.0.boot_disk_size_gb": "60",
						"spec.control_plane.network.vpc":        "vpc-a",
					} {
						if a[k] != v {
							return fmt.Errorf("imported %s = %q, want %q", k, a[k], v)
						}
					}
					if _, ok := a["spec.worker_pools.0.user_password"]; ok {
						return fmt.Errorf("the import invented a password")
					}
					return nil
				},
			},
		},
	})
}

// Controllers that take the network per pool refuse a pool without one: the API error names the pool.
func TestAccMKSCluster_poolWithoutNetworkIsRefusedByAStrictController(t *testing.T) {
	srv := newPoolsMock(t, clusterapi.Options{})
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		Steps: []resource.TestStep{{
			Config:      poolsConfig(srv.URL, "1.35", "", ""),
			ExpectError: errRe("Select a VPC and a subnet for this pool"),
		}},
	})
}

func TestAccMKSCluster_publicIPMustMatchAcrossWorkerPools(t *testing.T) {
	srv := newPoolsMock(t, clusterapi.Options{})
	other := strings.NewReplacer(`name     = "gpu"`, `name     = "cpu"`, "assign_public_ip  = false", "assign_public_ip  = true").Replace(gpuPool(gpuPoolSettings))
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		Steps: []resource.TestStep{{
			Config:      poolsConfig(srv.URL, "1.35", cpNetwork, gpuPool(gpuPoolSettings)+", "+other),
			PlanOnly:    true,
			ExpectError: errRe("every worker pool must carry the same value"),
		}},
	})
	if n := countMKS(srv, "POST "); n != 0 {
		t.Fatalf("validation created clusters: %v", srv.Requests())
	}
}

func TestAccMKSCluster_automaticScalingPool(t *testing.T) {
	srv := newPoolsMock(t, clusterapi.Options{})
	auto := func(min, max, start int) string {
		return fmt.Sprintf(`{
      name         = "auto"
      node_count   = %d
      scaling_mode = "automatic"
      min_nodes    = %d
      max_nodes    = %d
      sku_name     = "bmaas-test-1"
      sku_type     = "BMAAS"
      os_image     = "ubuntu-26"
      network      = { vpc = "vpc-a", subnet = "sub-1" }
    }`, start, min, max)
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy:             checkMKSGone(srv),
		Steps: []resource.TestStep{
			{
				Config: poolsConfig(srv.URL, "1.35", cpNetwork, auto(1, 4, 2)),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(mksAddr, "spec.worker_pools.0.scaling_mode", "automatic"),
					resource.TestCheckResourceAttr(mksAddr, "spec.worker_pools.0.min_nodes", "1"),
					resource.TestCheckResourceAttr(mksAddr, "spec.worker_pools.0.max_nodes", "4"),
					resource.TestCheckResourceAttr(mksAddr, "spec.worker_pools.0.node_count", "2"),
					resource.TestCheckResourceAttr(mksAddr, "status.node_count", "3"),
					checkCluster(srv, func(c *apiv1.MKSCluster) error {
						sc := c.Spec.WorkerPools[0].Scaling
						if sc == nil || sc.Mode != "automatic" || sc.Min != 1 || sc.Max != 4 || sc.Desired != 2 {
							return fmt.Errorf("scaling on the API: %+v", sc)
						}
						return nil
					}),
				),
			},
			{
				// Wider bounds: an in-place update.
				Config: poolsConfig(srv.URL, "1.35", cpNetwork, auto(1, 6, 2)),
				Check:  resource.TestCheckResourceAttr(mksAddr, "spec.worker_pools.0.max_nodes", "6"),
			},
			{
				Config:      poolsConfig(srv.URL, "1.35", cpNetwork, auto(3, 2, 2)),
				PlanOnly:    true,
				ExpectError: errRe("larger than max_nodes"),
			},
			{
				Config:      poolsConfig(srv.URL, "1.35", cpNetwork, strings.Replace(auto(1, 6, 2), `scaling_mode = "automatic"`, `scaling_mode = "manual"`, 1)),
				PlanOnly:    true,
				ExpectError: errRe("min_nodes and max_nodes need automatic scaling"),
			},
		},
	})
}

// A controller that places nodes per pool clears the cluster-level VPC and subnet. A configuration that
// still sets them must create, stay stable and never be sent again for that reason.
func TestAccMKSCluster_clusterLevelNetworkTheControllerClears(t *testing.T) {
	srv := newPoolsMock(t, clusterapi.Options{PlatformAdditions: true})
	var posts int
	cfg := func(extra string) string {
		return poolsConfigWith(srv.URL, "1.35", cpNetwork, gpuPool(""), `    networking = { vpc = "legacy-vpc", subnet = "legacy-subnet" }`, extra)
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy:             checkMKSGone(srv),
		Steps: []resource.TestStep{
			{
				Config: cfg(""),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(mksAddr, "spec.networking.vpc", "legacy-vpc"),
					resource.TestCheckResourceAttr(mksAddr, "spec.networking.subnet", "legacy-subnet"),
					checkCluster(srv, func(c *apiv1.MKSCluster) error {
						if c.Spec.Networking.VPC != "" {
							return fmt.Errorf("the mock should have cleared the cluster-level VPC")
						}
						return nil
					}),
					func(*terraform.State) error { posts = countMKS(srv, "POST "); return nil },
				),
			},
			{
				Config: cfg(`  timeouts = { update = "30m" }`),
				Check: func(*terraform.State) error {
					if n := countMKS(srv, "POST "); n != posts {
						return fmt.Errorf("a cluster-level network the controller clears made the cluster be sent again (%d POSTs, was %d)", n, posts)
					}
					return nil
				},
			},
		},
	})
}

// Values that are only known after apply must not be rejected by the plan-time checks.
func TestAccMKSCluster_valuesKnownOnlyAfterApplyPassThePlanChecks(t *testing.T) {
	srv := newPoolsMock(t, clusterapi.Options{})
	cfg := poolsConfigWith(srv.URL, "1.35", cpNetwork, `{
      name         = "auto"
      node_count   = 2
      scaling_mode = "automatic"
      min_nodes    = tonumber(terraform_data.min.output)
      max_nodes    = tonumber(terraform_data.max.output)
      sku_name     = "bmaas-test-1"
      sku_type     = "BMAAS"
      os_image     = "ubuntu-26"
      network      = { vpc = "vpc-a", subnet = terraform_data.subnet.output }
      taints       = [{ key = terraform_data.key.output, value = "v", effect = "NoSchedule" }]
    }`, "", "") + `
resource "terraform_data" "subnet" { input = "sub-1" }
resource "terraform_data" "key" { input = "dedicated" }
resource "terraform_data" "min" { input = 1 }
resource "terraform_data" "max" { input = 4 }
`
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy:             checkMKSGone(srv),
		Steps: []resource.TestStep{{
			Config: cfg,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr(mksAddr, "spec.worker_pools.0.network.subnet", "sub-1"),
				resource.TestCheckResourceAttr(mksAddr, "spec.worker_pools.0.min_nodes", "1"),
				resource.TestCheckResourceAttr(mksAddr, "spec.worker_pools.0.taints.0.key", "dedicated"),
			),
		}},
	})
}
