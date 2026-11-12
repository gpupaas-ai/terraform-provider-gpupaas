package provider_test

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	gpupaas "github.com/gpupaas-ai/gpupaas-go"
	apiv1 "github.com/gpupaas-ai/gpupaas-go/apis/v1alpha1"
	"github.com/gpupaas-ai/gpupaas-go/clientset"
	"github.com/gpupaas-ai/gpupaas-go/mock/bmapi"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/gpupaas-ai/terraform-provider-gpupaas/internal/provider/resources"
)

// These tests run the real terraform binary against the provider and an in-process mock of the
// first-class bare-metal API (dev.envmgmt.io/v1). Run them with TF_ACC=1.

const (
	bmAddr      = "gpupaas_bare_metal_instance.b"
	bmWorkspace = "ws"
)

func bmTestProfiles() []apiv1.BmProfile {
	return []apiv1.BmProfile{
		{
			Metadata: apiv1.ObjectMeta{Name: "bm-l40s"},
			Spec: apiv1.BmProfileSpec{
				ProvisionerType: "bcm", Image: []string{"ubuntu-22", "ubuntu-24"}, ImageDefault: "ubuntu-22", Datacenter: []string{"dc1"},
				AssignPublicIP: "CustomerSelectable", MRCContractTerm: []int32{12, 36}, GPUName: "L40S", UseCase: "bare_metal",
				InventoryConfig: []apiv1.BmInventoryTag{{Key: "server_type", Value: "l40s"}},
			},
			Status: apiv1.BmProfileStatus{Status: "success", GlobalSettings: &apiv1.BmProfileGlobalSettings{Agents: []string{"agent-1"}}},
		},
		{
			Metadata: apiv1.ObjectMeta{Name: "bm-noip"},
			Spec: apiv1.BmProfileSpec{
				ProvisionerType: "non_bcm", Image: []string{"ubuntu-22"}, Datacenter: []string{"dc1", "dc2"}, AssignPublicIP: "Disabled",
			},
			Status: apiv1.BmProfileStatus{Status: "success", GlobalSettings: &apiv1.BmProfileGlobalSettings{Agents: []string{"agent-1"}}},
		},
		{
			// no agent assigned: provisioning cannot work
			Metadata: apiv1.ObjectMeta{Name: "bm-noagent"},
			Spec:     apiv1.BmProfileSpec{ProvisionerType: "non_bcm", Image: []string{"ubuntu-22"}, Datacenter: []string{"dc1"}},
			Status:   apiv1.BmProfileStatus{Status: "success", GlobalSettings: &apiv1.BmProfileGlobalSettings{}},
		},
	}
}

func newBMMock(t *testing.T, opts bmapi.Options) (*bmapi.Server, clientset.Interface) {
	t.Helper()
	restore := resources.SetPollIntervalForTesting(10 * time.Millisecond)
	t.Cleanup(restore)
	opts.Profiles = bmTestProfiles()
	if opts.SSHKeys == nil {
		opts.SSHKeys = []string{"mine"}
	}
	if opts.Step == 0 {
		opts.Step = 15 * time.Millisecond
	}
	srv := bmapi.New(opts)
	t.Cleanup(srv.Close)
	cs, err := clientset.NewForConfig(gpupaas.NewConfig(srv.URL, "test"))
	if err != nil {
		t.Fatal(err)
	}
	return srv, cs
}

// bmResource renders a workspace-scoped machine. specExtra and extra are inserted inside spec and at
// the top level of the resource.
func bmResource(label, name, specExtra, extra string) string {
	return fmt.Sprintf(`
resource "gpupaas_bare_metal_instance" %q {
  metadata = {
    name      = %q
    project   = %q
    workspace = %q
  }
  spec = {
    type    = "bm-l40s"
    image   = "ubuntu-22"
    ssh_key = "mine"
%s
  }
%s
}
`, label, name, testProject, bmWorkspace, specExtra, extra)
}

func bmConfig(url, specExtra, extra string) string {
	return providerBlock(url) + bmResource("b", "b1", specExtra, extra)
}

func checkBM(srv *bmapi.Server, check func(*apiv1.BareMetal) error) resource.TestCheckFunc {
	return func(_ *terraform.State) error {
		b := srv.BareMetal(testProject, bmWorkspace, "b1")
		if b == nil {
			return fmt.Errorf("bare metal b1 does not exist on the API")
		}
		return check(b)
	}
}

func checkBMGone(srv *bmapi.Server) resource.TestCheckFunc {
	return func(_ *terraform.State) error {
		for _, name := range []string{"b1", "b2"} {
			if srv.Exists(testProject, bmWorkspace, name) {
				return fmt.Errorf("bare metal %s still exists (phase %s)", name, srv.BareMetal(testProject, bmWorkspace, name).Status.Phase())
			}
		}
		return nil
	}
}

func countBM(srv *bmapi.Server, substr string) int {
	n := 0
	for _, r := range srv.Requests() {
		if strings.Contains(r, substr) {
			n++
		}
	}
	return n
}

// Day-2 edits the console offers on a running machine: Public IP Assignment and Security Group. Each step is an
// in-place update of the same machine (never a delete) and keeps everything else on it.
func TestAccBareMetal_publicIPAndSecurityGroupEditedInPlace(t *testing.T) {
	srv, _ := newBMMock(t, bmapi.Options{})
	step := func(sg string, publicIP bool) resource.TestStep {
		extra, sgCheck := "", resource.TestCheckNoResourceAttr(bmAddr, "spec.security_group")
		if sg != "" {
			extra, sgCheck = fmt.Sprintf("    security_group = %q\n", sg), resource.TestCheckResourceAttr(bmAddr, "spec.security_group", sg)
		}
		extra += fmt.Sprintf("    assign_public_ip = %t", publicIP)
		return resource.TestStep{
			Config: bmConfig(srv.URL, extra, ""),
			Check: resource.ComposeAggregateTestCheckFunc(
				sgCheck,
				resource.TestCheckResourceAttr(bmAddr, "spec.assign_public_ip", fmt.Sprintf("%t", publicIP)),
				resource.TestCheckResourceAttr(bmAddr, "status.phase", "running"),
				checkBM(srv, func(b *apiv1.BareMetal) error {
					if b.Spec.SecurityGroup != sg || b.Spec.AssignPublicIP != publicIP {
						return fmt.Errorf("API has security group %q, public ip %v; want %q, %v", b.Spec.SecurityGroup, b.Spec.AssignPublicIP, sg, publicIP)
					}
					if b.Spec.Type != "bm-l40s" || b.Spec.Image != "ubuntu-22" || b.Spec.SSHKey != "mine" {
						return fmt.Errorf("the edit changed other settings: %+v", b.Spec)
					}
					return nil
				}),
				func(_ *terraform.State) error {
					if n := countBM(srv, "DELETE "); n != 0 {
						return fmt.Errorf("an edit deleted the machine (%d deletes)", n)
					}
					return nil
				},
			),
		}
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy:             checkBMGone(srv),
		Steps:                    []resource.TestStep{step("sg1", false), step("sg2", true), step("", false), step("sg1", true)},
	})
}

func TestAccBareMetal_lifecycle(t *testing.T) {
	srv, _ := newBMMock(t, bmapi.Options{})
	var firstInstance string
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy:             checkBMGone(srv),
		Steps: []resource.TestStep{
			{
				// Create provisions the machine and waits until it runs.
				Config: bmConfig(srv.URL, "", ""),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(bmAddr, "id", "system-catalog/ws/b1"),
					resource.TestCheckResourceAttr(bmAddr, "status.phase", "running"),
					resource.TestCheckResourceAttr(bmAddr, "status.power_state", "running"),
					resource.TestCheckResourceAttr(bmAddr, "status.action", "deploy"),
					resource.TestCheckResourceAttr(bmAddr, "status.host_name", "b1-host"),
					resource.TestCheckResourceAttr(bmAddr, "status.user_name", "ubuntu"),
					// The SKU's only datacenter is filled in, and what the platform derives is reported.
					resource.TestCheckResourceAttr(bmAddr, "spec.datacenter", "dc1"),
					resource.TestCheckResourceAttr(bmAddr, "spec.provisioner_type", "bcm"),
					resource.TestCheckResourceAttr(bmAddr, "spec.gpu_name", "L40S"),
					resource.TestCheckResourceAttr(bmAddr, "spec.assign_public_ip", "false"),
					resource.TestCheckResourceAttr(bmAddr, "spec.contract_term", "0"),
					resource.TestCheckResourceAttrSet(bmAddr, "spec.compute_instance_id"),
					// The platform's default sharing is not shown as a change.
					resource.TestCheckNoResourceAttr(bmAddr, "spec.sharing.share_mode"),
					func(s *terraform.State) error {
						firstInstance = s.RootModule().Resources[bmAddr].Primary.Attributes["spec.compute_instance_id"]
						return nil
					},
					checkBM(srv, func(b *apiv1.BareMetal) error {
						if b.Spec.Type != "bm-l40s" || b.Spec.Image != "ubuntu-22" || b.Spec.SSHKey != "mine" || b.Spec.Datacenter != "dc1" {
							return fmt.Errorf("spec = %+v", b.Spec)
						}
						if b.Spec.Sharing == nil || b.Spec.Sharing.ShareMode != "None" {
							return fmt.Errorf("sharing = %#v, want None", b.Spec.Sharing)
						}
						return nil
					}),
				),
			},
			{
				// An in-place update: stored, the machine is not touched.
				Config: bmConfig(srv.URL, `    security_group = "sg1"`, ""),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(bmAddr, "spec.security_group", "sg1"),
					resource.TestCheckResourceAttr(bmAddr, "status.phase", "running"),
					func(s *terraform.State) error {
						if got := s.RootModule().Resources[bmAddr].Primary.Attributes["spec.compute_instance_id"]; got != firstInstance {
							return fmt.Errorf("an in-place update replaced the machine: compute_instance_id %q, was %q", got, firstInstance)
						}
						return nil
					},
					checkBM(srv, func(b *apiv1.BareMetal) error {
						if b.Spec.SecurityGroup != "sg1" {
							return fmt.Errorf("security group = %q", b.Spec.SecurityGroup)
						}
						return nil
					}),
					func(_ *terraform.State) error {
						if n := countBM(srv, "DELETE "); n != 0 {
							return fmt.Errorf("an in-place update deleted the machine (%d deletes)", n)
						}
						return nil
					},
				),
			},
			{
				Config: bmConfig(srv.URL, `    security_group = "sg1"`, `  desired_action = "stop"`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(bmAddr, "status.phase", "stopped"),
					resource.TestCheckResourceAttr(bmAddr, "status.power_state", "stopped"),
					resource.TestCheckResourceAttr(bmAddr, "status.action", "stop"),
					func(_ *terraform.State) error {
						if countBM(srv, "/action/stop") != 1 {
							return fmt.Errorf("stop was not requested exactly once: %v", srv.Requests())
						}
						return nil
					},
				),
			},
			{
				Config: bmConfig(srv.URL, `    security_group = "sg1"`, `  desired_action = "start"`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(bmAddr, "status.phase", "running"),
					resource.TestCheckResourceAttr(bmAddr, "status.power_state", "running"),
					resource.TestCheckResourceAttr(bmAddr, "status.action", "start"),
				),
			},
			{
				Config: bmConfig(srv.URL, `    security_group = "sg1"`, `  desired_action = "reboot"`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(bmAddr, "status.phase", "running"),
					resource.TestCheckResourceAttr(bmAddr, "status.action", "reboot"),
					func(_ *terraform.State) error {
						if countBM(srv, "/action/reboot") != 1 {
							return fmt.Errorf("reboot was not requested exactly once: %v", srv.Requests())
						}
						return nil
					},
				),
			},
			{
				// The names the platform lists for BCM machines: sent as "Power cycle" and "Power reset".
				Config: bmConfig(srv.URL, `    security_group = "sg1"`, `  desired_action = "power_cycle"`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(bmAddr, "status.phase", "running"),
					resource.TestCheckResourceAttr(bmAddr, "status.action", "Power cycle"),
					func(_ *terraform.State) error {
						if countBM(srv, "/action/Power cycle") != 1 {
							return fmt.Errorf("power cycle was not requested exactly once: %v", srv.Requests())
						}
						return nil
					},
				),
			},
			{
				Config: bmConfig(srv.URL, `    security_group = "sg1"`, `  desired_action = "power_reset"`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(bmAddr, "status.phase", "running"),
					resource.TestCheckResourceAttr(bmAddr, "status.action", "Power reset"),
				),
			},
			{
				// An existing machine can be imported.
				ResourceName:  bmAddr,
				ImportState:   true,
				ImportStateId: "system-catalog/ws/b1",
				ImportStateCheck: func(states []*terraform.InstanceState) error {
					if len(states) != 1 {
						return fmt.Errorf("imported %d resources", len(states))
					}
					a := states[0].Attributes
					want := map[string]string{
						"spec.type":             "bm-l40s",
						"spec.image":            "ubuntu-22",
						"spec.ssh_key":          "mine",
						"spec.datacenter":       "dc1",
						"spec.security_group":   "sg1",
						"spec.provisioner_type": "bcm",
						"status.phase":          "running",
						"metadata.workspace":    "ws",
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

func TestAccBareMetal_changingTheImageReplacesTheMachine(t *testing.T) {
	srv, _ := newBMMock(t, bmapi.Options{})
	var first string
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy:             checkBMGone(srv),
		Steps: []resource.TestStep{
			{
				Config: bmConfig(srv.URL, "", ""),
				Check: func(s *terraform.State) error {
					first = s.RootModule().Resources[bmAddr].Primary.Attributes["spec.compute_instance_id"]
					return nil
				},
			},
			{
				Config: strings.Replace(bmConfig(srv.URL, "", ""), `image   = "ubuntu-22"`, `image   = "ubuntu-24"`, 1),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(bmAddr, "spec.image", "ubuntu-24"),
					resource.TestCheckResourceAttr(bmAddr, "status.phase", "running"),
					func(s *terraform.State) error {
						if got := s.RootModule().Resources[bmAddr].Primary.Attributes["spec.compute_instance_id"]; got == first || got == "" {
							return fmt.Errorf("machine was not replaced: compute_instance_id %q, was %q", got, first)
						}
						if n := countBM(srv, "DELETE "); n != 1 {
							return fmt.Errorf("%d deletes, want 1", n)
						}
						return nil
					},
				),
			},
		},
	})
}

func TestAccBareMetal_clientSideSettingsDoNotReapplyTheMachine(t *testing.T) {
	srv, _ := newBMMock(t, bmapi.Options{})
	var posts int
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy:             checkBMGone(srv),
		Steps: []resource.TestStep{
			{
				Config: bmConfig(srv.URL, "", ""),
				Check: func(_ *terraform.State) error {
					posts = countBM(srv, "POST ")
					return nil
				},
			},
			{
				// Only timeouts and desired_action = none change: Terraform updates its state and sends nothing.
				Config: bmConfig(srv.URL, "", "  desired_action = \"none\"\n  timeouts = { create = \"30m\" }"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(bmAddr, "status.phase", "running"),
					func(_ *terraform.State) error {
						if n := countBM(srv, "POST "); n != posts {
							return fmt.Errorf("a client-side change re-applied the machine: %d POSTs, want %d", n, posts)
						}
						return nil
					},
				),
			},
		},
	})
}

func TestAccBareMetal_passwordIsWriteOnly(t *testing.T) {
	srv, _ := newBMMock(t, bmapi.Options{})
	cfg := func(password string) string {
		return providerBlock(srv.URL) + fmt.Sprintf(`
resource "gpupaas_bare_metal_instance" "b" {
  metadata = { name = "b1", project = %q, workspace = %q }
  spec = {
    type          = "bm-l40s"
    image         = "ubuntu-22"
    user_password = %q
  }
}
`, testProject, bmWorkspace, password)
	}
	rawPassword := func(want string) resource.TestCheckFunc {
		return func(_ *terraform.State) error {
			sp := srv.RawSpec(testProject, bmWorkspace, "b1")
			if sp == nil {
				return fmt.Errorf("bare metal b1 does not exist on the API")
			}
			if sp.UserPassword != want {
				return fmt.Errorf("password on the API differs from the configured one")
			}
			return nil
		}
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy:             checkBMGone(srv),
		Steps: []resource.TestStep{
			{
				// A password is enough to log in; no key is needed. The API never returns it, and
				// the plan after apply must still be empty.
				Config: cfg("first-secret"),
				Check: resource.ComposeAggregateTestCheckFunc(
					rawPassword("first-secret"),
					resource.TestCheckResourceAttr(bmAddr, "spec.user_password", "first-secret"),
					resource.TestCheckNoResourceAttr(bmAddr, "spec.ssh_key"),
				),
			},
			{
				// Changing it is sent to the API.
				Config: cfg("second-secret"),
				Check:  rawPassword("second-secret"),
			},
		},
	})
}

func TestAccBareMetal_projectScope(t *testing.T) {
	srv, _ := newBMMock(t, bmapi.Options{})
	cfg := providerBlock(srv.URL) + fmt.Sprintf(`
resource "gpupaas_bare_metal_instance" "b" {
  metadata = { name = "b1", project = %q }
  spec = {
    type    = "bm-l40s"
    image   = "ubuntu-24"
    ssh_key = "mine"
  }
}
`, testProject)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy: func(_ *terraform.State) error {
			if srv.Exists(testProject, "", "b1") {
				return fmt.Errorf("bare metal still exists")
			}
			return nil
		},
		Steps: []resource.TestStep{{
			Config: cfg,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr(bmAddr, "id", "system-catalog/b1"),
				resource.TestCheckResourceAttr(bmAddr, "status.phase", "running"),
				resource.TestCheckResourceAttr(bmAddr, "spec.image", "ubuntu-24"),
			),
		}},
	})
}

func TestAccBareMetal_planTimeValidation(t *testing.T) {
	srv, _ := newBMMock(t, bmapi.Options{})
	noip := func(extra string) string {
		return strings.Replace(bmConfig(srv.URL, extra, ""), `type    = "bm-l40s"`, `type    = "bm-noip"`, 1)
	}
	cases := []struct {
		name string
		cfg  string
		want string
	}{
		{"image", strings.Replace(bmConfig(srv.URL, "", ""), `"ubuntu-22"`, `"ubuntu-99"`, 1), `allows these image values: ubuntu-22, ubuntu-24; got "ubuntu-99"`},
		{"datacenter", bmConfig(srv.URL, `    datacenter = "mars-dc"`, ""), `allows these datacenter values: dc1; got "mars-dc"`},
		{"datacenter typo", noip(`    datacenter = "dc3"`), `Did you mean "dc`},
		{"public ip", noip("    assign_public_ip = true"), `does not assign public IPs`},
		{"contract term", bmConfig(srv.URL, "    contract_term = 7", ""), `offers these contract terms (months): 12, 36; got 7`},
		{"no way to log in", strings.Replace(bmConfig(srv.URL, "", ""), `ssh_key = "mine"`, "", 1), `Set spec.ssh_key, spec.user_password, or both`},
		{"desired action", bmConfig(srv.URL, "", `  desired_action = "explode"`), `value must be one of`},
	}
	var steps []resource.TestStep
	for _, c := range cases {
		steps = append(steps, resource.TestStep{Config: c.cfg, PlanOnly: true, ExpectError: errRe(c.want)})
	}
	resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: protoFactories(), Steps: steps})

	// Nothing was created while validating.
	if n := countBM(srv, "POST "); n != 0 {
		t.Fatalf("validation created machines: %v", srv.Requests())
	}
}

func TestAccBareMetal_skuWithoutAgentIsOnlyAWarning(t *testing.T) {
	// The agent list is not a health check, so an empty one must not block: the plan succeeds and
	// the machine is created (the mock has no agents to fail on).
	srv, _ := newBMMock(t, bmapi.Options{})
	cfg := strings.Replace(bmConfig(srv.URL, "", ""), `type    = "bm-l40s"`, `type    = "bm-noagent"`, 1)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy:             checkBMGone(srv),
		Steps:                    []resource.TestStep{{Config: cfg, Check: resource.TestCheckResourceAttr(bmAddr, "status.phase", "running")}},
	})
}

func TestAccBareMetal_provisionFailureShowsTheWorkflowCauseAndIsCleanedUp(t *testing.T) {
	// The instance only says "due to an internal error"; the cause is on the compute instance behind it.
	srv, _ := newBMMock(t, bmapi.Options{})
	srv.FailProvision = func(string) string { return "x" }
	srv.FailureCause = func(string) string { return "no healthy agents found. assigned agents: agent-a" }
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy:             checkBMGone(srv),
		Steps: []resource.TestStep{{
			Config:      bmConfig(srv.URL, "", ""),
			ExpectError: errRe(`due to an internal error`),
		}, {
			Config:      bmConfig(srv.URL, "", ""),
			ExpectError: errRe(`no healthy agents found. assigned agents: agent-a`),
		}},
	})
}

func TestAccBareMetal_noCapacityIsReported(t *testing.T) {
	srv, _ := newBMMock(t, bmapi.Options{Capacity: 1})
	cfg := providerBlock(srv.URL) + bmResource("b", "b1", "", "") + bmResource("c", "b2", "", "")
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy:             checkBMGone(srv),
		Steps: []resource.TestStep{{
			Config:      cfg,
			ExpectError: errRe(`no capacity available right now`),
		}},
	})
}

func TestAccBareMetal_teardownFailureCanBeRetried(t *testing.T) {
	srv, _ := newBMMock(t, bmapi.Options{})
	var calls atomic.Int32
	srv.FailDelete = func(string) string {
		if calls.Add(1) == 1 {
			return "BMC did not answer the power-off request"
		}
		return ""
	}
	cfg := bmConfig(srv.URL, "", "")
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy:             checkBMGone(srv),
		Steps: []resource.TestStep{
			{Config: cfg},
			{Config: cfg, Destroy: true, ExpectError: errRe(`BMC did not answer the power-off request`)},
		},
	})
}

func TestAccBareMetal_destroyWaitsForTheBackingInstance(t *testing.T) {
	// The record vanishes before the compute instance behind it; a workspace cannot be deleted
	// until that instance is gone, so destroy must wait for it.
	srv, _ := newBMMock(t, bmapi.Options{MirrorLinger: 400 * time.Millisecond})
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy: func(_ *terraform.State) error {
			if srv.Exists(testProject, bmWorkspace, "b1") {
				return fmt.Errorf("bare metal still exists")
			}
			if srv.MirrorExists(testProject, bmWorkspace, "b1") {
				return fmt.Errorf("destroy returned while the compute instance behind the machine still existed")
			}
			return nil
		},
		Steps: []resource.TestStep{{Config: bmConfig(srv.URL, "", "")}},
	})
}

func TestAccBareMetal_deletedOutsideTerraformIsRecreated(t *testing.T) {
	srv, cs := newBMMock(t, bmapi.Options{})
	cfg := bmConfig(srv.URL, "", "")
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy:             checkBMGone(srv),
		Steps: []resource.TestStep{
			{Config: cfg},
			{
				PreConfig: func() {
					ctx := context.Background()
					c := cs.V1alpha1().Workspaces(testProject).BareMetals(bmWorkspace)
					if err := c.Delete(ctx, "b1", gpupaas.DeleteOptions{}); err != nil {
						t.Fatal(err)
					}
					for i := 0; i < 200 && srv.Exists(testProject, bmWorkspace, "b1"); i++ {
						if _, err := c.Get(ctx, "b1", gpupaas.GetOptions{}); gpupaas.IsNotFound(err) {
							break
						}
						time.Sleep(10 * time.Millisecond)
					}
				},
				Config: cfg,
				Check:  resource.TestCheckResourceAttr(bmAddr, "status.phase", "running"),
			},
		},
	})
}

func TestAccBareMetal_dataSources(t *testing.T) {
	srv, _ := newBMMock(t, bmapi.Options{})
	cfg := bmConfig(srv.URL, "", "") + fmt.Sprintf(`
data "gpupaas_bare_metal_instance" "d" {
  name      = gpupaas_bare_metal_instance.b.metadata.name
  project   = %q
  workspace = %q
}

data "gpupaas_bare_metal_sku" "p" {
  name    = "bm-l40s"
  project = %q
}
`, testProject, bmWorkspace, testProject)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy:             checkBMGone(srv),
		Steps: []resource.TestStep{{
			Config: cfg,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("data.gpupaas_bare_metal_instance.d", "status.phase", "running"),
				resource.TestCheckResourceAttr("data.gpupaas_bare_metal_instance.d", "spec.type", "bm-l40s"),
				resource.TestCheckResourceAttr("data.gpupaas_bare_metal_instance.d", "spec.image", "ubuntu-22"),
				resource.TestCheckResourceAttr("data.gpupaas_bare_metal_instance.d", "spec.datacenter", "dc1"),
				resource.TestCheckResourceAttr("data.gpupaas_bare_metal_instance.d", "status.host_name", "b1-host"),
				resource.TestCheckResourceAttr("data.gpupaas_bare_metal_sku.p", "id", "system-catalog/bm-l40s"),
				resource.TestCheckResourceAttr("data.gpupaas_bare_metal_sku.p", "images.#", "2"),
				resource.TestCheckResourceAttr("data.gpupaas_bare_metal_sku.p", "default_image", "ubuntu-22"),
				resource.TestCheckResourceAttr("data.gpupaas_bare_metal_sku.p", "datacenters.0", "dc1"),
				resource.TestCheckResourceAttr("data.gpupaas_bare_metal_sku.p", "assign_public_ip", "CustomerSelectable"),
				resource.TestCheckResourceAttr("data.gpupaas_bare_metal_sku.p", "contract_terms.1", "36"),
				resource.TestCheckResourceAttr("data.gpupaas_bare_metal_sku.p", "provisioner_type", "bcm"),
				resource.TestCheckResourceAttr("data.gpupaas_bare_metal_sku.p", "inventory_config.0.key", "server_type"),
				resource.TestCheckResourceAttr("data.gpupaas_bare_metal_sku.p", "agents.0", "agent-1"),
			),
		}},
	})
}

func TestAccBareMetal_provisionFailureShowsTheMachinesOwnDetailWhenTheComputeInstanceIsGone(t *testing.T) {
	// After a teardown the compute instance behind a machine can be gone, while the failed record
	// still names the cause in status.reasonDetail.
	srv, _ := newBMMock(t, bmapi.Options{})
	srv.FailProvision = func(string) string { return "x" }
	srv.ReasonDetail = func(string) string { return "node-provision: Bond configuration failed (exit=124)." }
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy:             checkBMGone(srv),
		Steps: []resource.TestStep{{
			Config:      bmConfig(srv.URL, "", ""),
			ExpectError: errRe(`Bond configuration failed (exit=124).`),
		}},
	})
}

func TestAccBareMetal_parkedDeleteIsExplainedAndRefusedRetriesAreToo(t *testing.T) {
	// Live behaviour: a delete that cannot finalize ends delete_pending, and the platform then
	// answers every further DELETE from an end user with 412 "contact your administrator".
	srv, _ := newBMMock(t, bmapi.Options{})
	srv.ParkDelete = func(string) bool { return true }
	cfg := bmConfig(srv.URL, "", "")
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy:             checkBMGone(srv),
		Steps: []resource.TestStep{
			{Config: cfg},
			// first destroy: parked (the warning about the administrator accompanies the error)
			{Config: cfg, Destroy: true, ExpectError: errRe(`Status "delete_pending" after "destroy"`)},
			// second destroy: refused with 412, reported with the administrator hint
			{Config: cfg, Destroy: true, ExpectError: errRe(`end users cannot delete it again`)},
			// an administrator removes the record: the resource is gone on the next refresh
			{
				PreConfig:          func() { srv.AdminDelete(testProject, bmWorkspace, "b1") },
				Config:             cfg,
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
		},
	})
}

// An initial desired_action the platform refuses must not fail the create: Terraform would taint a
// machine that is up, and the next apply would destroy and rebuild it.
func TestAccBareMetal_refusedInitialActionDoesNotTaintTheMachine(t *testing.T) {
	srv, _ := newBMMock(t, bmapi.Options{UnsupportedActions: []string{"reboot"}})
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy:             checkBMGone(srv),
		Steps: []resource.TestStep{{
			Config: bmConfig(srv.URL, "", `  desired_action = "reboot"`),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr(bmAddr, "status.phase", "running"),
				resource.TestCheckResourceAttr(bmAddr, "desired_action", "reboot"),
				func(_ *terraform.State) error {
					if countBM(srv, "/action/reboot") != 1 {
						return fmt.Errorf("the refused action was not requested exactly once: %v", srv.Requests())
					}
					return nil
				},
			),
		}},
	})
}

// A refused action on an existing machine is an error, and the state keeps the previous
// desired_action, so the next plan shows the change again instead of forgetting the action.
func TestAccBareMetal_refusedActionOnUpdateIsRetried(t *testing.T) {
	srv, _ := newBMMock(t, bmapi.Options{UnsupportedActions: []string{"reboot"}})
	withAction := bmConfig(srv.URL, "", `  desired_action = "reboot"`)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy:             checkBMGone(srv),
		Steps: []resource.TestStep{
			{Config: bmConfig(srv.URL, "", "")},
			{Config: withAction, ExpectError: errRe(`not supported for this instance`)},
			{Config: withAction, PlanOnly: true, ExpectNonEmptyPlan: true},
		},
	})
}

// A name that is taken is refused up front, so the instance a failed create leaves behind can only
// be one this apply created.
func TestAccBareMetal_createRefusesAnExistingName(t *testing.T) {
	srv, _ := newBMMock(t, bmapi.Options{})
	one := bmConfig(srv.URL, "", "")
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy:             checkBMGone(srv),
		Steps: []resource.TestStep{
			{Config: one},
			{Config: one + bmResource("dup", "b1", "", ""), ExpectError: errRe(`Bare metal already exists`)},
		},
	})
}
