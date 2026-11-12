package provider_test

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	apiv1 "github.com/gpupaas-ai/gpupaas-go/apis/v1alpha1"
	"github.com/gpupaas-ai/gpupaas-go/mock/clusterapi"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// Rules and calls of the v3.1.41 API reference ("MKS First-Class - API Docs"), against a mock that enforces
// that release's rules (PerPoolNetwork): the control plane size, the pool datacenter, SSH keys and node
// access, annotations, the metadata fast path, and the discovery, status and audit data sources.

func TestAccMKSCluster_theControlPlaneHasOneOrThreeNodesAndKeepsItsSize(t *testing.T) {
	srv := newPoolsMock(t, clusterapi.Options{})
	base := poolsConfig(srv.URL, "1.35", cpNetwork, "")
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy:             checkMKSGone(srv),
		Steps: []resource.TestStep{
			{Config: strings.Replace(base, "node_count = 1", "node_count = 2", 1), PlanOnly: true, ExpectError: errRe("1 node, or 3 for high availability")},
			{Config: strings.Replace(base, "node_count = 1", "node_count = 5", 1), PlanOnly: true, ExpectError: errRe("1 node, or 3 for high availability")},
			{Config: base, Check: resource.TestCheckResourceAttr(mksAddr, "spec.control_plane.node_count", "1")},
			// The platform cannot convert a running cluster to high availability: the plan says so.
			{Config: strings.Replace(base, "node_count = 1", "node_count = 3", 1), PlanOnly: true, ExpectError: errRe("can't be changed")},
		},
	})
	if n := countMKS(srv, "POST "); n != 1 {
		t.Fatalf("the refused plans reached the API: %v", srv.Requests())
	}
}

func TestAccMKSCluster_aPoolDatacenterMustBeTheLocation(t *testing.T) {
	srv := newPoolsMock(t, clusterapi.Options{})
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		Steps: []resource.TestStep{
			{
				Config:      poolsConfigWith(srv.URL, "1.35", cpNetwork, gpuPool(`      datacenter = "elsewhere"`), `    location = "default-dc"`, ""),
				PlanOnly:    true,
				ExpectError: errRe("per-pool datacenter"),
			},
		},
	})
	if n := countMKS(srv, "POST "); n != 0 {
		t.Fatalf("validation created clusters: %v", srv.Requests())
	}
}

func TestAccMKSCluster_sshKeyIsTheNameOfAKey(t *testing.T) {
	srv := newPoolsMock(t, clusterapi.Options{})
	inline := strings.Replace(poolsConfig(srv.URL, "1.35", cpNetwork, ""), `ssh_key    = "mine"`, `ssh_key    = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5 me@host"`, 1)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		Steps:                    []resource.TestStep{{Config: inline, PlanOnly: true, ExpectError: errRe("inline public keys are not accepted")}},
	})
	if n := countMKS(srv, "POST "); n != 0 {
		t.Fatalf("validation created clusters: %v", srv.Requests())
	}
}

// A profile that asks the user to choose how to log in, and has no default, makes a new cluster name a key or a
// password. The platform installs one per cluster, so the control plane or any pool having one is enough.
func TestAccMKSCluster_aNewClusterNeedsNodeAccessWhenTheProfileAsksForIt(t *testing.T) {
	p := poolsProfile()
	p.Spec.ExposeSSHKeyChoice, p.Spec.DefaultSSHKey = true, ""
	srv, _ := newMKSMock(t, clusterapi.Options{PerPoolNetwork: true, Profiles: []apiv1.MKSClusterProfile{p}})
	noKey := func(cfg string) string { return strings.Replace(cfg, "      ssh_key    = \"mine\"\n", "", 1) }
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy:             checkMKSGone(srv),
		Steps: []resource.TestStep{
			{Config: noKey(poolsConfig(srv.URL, "1.35", cpNetwork, gpuPool(""))), PlanOnly: true, ExpectError: errRe("Node access is required")},
			{
				// A password on a pool is enough: one key serves the cluster.
				Config: noKey(poolsConfig(srv.URL, "1.35", cpNetwork, gpuPool(`      user_password = "pw-1"`))),
				Check:  resource.TestCheckResourceAttr(mksAddr, "spec.worker_pools.0.name", "gpu"),
			},
			{
				// A pool added later needs nothing: the platform never returns a password.
				Config: noKey(poolsConfig(srv.URL, "1.35", cpNetwork, gpuPool(`      user_password = "pw-1"`)+", "+strings.Replace(gpuPool(""), `name     = "gpu"`, `name     = "more"`, 1))),
				Check:  resource.TestCheckResourceAttr(mksAddr, "spec.worker_pools.1.name", "more"),
			},
		},
	})
}

// A boot disk is checked at plan time against the range and the step of the pool's VM SKU.
func TestAccMKSCluster_bootDiskIsCheckedAgainstTheVMSKU(t *testing.T) {
	vm := apiv1.VMProfile{Metadata: apiv1.ObjectMeta{Name: "vm-small"}, Spec: apiv1.VMProfileSpec{BootDisk: &apiv1.VMBootDisk{MinSize: "50", MaxSize: "100", Increment: "10", Unit: "GB"}}}
	srv, _ := newMKSMock(t, clusterapi.Options{PerPoolNetwork: true, Profiles: []apiv1.MKSClusterProfile{poolsProfile()}, VMProfiles: []apiv1.VMProfile{vm}})
	cfg := func(size string) string {
		return poolsConfig(srv.URL, "1.35", cpNetwork, gpuPool("      boot_disk_size_gb = "+size))
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy:             checkMKSGone(srv),
		Steps: []resource.TestStep{
			{Config: cfg("200"), PlanOnly: true, ExpectError: errRe(`outside the boot disk range of SKU "vm-small" (50-100 GB)`)},
			{Config: cfg("55"), PlanOnly: true, ExpectError: errRe("sizes step by 10 GB from 50 GB")},
			{Config: cfg("60"), Check: resource.TestCheckResourceAttr(mksAddr, "spec.worker_pools.0.boot_disk_size_gb", "60")},
		},
	})
}

func TestAccMKSCluster_workerPoolAnnotations(t *testing.T) {
	srv := newPoolsMock(t, clusterapi.Options{PlatformAdditions: true})
	pool := func(ann string) string {
		if ann == "" {
			return gpuPool("")
		}
		return gpuPool("      annotations = " + ann)
	}
	annotations := func(want map[string]string) resource.TestCheckFunc {
		return checkCluster(srv, func(c *apiv1.MKSCluster) error {
			got := c.Spec.WorkerPools[0].Annotations
			if len(got) != len(want) {
				return fmt.Errorf("annotations on the API: %v, want %v", got, want)
			}
			for k, v := range want {
				if got[k] != v {
					return fmt.Errorf("annotations on the API: %v, want %v", got, want)
				}
			}
			return nil
		})
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy:             checkMKSGone(srv),
		Steps: []resource.TestStep{
			{
				Config: poolsConfig(srv.URL, "1.35", cpNetwork, pool(`{ note = "a" }`)),
				Check:  resource.ComposeAggregateTestCheckFunc(resource.TestCheckResourceAttr(mksAddr, "spec.worker_pools.0.annotations.note", "a"), annotations(map[string]string{"note": "a"})),
			},
			{
				Config: poolsConfig(srv.URL, "1.35", cpNetwork, pool(`{ note = "b", owner = "me" }`)),
				Check:  resource.ComposeAggregateTestCheckFunc(resource.TestCheckResourceAttr(mksAddr, "spec.worker_pools.0.annotations.owner", "me"), annotations(map[string]string{"note": "b", "owner": "me"})),
			},
			{
				Config: poolsConfig(srv.URL, "1.35", cpNetwork, pool("")),
				Check:  resource.ComposeAggregateTestCheckFunc(resource.TestCheckNoResourceAttr(mksAddr, "spec.worker_pools.0.annotations.note"), annotations(nil)),
			},
		},
	})
}

func tagsSpec(kv string) string {
	if kv == "" {
		return ""
	}
	return "    tags = { " + kv + " }"
}

// Labels, annotations and tags have a route that edits them in place. A change to them alone must not send the
// cluster again (the platform would redeploy it).
func TestAccMKSCluster_tagChangesUseTheMetadataRoute(t *testing.T) {
	srv := newPoolsMock(t, clusterapi.Options{})
	var posts int
	cfg := func(tags string) string { return poolsConfigWith(srv.URL, "1.35", cpNetwork, "", tagsSpec(tags), "") }
	patches := func(want int) resource.TestCheckFunc {
		return func(*terraform.State) error {
			if n := countMKS(srv, "PATCH "); n != want {
				return fmt.Errorf("%d PATCH requests, want %d: %v", n, want, srv.Requests())
			}
			if n := countMKS(srv, "POST "); n != posts {
				return fmt.Errorf("a tag change was sent as a full update (%d POSTs, was %d)", n, posts)
			}
			return nil
		}
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy:             checkMKSGone(srv),
		Steps: []resource.TestStep{
			{
				Config: cfg(`env = "dev"`),
				Check: func(*terraform.State) error {
					posts = countMKS(srv, "POST ")
					return nil
				},
			},
			{
				Config: cfg(`env = "prod", owner = "me"`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(mksAddr, "spec.tags.env", "prod"),
					resource.TestCheckResourceAttr(mksAddr, "spec.tags.owner", "me"),
					checkCluster(srv, func(c *apiv1.MKSCluster) error {
						if len(c.Spec.Tags) != 2 || c.Spec.Tags["env"] != "prod" || c.Status.Phase() != "running" {
							return fmt.Errorf("tags on the API: %v, phase %s", c.Spec.Tags, c.Status.Phase())
						}
						return nil
					}),
					patches(1),
				),
			},
			{
				// Removing them all clears them on the platform.
				Config: cfg(""),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckNoResourceAttr(mksAddr, "spec.tags.env"),
					checkCluster(srv, func(c *apiv1.MKSCluster) error {
						if len(c.Spec.Tags) != 0 {
							return fmt.Errorf("tags on the API: %v", c.Spec.Tags)
						}
						return nil
					}),
					patches(2),
				),
			},
			{
				// Tags and a pool size at once need the full update.
				Config: poolsConfigWith(srv.URL, "1.35", cpNetwork, gpuPool(""), tagsSpec(`env = "x"`), ""),
				Check: func(*terraform.State) error {
					if n := countMKS(srv, "POST "); n != posts+1 {
						return fmt.Errorf("a pool and a tag change need one full update: %d POSTs, was %d", n, posts)
					}
					return nil
				},
			},
		},
	})
}

// The live controller starts a node reconcile after a metadata change: the cluster reads UPDATING for a minute or so
// ("Compute allocation in progress"), then RUNNING again. The apply must end after that, so a change made right after
// it does not meet a cluster that is still updating, and the state does not keep the transient phase.
func TestAccMKSCluster_tagChangeWaitsForTheNodeReconcile(t *testing.T) {
	srv := newPoolsMock(t, clusterapi.Options{ReconcileAfterMetadata: true})
	cfg := func(tags string) string { return poolsConfigWith(srv.URL, "1.35", cpNetwork, "", tagsSpec(tags), "") }
	var posts int
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy:             checkMKSGone(srv),
		Steps: []resource.TestStep{
			{
				Config: cfg(`env = "dev"`),
				Check: func(*terraform.State) error {
					posts = countMKS(srv, "POST ")
					return nil
				},
			},
			{
				Config: cfg(`env = "prod"`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(mksAddr, "spec.tags.env", "prod"),
					resource.TestCheckResourceAttr(mksAddr, "status.phase", "running"),
					resource.TestCheckResourceAttr(mksAddr, "status.reason", "Node reconciliation complete"),
					func(*terraform.State) error {
						if n := countMKS(srv, "PATCH "); n != 1 {
							return fmt.Errorf("%d PATCH requests, want 1: %v", n, srv.Requests())
						}
						if n := countMKS(srv, "POST "); n != posts {
							return fmt.Errorf("a tag change was sent as a full update (%d POSTs, was %d)", n, posts)
						}
						return nil
					},
				),
			},
		},
	})
}

// A controller without the metadata route answers 404: the change is sent as a full update instead.
func TestAccMKSCluster_tagChangesFallBackToTheFullUpdateWithoutAMetadataRoute(t *testing.T) {
	srv := newPoolsMock(t, clusterapi.Options{NoMetadataRoute: true})
	cfg := func(tags string) string { return poolsConfigWith(srv.URL, "1.35", cpNetwork, "", tagsSpec(tags), "") }
	var posts int
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy:             checkMKSGone(srv),
		Steps: []resource.TestStep{
			{Config: cfg(`env = "dev"`), Check: func(*terraform.State) error { posts = countMKS(srv, "POST "); return nil }},
			{
				Config: cfg(`env = "prod"`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(mksAddr, "spec.tags.env", "prod"),
					func(*terraform.State) error {
						if countMKS(srv, "PATCH ") != 1 || countMKS(srv, "POST ") != posts+1 {
							return fmt.Errorf("want one failed PATCH and one POST: %v", srv.Requests())
						}
						return nil
					},
				),
			},
		},
	})
}

// The platform labels the cluster itself and tags can be set in the console. Terraform manages the ones it is given:
// changing its tags, or applying a change that is only its own (a timeout), must leave the others alone and must not
// send the cluster again.
func TestAccMKSCluster_tagChangesKeepWhatTheUserDoesNotManage(t *testing.T) {
	srv := newPoolsMock(t, clusterapi.Options{PlatformAdditions: true})
	withLabels := func(cfg string) string {
		return strings.Replace(cfg, "    workspace = \"ws\"\n  }", "    workspace = \"ws\"\n    labels    = { team = \"ml\" }\n  }", 1)
	}
	cfg := func(tags, extra string) string {
		return withLabels(poolsConfigWith(srv.URL, "1.35", cpNetwork, "", tagsSpec(tags), extra))
	}
	var posts, patches int
	remember := func(*terraform.State) error {
		posts, patches = countMKS(srv, "POST "), countMKS(srv, "PATCH ")
		return nil
	}
	requests := func(dPosts, dPatches int) resource.TestCheckFunc {
		return func(*terraform.State) error {
			if p, q := countMKS(srv, "POST ")-posts, countMKS(srv, "PATCH ")-patches; p != dPosts || q != dPatches {
				return fmt.Errorf("%d POST and %d PATCH requests since the last step, want %d and %d: %v", p, q, dPosts, dPatches, srv.Requests())
			}
			return nil
		}
	}
	keeps := func(tags map[string]string) resource.TestCheckFunc {
		return checkCluster(srv, func(c *apiv1.MKSCluster) error {
			if c.Metadata.Labels["paas.envmgmt.io/compute_type"] != "mks" || c.Metadata.Labels["team"] != "ml" {
				return fmt.Errorf("labels on the API: %v", c.Metadata.Labels)
			}
			if len(c.Spec.Tags) != len(tags) {
				return fmt.Errorf("tags on the API: %v, want %v", c.Spec.Tags, tags)
			}
			for k, v := range tags {
				if c.Spec.Tags[k] != v {
					return fmt.Errorf("tags on the API: %v, want %v", c.Spec.Tags, tags)
				}
			}
			return nil
		})
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy:             checkMKSGone(srv),
		Steps: []resource.TestStep{
			{
				Config: cfg(`env = "dev"`, ""),
				Check: resource.ComposeAggregateTestCheckFunc(
					keeps(map[string]string{"env": "dev"}),
					func(*terraform.State) error {
						// Someone adds a tag in the console.
						srv.SetTags(testProject, mksWorkspace, "c1", map[string]string{"env": "dev", "console": "by-hand"})
						return nil
					},
					remember,
				),
			},
			{
				// Only a timeout changes: nothing is sent, and the tag set by hand is not in the way.
				Config: cfg(`env = "dev"`, `  timeouts = { update = "30m" }`),
				Check: resource.ComposeAggregateTestCheckFunc(
					requests(0, 0),
					resource.TestCheckResourceAttr(mksAddr, "spec.tags.%", "1"),
					keeps(map[string]string{"env": "dev", "console": "by-hand"}),
				),
			},
			{
				// A tag changes: one patch, no full update, and the tag set by hand and the platform label stay.
				Config: cfg(`env = "prod", owner = "me"`, `  timeouts = { update = "30m" }`),
				Check: resource.ComposeAggregateTestCheckFunc(
					requests(0, 1),
					resource.TestCheckResourceAttr(mksAddr, "spec.tags.owner", "me"),
					keeps(map[string]string{"env": "prod", "owner": "me", "console": "by-hand"}),
				),
			},
			{
				// The tags Terraform managed are removed; the one set by hand is not.
				Config: cfg("", `  timeouts = { update = "30m" }`),
				Check:  resource.ComposeAggregateTestCheckFunc(requests(0, 2), keeps(map[string]string{"console": "by-hand"})),
			},
		},
	})
}

// tags = {} is an empty map: Terraform refuses an apply whose result differs from the plan, so it must stay empty.
func TestAccMKSCluster_anEmptyTagsMapIsStable(t *testing.T) {
	srv := newPoolsMock(t, clusterapi.Options{})
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy:             checkMKSGone(srv),
		Steps: []resource.TestStep{
			{
				Config: poolsConfigWith(srv.URL, "1.35", cpNetwork, "", "    tags = {}", ""),
				Check:  resource.TestCheckResourceAttr(mksAddr, "spec.tags.%", "0"),
			},
			{
				Config: poolsConfigWith(srv.URL, "1.35", cpNetwork, "", `    tags = { env = "dev" }`, ""),
				Check:  resource.TestCheckResourceAttr(mksAddr, "spec.tags.env", "dev"),
			},
			{
				Config: poolsConfigWith(srv.URL, "1.35", cpNetwork, "", "    tags = {}", ""),
				Check:  resource.TestCheckResourceAttr(mksAddr, "spec.tags.%", "0"),
			},
		},
	})
}

func TestAccMKSDataSources_discoveryStatusAndAudit(t *testing.T) {
	srv := newPoolsMock(t, clusterapi.Options{})
	data := fmt.Sprintf(`
data "gpupaas_kubernetes_versions" "v" {
  project = %[1]q
}
data "gpupaas_kubernetes_sku_create_options" "o" {
  name    = "mks-oneclick-1"
  project = %[1]q
}
data "gpupaas_kubernetes_sku_capacity" "cap" {
  name    = "mks-oneclick-1"
  project = %[1]q
}
data "gpupaas_kubernetes_cluster_status" "s" {
  cluster    = "c1"
  project    = %[1]q
  depends_on = [gpupaas_kubernetes_cluster.c]
}
data "gpupaas_kubernetes_cluster_audit_events" "all" {
  cluster    = "c1"
  project    = %[1]q
  depends_on = [gpupaas_kubernetes_cluster.c]
}
data "gpupaas_kubernetes_cluster_audit_events" "other_action" {
  cluster    = "c1"
  project    = %[1]q
  action     = "cluster.upgrade"
  limit      = 10
  depends_on = [gpupaas_kubernetes_cluster.c]
}
data "gpupaas_kubernetes_cluster_audit_events" "by_action" {
  cluster    = "c1"
  project    = %[1]q
  action     = "cluster.metadata.update"
  depends_on = [gpupaas_kubernetes_cluster.c]
}
data "gpupaas_kubernetes_cluster_audit_events" "completed" {
  cluster    = "c1"
  project    = %[1]q
  result     = "completed"
  depends_on = [gpupaas_kubernetes_cluster.c]
}
data "gpupaas_kubernetes_cluster_audit_events" "failed" {
  cluster    = "c1"
  project    = %[1]q
  result     = "failed"
  depends_on = [gpupaas_kubernetes_cluster.c]
}
data "gpupaas_kubernetes_cluster_audit_events" "someone_else" {
  cluster    = "c1"
  project    = %[1]q
  actor      = "someone-else"
  depends_on = [gpupaas_kubernetes_cluster.c]
}
data "gpupaas_kubernetes_cluster_audit_events" "since_the_future" {
  cluster    = "c1"
  project    = %[1]q
  from       = "2030-01-01T00:00:00Z"
  depends_on = [gpupaas_kubernetes_cluster.c]
}
data "gpupaas_kubernetes_cluster_audit_events" "since_the_past" {
  cluster    = "c1"
  project    = %[1]q
  from       = "2000-01-01T00:00:00Z"
  to         = "2030-01-01T00:00:00Z"
  depends_on = [gpupaas_kubernetes_cluster.c]
}
data "gpupaas_kubernetes_cluster_audit_events" "until_the_past" {
  cluster    = "c1"
  project    = %[1]q
  to         = "2000-01-01T00:00:00Z"
  depends_on = [gpupaas_kubernetes_cluster.c]
}
# length() fails on null: a filter that matches nothing must give an empty list.
output "none_matched" {
  value = length(data.gpupaas_kubernetes_cluster_audit_events.other_action.events)
}
output "capacity_supported" {
  value = data.gpupaas_kubernetes_sku_capacity.cap.supported
}
`, testProject)
	cfg := poolsConfig(srv.URL, "1.35", cpNetwork, gpuPool("")) + data
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy:             checkMKSGone(srv),
		Steps: []resource.TestStep{
			{
				Config: cfg,
				Check: resource.ComposeAggregateTestCheckFunc(
					// The cluster resource now lists what the platform reports about each node.
					resource.TestMatchResourceAttr(mksAddr, "status.nodes.0.private_ip", regexp.MustCompile(`^10\.0\.0\.`)),
					resource.TestCheckResourceAttr(mksAddr, "status.nodes.0.roles.0", "Master"),
					resource.TestCheckResourceAttr(mksAddr, "status.nodes.0.status", "READY"),

					resource.TestCheckResourceAttr("data.gpupaas_kubernetes_versions.v", "minors.#", "2"),
					resource.TestCheckResourceAttr("data.gpupaas_kubernetes_versions.v", "minors.0", "1.36"),
					resource.TestCheckResourceAttr("data.gpupaas_kubernetes_versions.v", "default_version", "1.36"),
					resource.TestCheckResourceAttr("data.gpupaas_kubernetes_versions.v", "versions.0.latest_patch", "v1.36.4"),

					resource.TestCheckResourceAttr("data.gpupaas_kubernetes_sku_create_options.o", "default_kubernetes_version", "1.36"),
					resource.TestCheckResourceAttr("data.gpupaas_kubernetes_sku_create_options.o", "datacenters.0", "default-dc"),
					resource.TestCheckResourceAttr("data.gpupaas_kubernetes_sku_create_options.o", "control_plane.sku_names.0", "bmaas-test-1"),
					resource.TestCheckResourceAttr("data.gpupaas_kubernetes_sku_create_options.o", "workers.sku_names.1", "vm-small"),
					resource.TestCheckResourceAttr("data.gpupaas_kubernetes_sku_create_options.o", "workers.sku_kinds.0", "bmaas"),
					resource.TestCheckResourceAttr("data.gpupaas_kubernetes_sku_create_options.o", "workers.images.0", "ubuntu-26"),

					resource.TestMatchResourceAttr("data.gpupaas_kubernetes_sku_capacity.cap", "json", regexp.MustCompile(`"profile":"mks-oneclick-1"`)),
					resource.TestCheckResourceAttr("data.gpupaas_kubernetes_sku_capacity.cap", "supported", "true"),
					resource.TestCheckOutput("capacity_supported", "true"),
					resource.TestCheckOutput("none_matched", "0"),

					resource.TestCheckResourceAttr("data.gpupaas_kubernetes_cluster_status.s", "phase", "running"),
					resource.TestCheckResourceAttr("data.gpupaas_kubernetes_cluster_status.s", "health", "healthy"),
					resource.TestCheckResourceAttr("data.gpupaas_kubernetes_cluster_status.s", "available_kubernetes_versions.0", "1.36"),
					resource.TestCheckResourceAttr("data.gpupaas_kubernetes_cluster_status.s", "control_plane.status", "ACTIVE"),
					resource.TestCheckResourceAttr("data.gpupaas_kubernetes_cluster_status.s", "control_plane.health", "healthy"),
					resource.TestCheckResourceAttr("data.gpupaas_kubernetes_cluster_status.s", "control_plane.nodes.#", "1"),
					resource.TestCheckResourceAttr("data.gpupaas_kubernetes_cluster_status.s", "control_plane.nodes.0.roles.0", "Master"),
					resource.TestCheckResourceAttr("data.gpupaas_kubernetes_cluster_status.s", "control_plane.nodes.0.state.ClusterNodeReady", "true"),
					resource.TestCheckResourceAttr("data.gpupaas_kubernetes_cluster_status.s", "worker_pools.0.name", "gpu"),
					resource.TestCheckResourceAttr("data.gpupaas_kubernetes_cluster_status.s", "worker_pools.0.nodes.#", "2"),
					resource.TestCheckResourceAttr("data.gpupaas_kubernetes_cluster_status.s", "worker_pools.0.nodes.0.roles.0", "Worker"),
					resource.TestMatchResourceAttr("data.gpupaas_kubernetes_cluster_status.s", "worker_pools.0.nodes.1.private_ip", regexp.MustCompile(`^10\.0\.0\.`)),

					resource.TestCheckResourceAttr("data.gpupaas_kubernetes_cluster_audit_events.all", "total", "1"),
					resource.TestCheckResourceAttr("data.gpupaas_kubernetes_cluster_audit_events.all", "events.0.action", "cluster.apply"),
					resource.TestCheckResourceAttr("data.gpupaas_kubernetes_cluster_audit_events.all", "events.0.result", "accepted"),
					resource.TestMatchResourceAttr("data.gpupaas_kubernetes_cluster_audit_events.all", "events.0.timestamp", regexp.MustCompile(`^20\d\d-\d\d-\d\dT\d\d:\d\d:\d\dZ$`)),
					resource.TestCheckResourceAttr("data.gpupaas_kubernetes_cluster_audit_events.other_action", "total", "0"),
					resource.TestCheckResourceAttr("data.gpupaas_kubernetes_cluster_audit_events.other_action", "events.#", "0"),
					resource.TestCheckResourceAttr("data.gpupaas_kubernetes_cluster_audit_events.by_action", "total", "0"),
					resource.TestCheckResourceAttr("data.gpupaas_kubernetes_cluster_audit_events.completed", "total", "0"),
					resource.TestCheckResourceAttr("data.gpupaas_kubernetes_cluster_audit_events.since_the_past", "total", "1"),
					resource.TestCheckResourceAttr("data.gpupaas_kubernetes_cluster_audit_events.since_the_future", "total", "0"),
					resource.TestCheckResourceAttr("data.gpupaas_kubernetes_cluster_audit_events.until_the_past", "total", "0"),
				),
			},
			{
				// A tag change adds a second event, of another action and result: the filters tell them apart.
				Config: poolsConfigWith(srv.URL, "1.35", cpNetwork, gpuPool(""), `    tags = { env = "dev" }`, "") + data,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.gpupaas_kubernetes_cluster_audit_events.all", "total", "2"),
					resource.TestCheckResourceAttr("data.gpupaas_kubernetes_cluster_audit_events.all", "events.0.action", "cluster.metadata.update"),
					resource.TestCheckResourceAttr("data.gpupaas_kubernetes_cluster_audit_events.all", "events.1.action", "cluster.apply"),
					resource.TestCheckResourceAttr("data.gpupaas_kubernetes_cluster_audit_events.by_action", "total", "1"),
					resource.TestCheckResourceAttr("data.gpupaas_kubernetes_cluster_audit_events.by_action", "events.0.result", "completed"),
					resource.TestCheckResourceAttr("data.gpupaas_kubernetes_cluster_audit_events.completed", "total", "1"),
					resource.TestCheckResourceAttr("data.gpupaas_kubernetes_cluster_audit_events.completed", "events.0.action", "cluster.metadata.update"),
					resource.TestCheckResourceAttr("data.gpupaas_kubernetes_cluster_audit_events.failed", "total", "0"),
					resource.TestCheckResourceAttr("data.gpupaas_kubernetes_cluster_audit_events.someone_else", "total", "0"),
					resource.TestCheckResourceAttr("data.gpupaas_kubernetes_cluster_audit_events.other_action", "total", "0"),
					resource.TestCheckResourceAttr("data.gpupaas_kubernetes_cluster_audit_events.since_the_past", "total", "2"),
					resource.TestCheckResourceAttr("data.gpupaas_kubernetes_cluster_audit_events.since_the_future", "total", "0"),
					resource.TestCheckResourceAttr("data.gpupaas_kubernetes_cluster_audit_events.until_the_past", "total", "0"),
				),
			},
		},
	})
}

// A profile whose compute type has no capacity answer (bare metal on the live controller) is not an error: the
// data source says so and leaves json empty.
func TestAccMKSDataSources_capacityNotSupported(t *testing.T) {
	srv := newPoolsMock(t, clusterapi.Options{NoCapacity: true})
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		Steps: []resource.TestStep{
			{
				Config: providerBlock(srv.URL) + fmt.Sprintf(`
data "gpupaas_kubernetes_sku_capacity" "cap" {
  name    = "mks-oneclick-1"
  project = %q
}
output "supported" {
  value = data.gpupaas_kubernetes_sku_capacity.cap.supported
}
output "json_is_null" {
  value = data.gpupaas_kubernetes_sku_capacity.cap.json == null
}
`, testProject),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.gpupaas_kubernetes_sku_capacity.cap", "supported", "false"),
					resource.TestCheckNoResourceAttr("data.gpupaas_kubernetes_sku_capacity.cap", "json"),
					resource.TestCheckOutput("supported", "false"),
					resource.TestCheckOutput("json_is_null", "true"),
				),
			},
			{
				// Any other refusal is still an error: only "not supported" is softened.
				Config: providerBlock(srv.URL) + fmt.Sprintf(`
data "gpupaas_kubernetes_sku_capacity" "cap" {
  name    = "no-such-profile"
  project = %q
}
`, testProject),
				ExpectError: errRe("Read Kubernetes SKU capacity"),
			},
		},
	})
}

func TestAccMKSDataSources_errors(t *testing.T) {
	srv := newPoolsMock(t, clusterapi.Options{})
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		Steps: []resource.TestStep{
			{
				Config: providerBlock(srv.URL) + fmt.Sprintf(`
data "gpupaas_kubernetes_cluster_audit_events" "e" {
  cluster = "c1"
  project = %q
  from    = "yesterday"
}
`, testProject),
				ExpectError: errRe("Invalid time"),
			},
			{
				Config: providerBlock(srv.URL) + fmt.Sprintf(`
data "gpupaas_kubernetes_cluster_status" "s" {
  cluster = "missing"
  project = %q
}
`, testProject),
				ExpectError: errRe("not found"),
			},
			{
				Config: providerBlock(srv.URL) + fmt.Sprintf(`
data "gpupaas_kubernetes_sku_create_options" "o" {
  name    = "no-such-sku"
  project = %q
}
`, testProject),
				ExpectError: errRe("not found"),
			},
		},
	})
}
