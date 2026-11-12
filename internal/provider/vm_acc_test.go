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
	"github.com/gpupaas-ai/gpupaas-go/mock/vmapi"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/gpupaas-ai/terraform-provider-gpupaas/internal/provider/resources"
)

// These tests run the real terraform binary against the provider and an in-process mock of the
// first-class virtual machine API (dev.envmgmt.io/v1). Run them with TF_ACC=1.

const (
	vmAddr      = "gpupaas_virtual_machine.v"
	vmWorkspace = "ws"
)

func vmTestProfiles() []apiv1.VMProfile {
	disk := &apiv1.VMBootDisk{MinSize: "15", MaxSize: "2000", Increment: "10", Unit: "gb"}
	agents := &apiv1.VMProfileGlobalSettings{Agents: []string{"agent-1"}}
	return []apiv1.VMProfile{
		{
			// like the live vm-test, with a second preset
			Metadata: apiv1.ObjectMeta{Name: "vm-small"},
			Spec: apiv1.VMProfileSpec{
				Image: []string{"ubuntu-22", "ubuntu-24"}, ImageDefault: "ubuntu-22", Datacenter: []string{"dc1"},
				CPUPreset: []apiv1.VMPreset{{Name: "Small", CPU: "3", Memory: "3"}, {Name: "Medium", CPU: "6", Memory: "12"}},
				BootDisk:  disk, PublicIP: "Optional", InstanceType: "ONDEMAND", TemplateVersion: "v3.1.41",
			},
			Status: apiv1.VMProfileStatus{Status: "success", GlobalSettings: agents},
		},
		{
			// a single preset: the size needs no choosing
			Metadata: apiv1.ObjectMeta{Name: "vm-single"},
			Spec: apiv1.VMProfileSpec{
				Image: []string{"ubuntu-22"}, Datacenter: []string{"dc1"}, CPUPreset: []apiv1.VMPreset{{Name: "Only", CPU: "4", Memory: "8"}}, BootDisk: disk, PublicIP: "Optional",
			},
			Status: apiv1.VMProfileStatus{Status: "success", GlobalSettings: agents},
		},
		{
			Metadata: apiv1.ObjectMeta{Name: "vm-noip"},
			Spec: apiv1.VMProfileSpec{
				Image: []string{"ubuntu-22"}, Datacenter: []string{"dc1", "dc2"}, CPUPreset: []apiv1.VMPreset{{Name: "Small", CPU: "3", Memory: "3"}}, PublicIP: "None",
			},
			Status: apiv1.VMProfileStatus{Status: "success", GlobalSettings: agents},
		},
		{
			Metadata: apiv1.ObjectMeta{Name: "vm-always"},
			Spec: apiv1.VMProfileSpec{
				Image: []string{"ubuntu-22"}, Datacenter: []string{"dc1"}, CPUPreset: []apiv1.VMPreset{{Name: "Small", CPU: "3", Memory: "3"}}, PublicIP: "Always",
			},
			Status: apiv1.VMProfileStatus{Status: "success", GlobalSettings: agents},
		},
		{
			Metadata: apiv1.ObjectMeta{Name: "vm-gpu"},
			Spec: apiv1.VMProfileSpec{
				Image: []string{"ubuntu-22"}, Datacenter: []string{"dc1"}, GPUsEnabled: true, GPUModel: "L40S", GPUVendor: "nvidia",
				CPUPreset: []apiv1.VMPreset{{Name: "ignored", CPU: "1", Memory: "1"}},
				GPUPreset: []apiv1.VMPreset{{Name: "G1", CPU: "8", Memory: "64", GPU: "1"}, {Name: "G2", CPU: "16", Memory: "128", GPU: "2"}}, PublicIP: "Optional",
			},
			Status: apiv1.VMProfileStatus{Status: "success", GlobalSettings: agents},
		},
		{
			// no agent assigned: provisioning cannot work
			Metadata: apiv1.ObjectMeta{Name: "vm-noagent"},
			Spec: apiv1.VMProfileSpec{
				Image: []string{"ubuntu-22"}, Datacenter: []string{"dc1"}, CPUPreset: []apiv1.VMPreset{{Name: "Small", CPU: "3", Memory: "3"}}, PublicIP: "Optional",
			},
			Status: apiv1.VMProfileStatus{Status: "success", GlobalSettings: &apiv1.VMProfileGlobalSettings{}},
		},
	}
}

func newVMMock(t *testing.T, opts vmapi.Options) (*vmapi.Server, clientset.Interface) {
	t.Helper()
	restore := resources.SetPollIntervalForTesting(10 * time.Millisecond)
	t.Cleanup(restore)
	opts.Profiles = vmTestProfiles()
	if opts.SSHKeys == nil {
		opts.SSHKeys = []string{"mine"}
	}
	if opts.Step == 0 {
		opts.Step = 15 * time.Millisecond
	}
	srv := vmapi.New(opts)
	t.Cleanup(srv.Close)
	cs, err := clientset.NewForConfig(gpupaas.NewConfig(srv.URL, "test"))
	if err != nil {
		t.Fatal(err)
	}
	return srv, cs
}

// vmResource renders a workspace-scoped VM. specExtra and extra are inserted inside spec and at the
// top level of the resource.
func vmResource(label, name, specExtra, extra string) string {
	return fmt.Sprintf(`
resource "gpupaas_virtual_machine" %q {
  metadata = {
    name      = %q
    project   = %q
    workspace = %q
  }
  spec = {
    type    = "vm-small"
    preset  = "Small"
    image   = "ubuntu-22"
    ssh_key = "mine"
%s
  }
%s
}
`, label, name, testProject, vmWorkspace, specExtra, extra)
}

func vmConfig(url, specExtra, extra string) string {
	return providerBlock(url) + vmResource("v", "v1", specExtra, extra)
}

func checkVM(srv *vmapi.Server, check func(*apiv1.VMInstance) error) resource.TestCheckFunc {
	return func(_ *terraform.State) error {
		v := srv.VM(testProject, vmWorkspace, "v1")
		if v == nil {
			return fmt.Errorf("virtual machine v1 does not exist on the API")
		}
		return check(v)
	}
}

func checkVMGone(srv *vmapi.Server) resource.TestCheckFunc {
	return func(_ *terraform.State) error {
		for _, name := range []string{"v1", "v2"} {
			if srv.Exists(testProject, vmWorkspace, name) {
				return fmt.Errorf("virtual machine %s still exists (phase %s)", name, srv.VM(testProject, vmWorkspace, name).Status.Phase())
			}
		}
		return nil
	}
}

func countVM(srv *vmapi.Server, substr string) int {
	n := 0
	for _, r := range srv.Requests() {
		if strings.Contains(r, substr) {
			n++
		}
	}
	return n
}

func TestAccVM_lifecycle(t *testing.T) {
	srv, _ := newVMMock(t, vmapi.Options{})
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy:             checkVMGone(srv),
		Steps: []resource.TestStep{
			{
				// Create provisions the VM and waits until it runs.
				Config: vmConfig(srv.URL, "", ""),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(vmAddr, "id", "system-catalog/ws/v1"),
					resource.TestCheckResourceAttr(vmAddr, "status.phase", "running"),
					resource.TestCheckResourceAttr(vmAddr, "status.power_state", "running"),
					resource.TestCheckResourceAttr(vmAddr, "status.action", "deploy"),
					resource.TestCheckResourceAttr(vmAddr, "status.host_name", "v1-host"),
					resource.TestCheckResourceAttr(vmAddr, "status.user_name", "ubuntu"),
					resource.TestCheckResourceAttr(vmAddr, "status.instance_id", "i-v1"),
					resource.TestCheckResourceAttr(vmAddr, "status.server_host", "host-1"),
					// "No public IP configured" is the platform's text for no address, not an address
					resource.TestCheckNoResourceAttr(vmAddr, "status.public_ip"),
					// a new VM gets the SKU's minimum boot disk, not the platform's own (larger) default
					resource.TestCheckResourceAttr(vmAddr, "spec.boot_disk_size", "15"),
					// The preset resolves to the SKU's size, and the SKU's only datacenter is filled in.
					resource.TestCheckResourceAttr(vmAddr, "spec.cpu_count", "3"),
					resource.TestCheckResourceAttr(vmAddr, "spec.memory", "3"),
					resource.TestCheckResourceAttr(vmAddr, "spec.datacenter", "dc1"),
					resource.TestCheckResourceAttr(vmAddr, "spec.assign_public_ip", "false"),
					// The platform's default sharing is not shown as a change.
					resource.TestCheckNoResourceAttr(vmAddr, "spec.sharing.share_mode"),
					checkVM(srv, func(v *apiv1.VMInstance) error {
						if v.Spec.Type != "vm-small" || v.Spec.CPUCount != "3" || v.Spec.Memory != "3" || v.Spec.Image != "ubuntu-22" || v.Spec.SSHKey != "mine" || v.Spec.Datacenter != "dc1" {
							return fmt.Errorf("spec = %+v", v.Spec)
						}
						if v.Spec.Sharing == nil || v.Spec.Sharing.ShareMode != "None" {
							return fmt.Errorf("sharing = %#v, want None", v.Spec.Sharing)
						}
						if v.Spec.BootDiskSize != 15 {
							return fmt.Errorf("boot_disk_size sent as %d, want the SKU minimum 15", v.Spec.BootDiskSize)
						}
						return nil
					}),
				),
			},
			{
				// An in-place update: stored, the VM is not touched.
				Config: vmConfig(srv.URL, `    security_group = "sg1"`, ""),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(vmAddr, "spec.security_group", "sg1"),
					resource.TestCheckResourceAttr(vmAddr, "status.phase", "running"),
					checkVM(srv, func(v *apiv1.VMInstance) error {
						if v.Spec.SecurityGroup != "sg1" {
							return fmt.Errorf("security group = %q", v.Spec.SecurityGroup)
						}
						return nil
					}),
					func(_ *terraform.State) error {
						if n := countVM(srv, "DELETE "); n != 0 {
							return fmt.Errorf("an in-place update deleted the VM (%d deletes)", n)
						}
						return nil
					},
				),
			},
			{
				Config: vmConfig(srv.URL, `    security_group = "sg1"`, `  desired_action = "stop"`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(vmAddr, "status.phase", "stopped"),
					resource.TestCheckResourceAttr(vmAddr, "status.power_state", "stopped"),
					resource.TestCheckResourceAttr(vmAddr, "status.action", "stop"),
					func(_ *terraform.State) error {
						if countVM(srv, "/action/stop") != 1 {
							return fmt.Errorf("stop was not requested exactly once: %v", srv.Requests())
						}
						return nil
					},
				),
			},
			{
				Config: vmConfig(srv.URL, `    security_group = "sg1"`, `  desired_action = "start"`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(vmAddr, "status.phase", "running"),
					resource.TestCheckResourceAttr(vmAddr, "status.power_state", "running"),
					resource.TestCheckResourceAttr(vmAddr, "status.action", "start"),
				),
			},
			{
				Config: vmConfig(srv.URL, `    security_group = "sg1"`, `  desired_action = "reboot"`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(vmAddr, "status.phase", "running"),
					resource.TestCheckResourceAttr(vmAddr, "status.action", "reboot"),
					func(_ *terraform.State) error {
						if countVM(srv, "/action/reboot") != 1 {
							return fmt.Errorf("reboot was not requested exactly once: %v", srv.Requests())
						}
						return nil
					},
				),
			},
			{
				// An existing VM can be imported; the platform keeps the size, not the preset name, so the import names the SKU's matching preset.
				ResourceName:  vmAddr,
				ImportState:   true,
				ImportStateId: "system-catalog/ws/v1",
				ImportStateCheck: func(states []*terraform.InstanceState) error {
					if len(states) != 1 {
						return fmt.Errorf("imported %d resources", len(states))
					}
					a := states[0].Attributes
					want := map[string]string{
						"spec.type":           "vm-small",
						"spec.cpu_count":      "3",
						"spec.memory":         "3",
						"spec.image":          "ubuntu-22",
						"spec.ssh_key":        "mine",
						"spec.datacenter":     "dc1",
						"spec.security_group": "sg1",
						"status.phase":        "running",
						"metadata.workspace":  "ws",
					}
					for k, v := range want {
						if a[k] != v {
							return fmt.Errorf("imported %s = %q, want %q", k, a[k], v)
						}
					}
					if a["spec.preset"] != "Small" {
						return fmt.Errorf("spec.preset after import = %q, want the SKU's matching preset Small", a["spec.preset"])
					}
					return nil
				},
			},
		},
	})
}

// Day-2 security group edit, as the console's Security Group "Edit" does it: change the group, then remove it.
// Each step is an in-place update of the same VM (never a delete) and keeps everything else on the VM.
func TestAccVM_securityGroupChangedAndRemovedInPlace(t *testing.T) {
	srv, _ := newVMMock(t, vmapi.Options{})
	step := func(sg string) resource.TestStep {
		extra, check := "", resource.TestCheckNoResourceAttr(vmAddr, "spec.security_group")
		if sg != "" {
			extra, check = fmt.Sprintf("    security_group = %q", sg), resource.TestCheckResourceAttr(vmAddr, "spec.security_group", sg)
		}
		return resource.TestStep{
			Config: vmConfig(srv.URL, extra, ""),
			Check: resource.ComposeAggregateTestCheckFunc(
				check,
				resource.TestCheckResourceAttr(vmAddr, "status.phase", "running"),
				checkVM(srv, func(v *apiv1.VMInstance) error {
					if v.Spec.SecurityGroup != sg {
						return fmt.Errorf("security group on the API = %q, want %q", v.Spec.SecurityGroup, sg)
					}
					if v.Spec.Type != "vm-small" || v.Spec.Image != "ubuntu-22" || v.Spec.SSHKey != "mine" || v.Spec.CPUCount != "3" || v.Spec.BootDiskSize != 15 {
						return fmt.Errorf("the edit changed other settings: %+v", v.Spec)
					}
					return nil
				}),
				func(_ *terraform.State) error {
					if n := countVM(srv, "DELETE "); n != 0 {
						return fmt.Errorf("a security group edit deleted the VM (%d deletes)", n)
					}
					return nil
				},
			),
		}
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy:             checkVMGone(srv),
		Steps:                    []resource.TestStep{step("sg1"), step("sg2"), step(""), step("sg1")},
	})
}

func TestAccVM_sizeFromPresetOrExplicitValues(t *testing.T) {
	srv, _ := newVMMock(t, vmapi.Options{})
	explicit := strings.Replace(vmConfig(srv.URL, `    cpu_count = "6"
    memory    = "12"`, ""), `    preset  = "Small"`+"\n", "", 1)
	single := strings.NewReplacer(`type    = "vm-small"`, `type    = "vm-single"`, `    preset  = "Small"`+"\n", "").Replace(vmConfig(srv.URL, "", ""))
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy:             checkVMGone(srv),
		Steps: []resource.TestStep{
			{
				Config: explicit,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(vmAddr, "spec.cpu_count", "6"),
					resource.TestCheckResourceAttr(vmAddr, "spec.memory", "12"),
					resource.TestCheckNoResourceAttr(vmAddr, "spec.preset"),
				),
			},
			{
				// A SKU with one preset needs no choice: its size is used.
				Config: single,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(vmAddr, "spec.cpu_count", "4"),
					resource.TestCheckResourceAttr(vmAddr, "spec.memory", "8"),
					checkVM(srv, func(v *apiv1.VMInstance) error {
						if v.Spec.Type != "vm-single" || v.Spec.CPUCount != "4" || v.Spec.Memory != "8" {
							return fmt.Errorf("spec = %+v", v.Spec)
						}
						return nil
					}),
				),
			},
		},
	})
}

func TestAccVM_changingThePresetReplacesTheVM(t *testing.T) {
	srv, _ := newVMMock(t, vmapi.Options{})
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy:             checkVMGone(srv),
		Steps: []resource.TestStep{
			{Config: vmConfig(srv.URL, "", "")},
			{
				// The size is fixed at create: another preset means another VM.
				Config: strings.Replace(vmConfig(srv.URL, "", ""), `preset  = "Small"`, `preset  = "Medium"`, 1),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(vmAddr, "spec.cpu_count", "6"),
					resource.TestCheckResourceAttr(vmAddr, "spec.memory", "12"),
					resource.TestCheckResourceAttr(vmAddr, "status.phase", "running"),
					func(_ *terraform.State) error {
						if n := countVM(srv, "DELETE "); n != 1 {
							return fmt.Errorf("%d deletes, want 1: the VM was not replaced", n)
						}
						return nil
					},
				),
			},
		},
	})
}

func TestAccVM_changingTheImageOrBootDiskReplacesTheVM(t *testing.T) {
	srv, _ := newVMMock(t, vmapi.Options{})
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy:             checkVMGone(srv),
		Steps: []resource.TestStep{
			{Config: vmConfig(srv.URL, "    boot_disk_size = 25", ""), Check: resource.TestCheckResourceAttr(vmAddr, "spec.boot_disk_size", "25")},
			{
				Config: vmConfig(srv.URL, "    boot_disk_size = 35", ""),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(vmAddr, "spec.boot_disk_size", "35"),
					func(_ *terraform.State) error {
						if n := countVM(srv, "DELETE "); n != 1 {
							return fmt.Errorf("%d deletes after a boot disk change, want 1", n)
						}
						return nil
					},
				),
			},
			{
				Config: strings.Replace(vmConfig(srv.URL, "    boot_disk_size = 35", ""), `image   = "ubuntu-22"`, `image   = "ubuntu-24"`, 1),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(vmAddr, "spec.image", "ubuntu-24"),
					func(_ *terraform.State) error {
						if n := countVM(srv, "DELETE "); n != 2 {
							return fmt.Errorf("%d deletes after an image change, want 2 in total", n)
						}
						return nil
					},
				),
			},
		},
	})
}

func TestAccVM_clientSideSettingsDoNotReapplyTheVM(t *testing.T) {
	srv, _ := newVMMock(t, vmapi.Options{})
	var posts int
	// a description the platform does not store must not make a client-side change re-apply the VM either
	withDescription := func(extra string) string {
		return strings.Replace(vmConfig(srv.URL, "", extra), `    workspace = "ws"`, "    workspace = \"ws\"\n    description = \"kept in Terraform only\"", 1)
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy:             checkVMGone(srv),
		Steps: []resource.TestStep{
			{
				Config: withDescription(""),
				Check: func(_ *terraform.State) error {
					posts = countVM(srv, "POST ")
					return nil
				},
			},
			{
				// Only timeouts and desired_action = none change: Terraform updates its state and sends nothing.
				Config: withDescription("  desired_action = \"none\"\n  timeouts = { create = \"30m\" }"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(vmAddr, "status.phase", "running"),
					func(_ *terraform.State) error {
						if n := countVM(srv, "POST "); n != posts {
							return fmt.Errorf("a client-side change re-applied the VM: %d POSTs, want %d", n, posts)
						}
						return nil
					},
				),
			},
		},
	})
}

func TestAccVM_passwordIsWriteOnly(t *testing.T) {
	srv, _ := newVMMock(t, vmapi.Options{})
	cfg := func(password string) string {
		return providerBlock(srv.URL) + fmt.Sprintf(`
resource "gpupaas_virtual_machine" "v" {
  metadata = { name = "v1", project = %q, workspace = %q }
  spec = {
    type           = "vm-small"
    preset         = "Small"
    image          = "ubuntu-22"
    guest_password = %q
  }
}
`, testProject, vmWorkspace, password)
	}
	rawPassword := func(want string) resource.TestCheckFunc {
		return func(_ *terraform.State) error {
			sp := srv.RawSpec(testProject, vmWorkspace, "v1")
			if sp == nil {
				return fmt.Errorf("virtual machine v1 does not exist on the API")
			}
			if sp.GuestPassword != want {
				return fmt.Errorf("password on the API differs from the configured one")
			}
			return nil
		}
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy:             checkVMGone(srv),
		Steps: []resource.TestStep{
			{
				// A password is enough to log in; no key is needed. The API never returns it, and the plan
				// after apply must still be empty.
				Config: cfg("first-secret"),
				Check: resource.ComposeAggregateTestCheckFunc(
					rawPassword("first-secret"),
					resource.TestCheckResourceAttr(vmAddr, "spec.guest_password", "first-secret"),
					resource.TestCheckNoResourceAttr(vmAddr, "spec.ssh_key"),
				),
			},
			{Config: cfg("second-secret"), Check: rawPassword("second-secret")},
		},
	})
}

func TestAccVM_projectScope(t *testing.T) {
	srv, _ := newVMMock(t, vmapi.Options{})
	cfg := providerBlock(srv.URL) + fmt.Sprintf(`
resource "gpupaas_virtual_machine" "v" {
  metadata = { name = "v1", project = %q }
  spec = {
    type    = "vm-small"
    preset  = "Medium"
    image   = "ubuntu-24"
    ssh_key = "mine"
  }
}
`, testProject)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy: func(_ *terraform.State) error {
			if srv.Exists(testProject, "", "v1") {
				return fmt.Errorf("virtual machine still exists")
			}
			return nil
		},
		Steps: []resource.TestStep{{
			Config: cfg,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr(vmAddr, "id", "system-catalog/v1"),
				resource.TestCheckResourceAttr(vmAddr, "status.phase", "running"),
				resource.TestCheckResourceAttr(vmAddr, "spec.memory", "12"),
			),
		}},
	})
}

func TestAccVM_planTimeValidation(t *testing.T) {
	srv, _ := newVMMock(t, vmapi.Options{})
	with := func(typeName, spec string) string {
		return strings.Replace(vmConfig(srv.URL, spec, ""), `type    = "vm-small"`, fmt.Sprintf(`type    = %q`, typeName), 1)
	}
	noPreset := func(typeName, spec string) string {
		return strings.Replace(with(typeName, spec), `    preset  = "Small"`+"\n", "", 1)
	}
	cases := []struct {
		name string
		cfg  string
		want string
	}{
		{"image", strings.Replace(vmConfig(srv.URL, "", ""), `"ubuntu-22"`, `"ubuntu-99"`, 1), `allows these image values: ubuntu-22, ubuntu-24; got "ubuntu-99"`},
		{"datacenter", vmConfig(srv.URL, `    datacenter = "mars-dc"`, ""), `allows these datacenter values: dc1; got "mars-dc"`},
		{"datacenter typo", with("vm-noip", `    datacenter = "dc3"`), `Did you mean "dc`},
		{"public ip", with("vm-noip", "    assign_public_ip = true"), `does not assign public IPs`},
		{"unknown preset", strings.Replace(vmConfig(srv.URL, "", ""), `"Small"`, `"Huge"`, 1), `has these presets: Small, Medium; got "Huge"`},
		{"preset and cpu together", vmConfig(srv.URL, `    cpu_count = "3"`, ""), `Set spec.preset, or spec.cpu_count and spec.memory, not both`},
		{"size not offered", noPreset("vm-small", "    cpu_count = \"4\"\n    memory = \"3\""), `offers these sizes: Small (cpu_count 3, memory 3); Medium (cpu_count 6, memory 12)`},
		{"cpu and memory of different rows", noPreset("vm-small", "    cpu_count = \"3\"\n    memory = \"12\""), `is not one of them`},
		{"several sizes, none chosen", noPreset("vm-small", ""), `Choose a size`},
		{"gpu count needed", noPreset("vm-gpu", "    cpu_count = \"8\"\n    memory = \"64\""), `GPU count required`},
		{"boot disk too small", vmConfig(srv.URL, "    boot_disk_size = 10", ""), `allows boot disks of 15 to 2000 GB`},
		{"boot disk off the step", vmConfig(srv.URL, "    boot_disk_size = 22", ""), `in steps of 10`},
		{"cpu without memory", noPreset("vm-small", `    cpu_count = "3"`), `cpu_count and memory go together`},
		{"memory without cpu", noPreset("vm-small", `    memory = "3"`), `cpu_count and memory go together`},
		{"gpu count on a cpu sku", noPreset("vm-small", "    cpu_count = \"3\"\n    memory = \"3\"\n    gpu_count = \"1\""), `is not a GPU SKU`},
		{"gpu count alone", noPreset("vm-gpu", `    gpu_count = "1"`), `cpu_count and memory go together`},
		{"public ip off on an always sku", with("vm-always", "    assign_public_ip = false"), `always assigns a public IP`},
		{"no way to log in", strings.Replace(vmConfig(srv.URL, "", ""), `ssh_key = "mine"`, "", 1), `Set spec.ssh_key, spec.guest_password, or both`},
		{"desired action", vmConfig(srv.URL, "", `  desired_action = "explode"`), `value must be one of`},
	}
	var steps []resource.TestStep
	for _, c := range cases {
		steps = append(steps, resource.TestStep{Config: c.cfg, PlanOnly: true, ExpectError: errRe(c.want)})
	}
	resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: protoFactories(), Steps: steps})

	// Nothing was created while validating.
	if n := countVM(srv, "POST "); n != 0 {
		t.Fatalf("validation created VMs: %v", srv.Requests())
	}
}

func TestAccVM_gpuSKUResolvesTheGPUCount(t *testing.T) {
	srv, _ := newVMMock(t, vmapi.Options{})
	cfg := strings.NewReplacer(`type    = "vm-small"`, `type    = "vm-gpu"`, `preset  = "Small"`, `preset  = "G2"`).Replace(vmConfig(srv.URL, "", ""))
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy:             checkVMGone(srv),
		Steps: []resource.TestStep{{
			Config: cfg,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr(vmAddr, "spec.cpu_count", "16"),
				resource.TestCheckResourceAttr(vmAddr, "spec.memory", "128"),
				resource.TestCheckResourceAttr(vmAddr, "spec.gpu_count", "2"),
				resource.TestCheckResourceAttr(vmAddr, "spec.gpu_model", "L40S"),
				resource.TestCheckResourceAttr(vmAddr, "spec.gpu_vendor", "nvidia"),
			),
		}},
	})
}

func TestAccVM_skuWithoutAgentIsOnlyAWarning(t *testing.T) {
	// The agent list is not a health check, so an empty one must not block: the plan succeeds and the
	// VM is created (the mock has no agents to fail on).
	srv, _ := newVMMock(t, vmapi.Options{})
	cfg := strings.Replace(vmConfig(srv.URL, "", ""), `type    = "vm-small"`, `type    = "vm-noagent"`, 1)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy:             checkVMGone(srv),
		Steps:                    []resource.TestStep{{Config: cfg, Check: resource.TestCheckResourceAttr(vmAddr, "status.phase", "running")}},
	})
}

func TestAccVM_provisionFailureShowsTheWorkflowCauseAndIsCleanedUp(t *testing.T) {
	// The VM only says "due to an internal error"; the cause is on the compute instance behind it.
	srv, _ := newVMMock(t, vmapi.Options{})
	srv.FailProvision = func(string) string { return "x" }
	srv.FailureCause = func(string) string { return "Invalid host configuration: host_ip is unbound" }
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy:             checkVMGone(srv),
		Steps: []resource.TestStep{{
			Config:      vmConfig(srv.URL, "", ""),
			ExpectError: errRe(`due to an internal error`),
		}, {
			Config:      vmConfig(srv.URL, "", ""),
			ExpectError: errRe(`Invalid host configuration: host_ip is unbound`),
		}},
	})
}

func TestAccVM_noCapacityIsReported(t *testing.T) {
	srv, _ := newVMMock(t, vmapi.Options{Capacity: 1})
	cfg := providerBlock(srv.URL) + vmResource("v", "v1", "", "") + vmResource("w", "v2", "", "")
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy:             checkVMGone(srv),
		Steps:                    []resource.TestStep{{Config: cfg, ExpectError: errRe(`no capacity available right now`)}},
	})
}

func TestAccVM_teardownFailureCanBeRetried(t *testing.T) {
	srv, _ := newVMMock(t, vmapi.Options{})
	var calls atomic.Int32
	srv.FailDelete = func(string) string {
		if calls.Add(1) == 1 {
			return "the host did not answer the shutdown request"
		}
		return ""
	}
	cfg := vmConfig(srv.URL, "", "")
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy:             checkVMGone(srv),
		Steps: []resource.TestStep{
			{Config: cfg},
			{Config: cfg, Destroy: true, ExpectError: errRe(`the host did not answer the shutdown request`)},
		},
	})
}

func TestAccVM_destroyWaitsForTheBackingInstance(t *testing.T) {
	// The record vanishes before the compute instance behind it; a workspace cannot be deleted until that
	// instance is gone, so destroy must wait for it.
	srv, _ := newVMMock(t, vmapi.Options{MirrorLinger: 400 * time.Millisecond})
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy: func(_ *terraform.State) error {
			if srv.Exists(testProject, vmWorkspace, "v1") {
				return fmt.Errorf("virtual machine still exists")
			}
			if srv.MirrorExists(testProject, vmWorkspace, "v1") {
				return fmt.Errorf("destroy returned while the compute instance behind the VM still existed")
			}
			return nil
		},
		Steps: []resource.TestStep{{Config: vmConfig(srv.URL, "", "")}},
	})
}

func TestAccVM_deletedOutsideTerraformIsRecreated(t *testing.T) {
	srv, cs := newVMMock(t, vmapi.Options{})
	cfg := vmConfig(srv.URL, "", "")
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy:             checkVMGone(srv),
		Steps: []resource.TestStep{
			{Config: cfg},
			{
				PreConfig: func() {
					ctx := context.Background()
					c := cs.V1alpha1().Workspaces(testProject).VMInstances(vmWorkspace)
					if err := c.Delete(ctx, "v1", gpupaas.DeleteOptions{}); err != nil {
						t.Fatal(err)
					}
					for i := 0; i < 200 && srv.Exists(testProject, vmWorkspace, "v1"); i++ {
						if _, err := c.Get(ctx, "v1", gpupaas.GetOptions{}); gpupaas.IsNotFound(err) {
							break
						}
						time.Sleep(10 * time.Millisecond)
					}
				},
				Config: cfg,
				Check:  resource.TestCheckResourceAttr(vmAddr, "status.phase", "running"),
			},
		},
	})
}

func TestAccVM_dataSources(t *testing.T) {
	srv, _ := newVMMock(t, vmapi.Options{})
	cfg := vmConfig(srv.URL, "", "") + fmt.Sprintf(`
data "gpupaas_virtual_machine" "d" {
  name      = gpupaas_virtual_machine.v.metadata.name
  project   = %q
  workspace = %q
}

data "gpupaas_virtual_machine_sku" "p" {
  name    = "vm-small"
  project = %q
}
`, testProject, vmWorkspace, testProject)
	const d, p = "data.gpupaas_virtual_machine.d", "data.gpupaas_virtual_machine_sku.p"
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy:             checkVMGone(srv),
		Steps: []resource.TestStep{{
			Config: cfg,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr(d, "status.phase", "running"),
				resource.TestCheckResourceAttr(d, "spec.type", "vm-small"),
				resource.TestCheckResourceAttr(d, "spec.cpu_count", "3"),
				resource.TestCheckResourceAttr(d, "spec.image", "ubuntu-22"),
				resource.TestCheckResourceAttr(d, "spec.datacenter", "dc1"),
				resource.TestCheckResourceAttr(d, "status.host_name", "v1-host"),
				resource.TestCheckResourceAttr(p, "id", "system-catalog/vm-small"),
				resource.TestCheckResourceAttr(p, "images.#", "2"),
				resource.TestCheckResourceAttr(p, "default_image", "ubuntu-22"),
				resource.TestCheckResourceAttr(p, "datacenters.0", "dc1"),
				resource.TestCheckResourceAttr(p, "gpus_enabled", "false"),
				resource.TestCheckResourceAttr(p, "presets.#", "2"),
				resource.TestCheckResourceAttr(p, "presets.1.name", "Medium"),
				resource.TestCheckResourceAttr(p, "presets.1.cpu", "6"),
				resource.TestCheckResourceAttr(p, "presets.1.memory", "12"),
				resource.TestCheckResourceAttr(p, "boot_disk_min", "15"),
				resource.TestCheckResourceAttr(p, "boot_disk_max", "2000"),
				resource.TestCheckResourceAttr(p, "boot_disk_increment", "10"),
				resource.TestCheckResourceAttr(p, "public_ip", "Optional"),
				resource.TestCheckResourceAttr(p, "agents.0", "agent-1"),
			),
		}},
	})
}

func TestAccVM_provisionFailureShowsTheVMsOwnDetailWhenTheComputeInstanceIsGone(t *testing.T) {
	srv, _ := newVMMock(t, vmapi.Options{})
	srv.FailProvision = func(string) string { return "x" }
	srv.ReasonDetail = func(string) string { return "vm-provision: no host with free capacity in dc1" }
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy:             checkVMGone(srv),
		Steps: []resource.TestStep{{
			Config:      vmConfig(srv.URL, "", ""),
			ExpectError: errRe(`no host with free capacity in dc1`),
		}},
	})
}

func TestAccVM_parkedDeleteIsExplainedAndRefusedRetriesAreToo(t *testing.T) {
	srv, _ := newVMMock(t, vmapi.Options{})
	srv.ParkDelete = func(string) bool { return true }
	cfg := vmConfig(srv.URL, "", "")
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy:             checkVMGone(srv),
		Steps: []resource.TestStep{
			{Config: cfg},
			{Config: cfg, Destroy: true, ExpectError: errRe(`Status "delete_pending" after "destroy"`)},
			{Config: cfg, Destroy: true, ExpectError: errRe(`end users cannot delete it again`)},
			{
				PreConfig:          func() { srv.AdminDelete(testProject, vmWorkspace, "v1") },
				Config:             cfg,
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
		},
	})
}

// An initial desired_action the platform refuses must not fail the create: Terraform would taint a VM
// that is up, and the next apply would destroy and rebuild it.
func TestAccVM_refusedInitialActionDoesNotTaintTheVM(t *testing.T) {
	srv, _ := newVMMock(t, vmapi.Options{UnsupportedActions: []string{"reboot"}})
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy:             checkVMGone(srv),
		Steps: []resource.TestStep{{
			Config: vmConfig(srv.URL, "", `  desired_action = "reboot"`),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr(vmAddr, "status.phase", "running"),
				resource.TestCheckResourceAttr(vmAddr, "desired_action", "reboot"),
				func(_ *terraform.State) error {
					if countVM(srv, "/action/reboot") != 1 {
						return fmt.Errorf("the refused action was not requested exactly once: %v", srv.Requests())
					}
					return nil
				},
			),
		}},
	})
}

// A refused action on an existing VM is an error, and the state keeps the previous desired_action, so
// the next plan shows the change again instead of forgetting the action.
func TestAccVM_refusedActionOnUpdateIsRetried(t *testing.T) {
	srv, _ := newVMMock(t, vmapi.Options{UnsupportedActions: []string{"reboot"}})
	withAction := vmConfig(srv.URL, "", `  desired_action = "reboot"`)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy:             checkVMGone(srv),
		Steps: []resource.TestStep{
			{Config: vmConfig(srv.URL, "", "")},
			{Config: withAction, ExpectError: errRe(`not supported for this instance`)},
			{Config: withAction, PlanOnly: true, ExpectNonEmptyPlan: true},
		},
	})
}

// A name that is taken is refused up front, so the VM a failed create leaves behind can only be one this
// apply created.
func TestAccVM_createRefusesAnExistingName(t *testing.T) {
	srv, _ := newVMMock(t, vmapi.Options{})
	one := vmConfig(srv.URL, "", "")
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy:             checkVMGone(srv),
		Steps: []resource.TestStep{
			{Config: one},
			{Config: one + vmResource("dup", "v1", "", ""), ExpectError: errRe(`VM already exists`)},
		},
	})
}

// The live platform accepts a display name and a description on a VM but stores neither (seen on
// narayana-tb1): the plan after apply must still be empty, and an update of them must not loop.
func TestAccVM_displayNameAndDescriptionAreKeptAsConfigured(t *testing.T) {
	srv, _ := newVMMock(t, vmapi.Options{})
	with := func(display, desc string) string {
		return strings.Replace(vmConfig(srv.URL, "", ""), `    workspace = "ws"`, fmt.Sprintf("    workspace = \"ws\"\n    display_name = %q\n    description  = %q", display, desc), 1)
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy:             checkVMGone(srv),
		Steps: []resource.TestStep{
			{Config: with("First", "one"), Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr(vmAddr, "metadata.display_name", "First"),
				resource.TestCheckResourceAttr(vmAddr, "metadata.description", "one"))},
			{Config: with("Second", "two"), Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr(vmAddr, "metadata.display_name", "Second"),
				resource.TestCheckResourceAttr(vmAddr, "metadata.description", "two"))},
		},
	})
}

// An administrator editing the SKU's preset after a VM exists must not replace the VM: a no-op plan stays
// empty (with a warning), and an update that has nothing to do with the size still works, even when the preset
// row is gone.
func TestAccVM_aSKUEditedAfterCreateDoesNotReplaceTheVM(t *testing.T) {
	srv, _ := newVMMock(t, vmapi.Options{})
	edited := func(rows ...apiv1.VMPreset) func() {
		return func() {
			p := vmTestProfiles()[0]
			p.Spec.CPUPreset = rows
			srv.SetProfile(p)
		}
	}
	cfg := vmConfig(srv.URL, "", "")
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy:             checkVMGone(srv),
		Steps: []resource.TestStep{
			{Config: cfg, Check: resource.TestCheckResourceAttr(vmAddr, "spec.cpu_count", "3")},
			{
				// Small now means 4 CPUs and 8 GB: the VM keeps what it was created with.
				PreConfig: edited(apiv1.VMPreset{Name: "Small", CPU: "4", Memory: "8"}, apiv1.VMPreset{Name: "Medium", CPU: "6", Memory: "12"}),
				Config:    cfg,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(vmAddr, "spec.cpu_count", "3"),
					func(_ *terraform.State) error {
						if n := countVM(srv, "DELETE "); n != 0 {
							return fmt.Errorf("an edited SKU replaced the VM (%d deletes)", n)
						}
						return nil
					},
				),
			},
			{
				// The preset row is removed altogether; a power action must still work.
				PreConfig: edited(apiv1.VMPreset{Name: "Medium", CPU: "6", Memory: "12"}),
				Config:    vmConfig(srv.URL, "", `  desired_action = "stop"`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(vmAddr, "status.phase", "stopped"),
					resource.TestCheckResourceAttr(vmAddr, "spec.cpu_count", "3"),
				),
			},
		},
	})
}

// A SKU lives in the system catalog; a VM of another project still validates and resolves its preset.
func TestAccVM_aSKUIsFoundInTheCatalogFromAnotherProject(t *testing.T) {
	srv, _ := newVMMock(t, vmapi.Options{CatalogOnlyProfiles: true})
	cfg := providerBlock(srv.URL) + `
resource "gpupaas_virtual_machine" "v" {
  metadata = { name = "v1", project = "team-x" }
  spec = {
    type    = "vm-small"
    preset  = "Medium"
    image   = "ubuntu-22"
    ssh_key = "mine"
  }
}
`
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy: func(_ *terraform.State) error {
			if srv.Exists("team-x", "", "v1") {
				return fmt.Errorf("virtual machine still exists")
			}
			return nil
		},
		Steps: []resource.TestStep{{
			Config: cfg,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr(vmAddr, "id", "team-x/v1"),
				resource.TestCheckResourceAttr(vmAddr, "spec.cpu_count", "6"),
				resource.TestCheckResourceAttr(vmAddr, "spec.memory", "12"),
			),
		}},
	})
	// and a preset it cannot find in the catalog is still reported at plan time
	srv2, _ := newVMMock(t, vmapi.Options{CatalogOnlyProfiles: true})
	bad := strings.Replace(strings.Replace(providerBlock(srv2.URL)+`
resource "gpupaas_virtual_machine" "v" {
  metadata = { name = "v1", project = "team-x" }
  spec = { type = "vm-small", preset = "Huge", image = "ubuntu-22", ssh_key = "mine" }
}
`, "Huge", "Huger", 1), "Huger", "Huge", 1)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		Steps:                    []resource.TestStep{{Config: bad, PlanOnly: true, ExpectError: errRe(`has these presets: Small, Medium; got "Huge"`)}},
	})
}

// A preset that is only known after apply (it comes from another resource) cannot be checked at plan time: it
// must neither fail with a false "choose a size" nor be replaced by a guess.
func TestAccVM_aPresetKnownOnlyAfterApplyIsAccepted(t *testing.T) {
	srv, _ := newVMMock(t, vmapi.Options{})
	cfg := strings.Replace(vmConfig(srv.URL, "", ""), `preset  = "Small"`, `preset  = terraform_data.size.output`, 1) + `
resource "terraform_data" "size" {
  input = "Medium"
}
`
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy:             checkVMGone(srv),
		Steps: []resource.TestStep{{
			Config: cfg,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr(vmAddr, "spec.cpu_count", "6"),
				resource.TestCheckResourceAttr(vmAddr, "spec.memory", "12"),
				resource.TestCheckResourceAttr(vmAddr, "spec.preset", "Medium"),
			),
		}},
	})
}
