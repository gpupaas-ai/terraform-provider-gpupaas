package provider_test

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	gpupaas "github.com/gpupaas-ai/gpupaas-go"
	apiv1 "github.com/gpupaas-ai/gpupaas-go/apis/v1alpha1"
	"github.com/gpupaas-ai/gpupaas-go/clientset"
	"github.com/gpupaas-ai/gpupaas-go/mock/clusterapi"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/gpupaas-ai/terraform-provider-gpupaas/internal/provider/resources"
)

// These tests run the real terraform binary against the provider and an in-process mock of
// the first-class MKS API (paas.k8smgmt.io/v3). Run them with TF_ACC=1.

const (
	mksAddr      = "gpupaas_kubernetes_cluster.c"
	mksWorkspace = "ws"
)

func mksTestProfile() apiv1.MKSClusterProfile {
	return apiv1.MKSClusterProfile{
		Metadata: apiv1.ObjectMeta{Name: "mks-oneclick-1"},
		Spec: apiv1.MKSClusterProfileSpec{
			ControlPlane:                &apiv1.MKSGroupProfile{DefaultSKUName: "bmaas-test-1", DefaultImage: "ubuntu-26", SKUNames: []string{"bmaas-test-1"}, SKUTypes: []string{"BMAAS"}, Images: []string{"ubuntu-26"}, ComputeTypes: []string{"CPU"}},
			Workers:                     &apiv1.MKSGroupProfile{DefaultSKUName: "bmaas-test-1", DefaultImage: "ubuntu-26", SKUNames: []string{"bmaas-test-1"}, SKUTypes: []string{"BMAAS"}, Images: []string{"ubuntu-26"}, ComputeTypes: []string{"CPU"}},
			Datacenters:                 []string{"default-dc"},
			SupportedCNIs:               []apiv1.MKSCNI{{Name: "Calico", Version: "3.32.2"}},
			DefaultCNI:                  &apiv1.MKSCNI{Name: "Calico", Version: "3.32.2"},
			DefaultBlueprint:            &apiv1.MKSBlueprint{Name: "minimal", Version: "latest"},
			DefaultKubernetesVersion:    "1.36",
			SupportedKubernetesVersions: []string{"1.36", "1.35"},
			DefaultPodSubnet:            "10.244.0.0/16",
			DefaultServiceSubnet:        "10.96.0.0/12",
			DefaultSSHKey:               "mine",
			Version:                     "v3.1.41",
		},
	}
}

func newMKSMock(t *testing.T, opts clusterapi.Options) (*clusterapi.Server, clientset.Interface) {
	t.Helper()
	restore := resources.SetPollIntervalForTesting(10 * time.Millisecond)
	t.Cleanup(restore)
	if len(opts.Profiles) == 0 {
		opts.Profiles = []apiv1.MKSClusterProfile{mksTestProfile()}
	}
	if opts.SSHKeys == nil {
		opts.SSHKeys = []string{"mine"} // the key every test config uses exists in the workspace
	}
	if opts.Step == 0 {
		opts.Step = 15 * time.Millisecond
	}
	srv := clusterapi.New(opts)
	t.Cleanup(srv.Close)
	cs, err := clientset.NewForConfig(gpupaas.NewConfig(srv.URL, "test"))
	if err != nil {
		t.Fatal(err)
	}
	return srv, cs
}

// clusterConfig renders a workspace-scoped cluster with one control-plane node. specExtra and
// extra are inserted inside spec and at the top level of the resource.
func clusterConfig(url, version, specExtra, extra string) string {
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
    }
%s
  }
%s
}
`, testProject, mksWorkspace, version, specExtra, extra)
}

func pool(name string, count int) string {
	return fmt.Sprintf(`{ name = %q, node_count = %d, sku_name = "bmaas-test-1", sku_type = "BMAAS", os_image = "ubuntu-26" }`, name, count)
}

func checkCluster(srv *clusterapi.Server, check func(*apiv1.MKSCluster) error) resource.TestCheckFunc {
	return func(_ *terraform.State) error {
		c := srv.Cluster(testProject, mksWorkspace, "c1")
		if c == nil {
			return fmt.Errorf("cluster c1 does not exist on the API")
		}
		return check(c)
	}
}

func checkMKSGone(srv *clusterapi.Server) resource.TestCheckFunc {
	return func(_ *terraform.State) error {
		if srv.Exists(testProject, mksWorkspace, "c1") {
			c := srv.Cluster(testProject, mksWorkspace, "c1")
			return fmt.Errorf("cluster c1 still exists (phase %s)", c.Status.Phase())
		}
		return nil
	}
}

func countMKS(srv *clusterapi.Server, prefix string) int {
	n := 0
	for _, r := range srv.Requests() {
		if strings.HasPrefix(r, prefix) {
			n++
		}
	}
	return n
}

func TestAccMKSCluster_lifecycle(t *testing.T) {
	srv, _ := newMKSMock(t, clusterapi.Options{Flap: true, StaleReads: 2})
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy:             checkMKSGone(srv),
		Steps: []resource.TestStep{
			{
				// Create provisions the cluster and waits until it runs.
				Config: clusterConfig(srv.URL, "1.36", "", ""),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(mksAddr, "id", "system-catalog/ws/c1"),
					resource.TestCheckResourceAttr(mksAddr, "status.phase", "running"),
					resource.TestCheckResourceAttr(mksAddr, "status.status", "MKS_CLUSTER_STATUS_RUNNING"),
					resource.TestCheckResourceAttr(mksAddr, "status.cluster_id", "c1"),
					// The configured short version is kept; the exact one the cluster runs is reported.
					resource.TestCheckResourceAttr(mksAddr, "spec.kubernetes_version", "1.36"),
					resource.TestCheckResourceAttr(mksAddr, "status.kubernetes_version", "v1.36.4"),
					// Defaults the platform fills in come back as computed values.
					resource.TestCheckResourceAttr(mksAddr, "spec.location", "default-dc"),
					resource.TestCheckResourceAttr(mksAddr, "spec.networking.cni_name", "Calico"),
					resource.TestCheckResourceAttr(mksAddr, "spec.networking.pod_subnet", "10.244.0.0/16"),
					resource.TestCheckResourceAttr(mksAddr, "spec.blueprint.name", "minimal"),
					resource.TestCheckResourceAttr(mksAddr, "spec.contract_term", "0"),
					resource.TestCheckResourceAttr(mksAddr, "spec.control_plane.name", "control-plane"),
					resource.TestCheckResourceAttr(mksAddr, "spec.control_plane.compute_type", "GPU"),
					resource.TestCheckResourceAttr(mksAddr, "spec.control_plane.node_count", "1"),
					resource.TestCheckResourceAttr(mksAddr, "status.node_count", "1"),
					resource.TestCheckResourceAttr(mksAddr, "status.nodes.0.node_pool", "master"),
					checkCluster(srv, func(c *apiv1.MKSCluster) error {
						if c.Spec.Profile == nil || c.Spec.Profile.Name != "mks-oneclick-1" {
							return fmt.Errorf("profile = %#v", c.Spec.Profile)
						}
						return nil
					}),
				),
			},
			{
				// Add a worker pool: an update, not a new cluster.
				Config: clusterConfig(srv.URL, "1.36", "    worker_pools = ["+pool("pool-a", 2)+"]", ""),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(mksAddr, "spec.worker_pools.#", "1"),
					resource.TestCheckResourceAttr(mksAddr, "spec.worker_pools.0.name", "pool-a"),
					resource.TestCheckResourceAttr(mksAddr, "spec.worker_pools.0.node_count", "2"),
					resource.TestCheckResourceAttr(mksAddr, "status.node_count", "3"),
					resource.TestCheckResourceAttr(mksAddr, "status.phase", "running"),
					func(_ *terraform.State) error {
						if n := countMKS(srv, "DELETE "); n != 0 {
							return fmt.Errorf("adding a pool deleted the cluster (%d deletes)", n)
						}
						return nil
					},
				),
			},
			{
				// Resize the pool and tag the cluster.
				Config: clusterConfig(srv.URL, "1.36", "    worker_pools = ["+pool("pool-a", 1)+"]\n    tags = { team = \"ml\" }", ""),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(mksAddr, "spec.worker_pools.0.node_count", "1"),
					resource.TestCheckResourceAttr(mksAddr, "spec.tags.team", "ml"),
					resource.TestCheckResourceAttr(mksAddr, "status.node_count", "2"),
				),
			},
			{
				// Upgrade by changing the version.
				Config: clusterConfig(srv.URL, "1.35", "    worker_pools = ["+pool("pool-a", 1)+"]\n    tags = { team = \"ml\" }", ""),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(mksAddr, "spec.kubernetes_version", "1.35"),
					resource.TestMatchResourceAttr(mksAddr, "status.kubernetes_version", regexp.MustCompile(`^v1\.35\.`)),
					checkCluster(srv, func(c *apiv1.MKSCluster) error {
						if !strings.HasPrefix(c.Spec.KubernetesVersion, "v1.35") {
							return fmt.Errorf("API version = %s", c.Spec.KubernetesVersion)
						}
						return nil
					}),
				),
			},
			{
				// Remove the pool and the tag again.
				Config: clusterConfig(srv.URL, "1.35", "", ""),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(mksAddr, "spec.worker_pools.#", "0"),
					resource.TestCheckNoResourceAttr(mksAddr, "spec.tags.team"),
					resource.TestCheckResourceAttr(mksAddr, "status.node_count", "1"),
					checkCluster(srv, func(c *apiv1.MKSCluster) error {
						if len(c.Spec.WorkerPools) != 0 || len(c.Spec.Tags) != 0 {
							return fmt.Errorf("pools=%d tags=%v", len(c.Spec.WorkerPools), c.Spec.Tags)
						}
						return nil
					}),
				),
			},
			{
				// An existing cluster can be imported and then matches the configuration.
				ResourceName:      mksAddr,
				ImportState:       true,
				ImportStateId:     "system-catalog/ws/c1",
				ImportStateVerify: false,
				ImportStateCheck: func(states []*terraform.InstanceState) error {
					if len(states) != 1 {
						return fmt.Errorf("imported %d resources", len(states))
					}
					a := states[0].Attributes
					want := map[string]string{
						"spec.profile_name":               "mks-oneclick-1",
						"spec.control_plane.sku_name":     "bmaas-test-1",
						"spec.control_plane.os_image":     "ubuntu-26",
						"spec.control_plane.node_count":   "1",
						"spec.location":                   "default-dc",
						"status.phase":                    "running",
						"metadata.workspace":              "ws",
						"spec.networking.cni_version":     "3.32.2",
						"spec.control_plane.compute_type": "GPU",
					}
					for k, v := range want {
						if a[k] != v {
							return fmt.Errorf("imported %s = %q, want %q", k, a[k], v)
						}
					}
					return nil
				},
			},
		},
	})
}

func TestAccMKSCluster_clientSideSettingsDoNotReapplyTheCluster(t *testing.T) {
	srv, _ := newMKSMock(t, clusterapi.Options{})
	var posts int
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy:             checkMKSGone(srv),
		Steps: []resource.TestStep{
			{
				Config: clusterConfig(srv.URL, "1.36", "", ""),
				Check: func(_ *terraform.State) error {
					posts = countMKS(srv, "POST ")
					return nil
				},
			},
			{
				// Only timeouts and force_destroy change: Terraform updates its state and sends nothing.
				Config: clusterConfig(srv.URL, "1.36", "", "  force_destroy = true\n  timeouts = { create = \"30m\" }"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(mksAddr, "force_destroy", "true"),
					resource.TestCheckResourceAttr(mksAddr, "status.phase", "running"),
					func(_ *terraform.State) error {
						if n := countMKS(srv, "POST "); n != posts {
							return fmt.Errorf("a client-side change re-applied the cluster: %d POSTs, want %d", n, posts)
						}
						return nil
					},
				),
			},
		},
	})
}

func TestAccMKSCluster_destroyWaitsForTheBackingInstance(t *testing.T) {
	// The cluster record vanishes before the compute instance behind it; a workspace cannot be
	// deleted until that instance is gone, so destroy must wait for it.
	srv, _ := newMKSMock(t, clusterapi.Options{MirrorLinger: 400 * time.Millisecond})
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy: func(_ *terraform.State) error {
			if srv.Exists(testProject, mksWorkspace, "c1") {
				return fmt.Errorf("cluster still exists")
			}
			if srv.MirrorExists(testProject, mksWorkspace, "c1") {
				return fmt.Errorf("destroy returned while the cluster's compute instance still existed")
			}
			return nil
		},
		Steps: []resource.TestStep{{Config: clusterConfig(srv.URL, "1.36", "", "")}},
	})
}

func TestAccMKSCluster_projectScope(t *testing.T) {
	srv, _ := newMKSMock(t, clusterapi.Options{})
	cfg := providerBlock(srv.URL) + fmt.Sprintf(`
resource "gpupaas_kubernetes_cluster" "c" {
  metadata = { name = "c1", project = %q }
  spec = {
    profile_name       = "mks-oneclick-1"
    kubernetes_version = "1.36"
    control_plane      = { node_count = 1 }
  }
}
`, testProject)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy: func(_ *terraform.State) error {
			if srv.Exists(testProject, "", "c1") {
				return fmt.Errorf("cluster still exists")
			}
			return nil
		},
		Steps: []resource.TestStep{{
			Config: cfg,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr(mksAddr, "id", "system-catalog/c1"),
				resource.TestCheckResourceAttr(mksAddr, "status.phase", "running"),
				// Nothing was configured for the SKU: the profile defaults apply and stay out of the plan.
				resource.TestCheckNoResourceAttr(mksAddr, "spec.control_plane.sku_name"),
				func(_ *terraform.State) error {
					c := srv.Cluster(testProject, "", "c1")
					if c == nil || c.Spec.ControlPlane.SKUName != "bmaas-test-1" || c.Spec.ControlPlane.OSImage != "ubuntu-26" {
						return fmt.Errorf("profile defaults were not applied: %#v", c)
					}
					return nil
				},
				func(_ *terraform.State) error {
					if n := countMKS(srv, "POST /apis/paas.k8smgmt.io/v3/projects/system-catalog/clusters"); n != 1 {
						return fmt.Errorf("project-scope create used %d project routes: %v", n, srv.Requests())
					}
					return nil
				},
			),
		}},
	})
}

func TestAccMKSCluster_planTimeValidation(t *testing.T) {
	srv, _ := newMKSMock(t, clusterapi.Options{})
	cases := []struct {
		name string
		cfg  string
		want string
	}{
		{"unsupported version", clusterConfig(srv.URL, "1.20", "", ""), `supports 1.36, 1.35; got "1.20"`},
		{"sku typo", strings.Replace(clusterConfig(srv.URL, "1.36", "", ""), `"bmaas-test-1"`, `"bmaas-test-2"`, 1), `Did you mean "bmaas-test-1"?`},
		{"image", strings.Replace(clusterConfig(srv.URL, "1.36", "", ""), `"ubuntu-26"`, `"ubuntu-99"`, 1), `allows these image values: ubuntu-26`},
		{"sku type", strings.Replace(clusterConfig(srv.URL, "1.36", "", ""), `"BMAAS"`, `"VMAAS"`, 1), `SKU type values: BMAAS`},
		{"location", clusterConfig(srv.URL, "1.36", `    location = "mars-dc"`, ""), `allows these datacenter values: default-dc; got "mars-dc"`},
		{"cni", clusterConfig(srv.URL, "1.36", `    networking = { cni_name = "Cilium" }`, ""), `supports Calico 3.32.2`},
		{"duplicate pool", clusterConfig(srv.URL, "1.36", "    worker_pools = ["+pool("a", 1)+", "+pool("a", 2)+"]", ""), `Worker pool "a" is defined twice`},
		{"empty control plane", strings.Replace(clusterConfig(srv.URL, "1.36", "", ""), "node_count = 1", "node_count = 0", 1), `at least one control-plane node`},
		{"worker sku", clusterConfig(srv.URL, "1.36", `    worker_pools = [{ name = "w", node_count = 1, sku_name = "nope" }]`, ""), `Profile "mks-oneclick-1" allows these SKU values`},
	}
	var steps []resource.TestStep
	for _, c := range cases {
		steps = append(steps, resource.TestStep{Config: c.cfg, PlanOnly: true, ExpectError: errRe(c.want)})
	}
	resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: protoFactories(), Steps: steps})

	// Nothing was created while validating.
	if n := countMKS(srv, "POST "); n != 0 {
		t.Fatalf("validation created clusters: %v", srv.Requests())
	}
}

func TestAccMKSCluster_provisionFailureIsReportedAndCleanedUp(t *testing.T) {
	srv, _ := newMKSMock(t, clusterapi.Options{})
	srv.FailProvision = func(string) string { return "no free node with tag server_type:l40s" }
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy:             checkMKSGone(srv),
		Steps: []resource.TestStep{{
			Config:      clusterConfig(srv.URL, "1.36", "", ""),
			ExpectError: errRe(`no free node with tag server_type:l40s`),
		}},
	})
}

func TestAccMKSCluster_provisionFailureShowsTheNodeCause(t *testing.T) {
	// The cluster only says "a node failed"; the cause lives on the failed node's compute instance.
	srv, _ := newMKSMock(t, clusterapi.Options{})
	srv.FailProvision = func(string) string { return "Couldn't provision cluster c1 because a node failed to provision." }
	srv.NodeFailure = func(string) string {
		return `Failed to execute Node Provisioning handler: Bond configuration failed (exit=124).`
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy:             checkMKSGone(srv),
		Steps: []resource.TestStep{{
			Config:      clusterConfig(srv.URL, "1.36", "", ""),
			ExpectError: errRe(`Node c1-node-m0 failed: Failed to execute Node Provisioning handler: Bond configuration failed (exit=124).`),
		}},
	})
}

func TestAccMKSCluster_provisionFailureShowsTheClusterWorkflowCause(t *testing.T) {
	// Before any node exists the cause is on the cluster's own instance (live example on QC:
	// "no healthy agents found"), while the cluster itself only says "temporarily unavailable".
	srv, _ := newMKSMock(t, clusterapi.Options{})
	srv.FailProvision = func(string) string { return "Couldn't provision cluster c1; provisioning is temporarily unavailable." }
	srv.ClusterFailure = func(string) string { return "no healthy agents found. assigned agents: agent-a, agent-b" }
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy:             checkMKSGone(srv),
		Steps: []resource.TestStep{{
			Config:      clusterConfig(srv.URL, "1.36", "", ""),
			ExpectError: errRe(`Cluster workflow c1 failed: no healthy agents found. assigned agents: agent-a, agent-b`),
		}},
	})
}

func TestAccMKSCluster_teardownFailureCanBeRetried(t *testing.T) {
	srv, _ := newMKSMock(t, clusterapi.Options{})
	var calls atomic.Int32
	srv.FailDestroy = func(string) string {
		if calls.Add(1) == 1 {
			return "runner could not reach the node"
		}
		return ""
	}
	cfg := clusterConfig(srv.URL, "1.36", "", "")
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy:             checkMKSGone(srv),
		Steps: []resource.TestStep{
			{Config: cfg},
			{Config: cfg, Destroy: true, ExpectError: errRe(`runner could not reach the node`)},
		},
	})
}

func TestAccMKSCluster_deletedOutsideTerraformIsRecreated(t *testing.T) {
	srv, cs := newMKSMock(t, clusterapi.Options{})
	cfg := clusterConfig(srv.URL, "1.36", "", "")
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy:             checkMKSGone(srv),
		Steps: []resource.TestStep{
			{Config: cfg},
			{
				PreConfig: func() {
					ctx := context.Background()
					c := cs.V1alpha1().Workspaces(testProject).MKSClusters(mksWorkspace)
					if err := c.Delete(ctx, "c1", gpupaas.DeleteOptions{}); err != nil {
						t.Fatal(err)
					}
					for i := 0; i < 200 && srv.Exists(testProject, mksWorkspace, "c1"); i++ {
						if _, err := c.Get(ctx, "c1", gpupaas.GetOptions{}); gpupaas.IsNotFound(err) {
							break
						}
						time.Sleep(10 * time.Millisecond)
					}
				},
				Config: cfg,
				Check:  resource.TestCheckResourceAttr(mksAddr, "status.phase", "running"),
			},
		},
	})
}

func TestAccMKSCluster_dataSources(t *testing.T) {
	srv, _ := newMKSMock(t, clusterapi.Options{})
	cfg := clusterConfig(srv.URL, "1.36", "    worker_pools = ["+pool("pool-a", 1)+"]", "") + fmt.Sprintf(`
data "gpupaas_kubernetes_cluster" "d" {
  name      = gpupaas_kubernetes_cluster.c.metadata.name
  project   = %q
  workspace = %q
}

data "gpupaas_kubernetes_sku" "p" {
  name    = "mks-oneclick-1"
  project = %q
}
`, testProject, mksWorkspace, testProject)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy:             checkMKSGone(srv),
		Steps: []resource.TestStep{{
			Config: cfg,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("data.gpupaas_kubernetes_cluster.d", "status.phase", "running"),
				resource.TestCheckResourceAttr("data.gpupaas_kubernetes_cluster.d", "spec.profile_name", "mks-oneclick-1"),
				resource.TestCheckResourceAttr("data.gpupaas_kubernetes_cluster.d", "spec.kubernetes_version", "v1.36.4"),
				resource.TestCheckResourceAttr("data.gpupaas_kubernetes_cluster.d", "spec.control_plane.sku_name", "bmaas-test-1"),
				resource.TestCheckResourceAttr("data.gpupaas_kubernetes_cluster.d", "spec.worker_pools.0.name", "pool-a"),
				resource.TestCheckResourceAttr("data.gpupaas_kubernetes_cluster.d", "status.node_count", "2"),
				resource.TestCheckResourceAttr("data.gpupaas_kubernetes_sku.p", "id", "system-catalog/mks-oneclick-1"),
				resource.TestCheckResourceAttr("data.gpupaas_kubernetes_sku.p", "supported_kubernetes_versions.#", "2"),
				resource.TestCheckResourceAttr("data.gpupaas_kubernetes_sku.p", "control_plane.sku_names.0", "bmaas-test-1"),
				resource.TestCheckResourceAttr("data.gpupaas_kubernetes_sku.p", "default_cni.name", "Calico"),
				resource.TestCheckResourceAttr("data.gpupaas_kubernetes_sku.p", "datacenters.0", "default-dc"),
				resource.TestCheckResourceAttr("data.gpupaas_kubernetes_sku.p", "supported_cnis.0.version", "3.32.2"),
			),
		}},
	})
}

func TestAccMKSCluster_failedCreateKeepsTheStoredClusterInState(t *testing.T) {
	// The live API stores the cluster in ERROR and still fails the create call (for example
	// when starting the provisioning fails). The cluster must stay tracked so it gets cleaned up.
	srv, _ := newMKSMock(t, clusterapi.Options{})
	srv.RejectAfterStore = func(string) string { return "volume backend is not reachable" }
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy:             checkMKSGone(srv),
		Steps: []resource.TestStep{{
			Config:      clusterConfig(srv.URL, "1.36", "", ""),
			ExpectError: errRe(`The platform kept the cluster in status "error"`),
			Check:       func(_ *terraform.State) error { return nil },
		}},
	})
}

func TestAccMKSCluster_missingSSHKeyIsReportedByTheAPIAndCleanedUp(t *testing.T) {
	// A key created in the same configuration has a known name but does not exist at plan time,
	// so existence is not checked in the plan. The API rejects the create (and keeps the record in
	// ERROR); the provider must report that and still be able to remove the cluster.
	srv, _ := newMKSMock(t, clusterapi.Options{SSHKeys: []string{"other"}})
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy:             checkMKSGone(srv),
		Steps: []resource.TestStep{{
			Config:      clusterConfig(srv.URL, "1.36", "", ""), // uses ssh_key = "mine"
			ExpectError: errRe(`SSH key "mine" doesn't exist`),
		}},
	})
}

func TestAccMKSCluster_profileDefaultSSHKeyIsOnlyUsedWhenItExists(t *testing.T) {
	// The profile's default key "mine" does not exist in this workspace, so none is sent.
	srv, _ := newMKSMock(t, clusterapi.Options{SSHKeys: []string{"other"}})
	cfg := providerBlock(srv.URL) + fmt.Sprintf(`
resource "gpupaas_kubernetes_cluster" "c" {
  metadata = { name = "c1", project = %q, workspace = %q }
  spec = {
    profile_name       = "mks-oneclick-1"
    kubernetes_version = "1.36"
    control_plane      = { node_count = 1 }
  }
}
`, testProject, mksWorkspace)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy:             checkMKSGone(srv),
		Steps: []resource.TestStep{{
			Config: cfg,
			Check: checkCluster(srv, func(c *apiv1.MKSCluster) error {
				if c.Spec.ControlPlane.SSHKey != "" {
					return fmt.Errorf("a key that does not exist was sent: %q", c.Spec.ControlPlane.SSHKey)
				}
				return nil
			}),
		}},
	})
}

func TestAccMKSCluster_updateThatFailsWhileTheClusterStaysRunningIsReported(t *testing.T) {
	// Live behaviour (narayana-tb1): adding a worker pool whose node fails to provision leaves the
	// cluster RUNNING with the requested spec and even a registered node; only the reason changes
	// and the cluster's own workflow ends failed. The provider must report that, not succeed.
	srv, _ := newMKSMock(t, clusterapi.Options{})
	srv.UpdateFailure = func(string) string {
		return `Failed to execute Node Provisioning handler: Bond configuration failed (exit=124).`
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy:             checkMKSGone(srv),
		Steps: []resource.TestStep{
			{Config: clusterConfig(srv.URL, "1.36", "", "")},
			{
				Config:      clusterConfig(srv.URL, "1.36", "    worker_pools = ["+pool("pool-a", 1)+"]", ""),
				ExpectError: errRe(`Bond configuration failed (exit=124).`),
			},
		},
	})
}

func TestAccMKSCluster_kubeconfigDataSource(t *testing.T) {
	srv, _ := newMKSMock(t, clusterapi.Options{})
	cfg := clusterConfig(srv.URL, "1.36", "", "") + fmt.Sprintf(`
data "gpupaas_kubernetes_cluster_kubeconfig" "k" {
  name      = gpupaas_kubernetes_cluster.c.metadata.name
  project   = %q
  workspace = %q
}
`, testProject, mksWorkspace)
	missing := providerBlock(srv.URL) + fmt.Sprintf(`
data "gpupaas_kubernetes_cluster_kubeconfig" "k" {
  name      = "no-such-cluster"
  project   = %q
  workspace = %q
}
`, testProject, mksWorkspace)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy:             checkMKSGone(srv),
		Steps: []resource.TestStep{
			{Config: missing, ExpectError: errRe(`Download kubeconfig failed: not found`)},
			{
				Config: cfg,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.gpupaas_kubernetes_cluster_kubeconfig.k", "id", "system-catalog/ws/c1"),
					resource.TestMatchResourceAttr("data.gpupaas_kubernetes_cluster_kubeconfig.k", "kubeconfig", regexp.MustCompile(`(?s)kind: Config.*current-context: c1`)),
				),
			},
		},
	})
}
