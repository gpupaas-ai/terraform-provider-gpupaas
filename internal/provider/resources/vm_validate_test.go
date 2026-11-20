package resources

import (
	"context"
	"strings"
	"testing"

	apiv1 "github.com/gpupaas-ai/gpupaas-go/apis/v1alpha1"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func vmProfile(mut func(*apiv1.VMProfile)) *apiv1.VMProfile {
	p := &apiv1.VMProfile{
		Metadata: apiv1.ObjectMeta{Name: "vm-test"},
		Spec: apiv1.VMProfileSpec{
			Image: []string{"ubuntu-24"}, Datacenter: []string{"dc1"}, PublicIP: "Optional",
			CPUPreset: []apiv1.VMPreset{{Name: "Small", CPU: "3", Memory: "3"}},
			BootDisk:  &apiv1.VMBootDisk{MinSize: "15", MaxSize: "2000", Increment: "10", Unit: "gb"},
		},
		Status: apiv1.VMProfileStatus{GlobalSettings: &apiv1.VMProfileGlobalSettings{Agents: []string{"a"}}},
	}
	if mut != nil {
		mut(p)
	}
	return p
}

func errorsOf(d diag.Diagnostics) string {
	var out []string
	for _, e := range d.Errors() {
		out = append(out, e.Summary()+": "+e.Detail())
	}
	return strings.Join(out, "\n")
}

func TestPlannedVMSize(t *testing.T) {
	two := vmProfile(func(p *apiv1.VMProfile) {
		p.Spec.CPUPreset = append(p.Spec.CPUPreset, apiv1.VMPreset{Name: "Big", CPU: "8", Memory: "16"})
	})
	one := vmProfile(nil)
	cases := []struct {
		name string
		cfg  vmSpecModel
		p    *apiv1.VMProfile
		want vmSize
		ok   bool
	}{
		{"preset", vmSpecModel{Preset: types.StringValue("Big")}, two, vmSize{cpu: "8", memory: "16"}, true},
		{"explicit", vmSpecModel{CPUCount: types.StringValue("3"), Memory: types.StringValue("3")}, two, vmSize{cpu: "3", memory: "3"}, true},
		{"only preset is the default", vmSpecModel{}, one, vmSize{cpu: "3", memory: "3"}, true},
		{"several presets and nothing chosen", vmSpecModel{}, two, vmSize{}, false},
		{"unknown preset name", vmSpecModel{Preset: types.StringValue("Huge")}, two, vmSize{}, false},
	}
	for _, c := range cases {
		got, ok := plannedVMSize(c.cfg, c.p)
		if ok != c.ok || got != c.want {
			t.Errorf("%s: got (%+v, %v), want (%+v, %v)", c.name, got, ok, c.want, c.ok)
		}
	}
}

func TestBootDiskValidationOnlyForNewOrChangedValuesAndNeverPanics(t *testing.T) {
	p := vmProfile(nil)
	validate := func(s vmSpecModel, prior *vmSpecModel, prof *apiv1.VMProfile) diag.Diagnostics {
		var d diag.Diagnostics
		validateVMAgainstProfile(s, prior, prof, &d)
		return d
	}
	off := vmSpecModel{BootDiskSize: types.Int64Value(40)} // 40 is not 15 + n*10: the platform's own default
	if d := validate(off, nil, p); !d.HasError() {
		t.Fatal("a new off-step size must be refused")
	}
	// A VM created with the platform's own 40 GB default must not warn on every later plan.
	if d := validate(off, &vmSpecModel{BootDiskSize: types.Int64Value(40)}, p); len(d) != 0 {
		t.Fatalf("an unchanged size must stay quiet: %v", d)
	}
	if d := validate(vmSpecModel{BootDiskSize: types.Int64Value(25)}, nil, p); d.HasError() {
		t.Fatalf("15 + 10 is on the step: %s", errorsOf(d))
	}
	// a fractional increment used to divide by zero
	frac := vmProfile(func(p *apiv1.VMProfile) { p.Spec.BootDisk.Increment = "0.5" })
	if d := validate(vmSpecModel{BootDiskSize: types.Int64Value(20)}, nil, frac); d.HasError() {
		t.Fatalf("20 is a multiple of 0.5 above 15: %s", errorsOf(d))
	}
	zero := vmProfile(func(p *apiv1.VMProfile) { p.Spec.BootDisk.Increment = "0" })
	if d := validate(vmSpecModel{BootDiskSize: types.Int64Value(20)}, nil, zero); d.HasError() {
		t.Fatalf("a zero increment means no step: %s", errorsOf(d))
	}
	if d := validate(vmSpecModel{BootDiskSize: types.Int64Value(10)}, nil, p); !d.HasError() {
		t.Fatal("below the minimum must be refused")
	}
}

func TestPublicIPPolicyIsEnforcedBothWays(t *testing.T) {
	validate := func(mode string, ip bool) diag.Diagnostics {
		var d diag.Diagnostics
		validateVMAgainstProfile(vmSpecModel{AssignPublicIP: types.BoolValue(ip)}, nil, vmProfile(func(p *apiv1.VMProfile) { p.Spec.PublicIP = mode }), &d)
		return d
	}
	for _, c := range []struct {
		mode    string
		ip      bool
		refused bool
	}{{"None", true, true}, {"None", false, false}, {"Always", false, true}, {"Always", true, false}, {"Optional", true, false}, {"Optional", false, false}} {
		if d := validate(c.mode, c.ip); d.HasError() != c.refused {
			t.Errorf("mode %s, assign_public_ip %v: refused = %v, want %v (%s)", c.mode, c.ip, d.HasError(), c.refused, errorsOf(d))
		}
	}
}

func TestSameVMSize(t *testing.T) {
	s := vmSpecModel{CPUCount: types.StringValue("3"), Memory: types.StringValue("3"), GPUCount: types.StringNull()}
	if !sameVMSize(apiv1.VMPreset{CPU: "3", Memory: "3"}, s) {
		t.Fatal("an equal row must match (a missing gpu count is empty)")
	}
	if sameVMSize(apiv1.VMPreset{CPU: "4", Memory: "8"}, s) || sameVMSize(apiv1.VMPreset{CPU: "3", Memory: "3", GPU: "1"}, s) {
		t.Fatal("a different row must not match")
	}
}

func TestUnchangedNamesAreNotResentSoTheVMIsNotReapplied(t *testing.T) {
	current := &apiv1.VMInstance{
		Metadata: apiv1.ObjectMeta{Name: "v1", Project: "p", Workspace: "w"}, // the platform stores no description
		Spec:     apiv1.VMInstanceSpec{Type: "vm-test", CPUCount: "3", Memory: "3", Image: "ubuntu", Sharing: &apiv1.DevSharingSpec{ShareMode: "None"}},
	}
	meta := MetadataModel{Name: types.StringValue("v1"), Project: types.StringValue("p"), Workspace: types.StringValue("w"),
		DisplayName: types.StringValue("Nice"), Description: types.StringValue("live test"), Labels: types.MapNull(types.StringType), Annotations: types.MapNull(types.StringType)}
	plan := vmResourceModel{Metadata: meta, Spec: vmSpecModel{Type: types.StringValue("vm-test"), Image: types.StringValue("ubuntu"), CPUCount: types.StringValue("3"), Memory: types.StringValue("3")}}
	state := vmSpecModel{Type: types.StringValue("vm-test")}
	var diags diag.Diagnostics
	obj := buildVM(context.Background(), plan, &state, current, nil, &diags)
	if sameVMRequest(obj, current) {
		t.Fatal("precondition: the configured description makes the request differ from what the platform holds")
	}
	keepUnchangedNames(obj, current, plan.Metadata, meta) // the configuration did not change
	if !sameVMRequest(obj, current) {
		t.Fatalf("an unchanged description must not make the VM look changed: %+v", obj.Metadata)
	}
	changed := meta
	changed.Description = types.StringValue("new text")
	plan.Metadata = changed
	obj = buildVM(context.Background(), plan, &state, current, nil, &diags)
	keepUnchangedNames(obj, current, plan.Metadata, meta)
	if sameVMRequest(obj, current) || obj.Metadata.Description != "new text" {
		t.Fatal("a changed description must still be sent")
	}
}

func TestUpdateKeepsTheSizeWhenThePresetCannotBeResolved(t *testing.T) {
	current := &apiv1.VMInstance{
		Metadata: apiv1.ObjectMeta{Name: "v1"},
		Spec:     apiv1.VMInstanceSpec{Type: "vm-test", CPUCount: "3", Memory: "3", Image: "ubuntu"},
	}
	plan := vmResourceModel{
		Metadata: MetadataModel{Name: types.StringValue("v1"), Project: types.StringValue("p")},
		Spec:     vmSpecModel{Type: types.StringValue("vm-test"), Preset: types.StringValue("Removed"), Image: types.StringValue("ubuntu")},
	}
	var diags diag.Diagnostics
	obj := buildVM(context.Background(), plan, &vmSpecModel{Preset: types.StringValue("Removed")}, current, nil, &diags)
	if diags.HasError() || obj.Spec.CPUCount != "3" || obj.Spec.Memory != "3" {
		t.Fatalf("an update must keep the platform's size and not fail on the preset: %s %+v", errorsOf(diags), obj.Spec)
	}
	// on create the same preset is an error
	diags = nil
	buildVM(context.Background(), plan, nil, nil, nil, &diags)
	if !diags.HasError() {
		t.Fatal("a create with an unresolvable preset must fail")
	}
}

func TestVMSettledAndGonePredicates(t *testing.T) {
	vm := func(status, action string) *apiv1.VMInstance {
		return &apiv1.VMInstance{Metadata: apiv1.ObjectMeta{Name: "v1"}, Status: apiv1.VMInstanceStatus{Status: status, Action: action}}
	}
	for _, c := range []struct {
		status string
		done   bool
		err    bool
	}{{"running", true, false}, {"stopped", true, false}, {"in_progress", false, false}, {"failed", false, true}} {
		done, err := vmSettled(vm(c.status, "deploy"))
		if done != c.done || (err != nil) != c.err {
			t.Errorf("vmSettled(%s) = (%v, %v)", c.status, done, err)
		}
	}
	gone := vmGone(2)
	// a failed deploy record that has not started its teardown is tolerated for a few polls
	for i := 0; i < 2; i++ {
		if done, err := gone(vm("failed", "deploy")); done || err != nil {
			t.Fatalf("stale poll %d: (%v, %v)", i, done, err)
		}
	}
	if _, err := gone(vm("failed", "deploy")); err == nil {
		t.Fatal("a failed record that never starts its teardown must end the wait")
	}
	if _, err := vmGone(8)(vm("destroyfailed", "destroy")); err == nil {
		t.Fatal("a failed teardown must end the wait at once")
	}
	if done, err := vmGone(8)(vm("deleting", "destroy")); done || err != nil {
		t.Fatalf("deleting is still in flight: (%v, %v)", done, err)
	}
}
