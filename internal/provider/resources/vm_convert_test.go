package resources

import (
	"context"
	"testing"

	apiv1 "github.com/gpupaas-ai/gpupaas-go/apis/v1alpha1"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func vmSKUForTest() *apiv1.VMProfile {
	return &apiv1.VMProfile{
		Metadata: apiv1.ObjectMeta{Name: "vm-test"},
		Spec: apiv1.VMProfileSpec{
			CPUPreset: []apiv1.VMPreset{{Name: "Small", CPU: "3", Memory: "3"}, {Name: "Big", CPU: "8", Memory: "16"}},
		},
	}
}

func TestResolveVMSizeFromPresetOrValues(t *testing.T) {
	p := vmSKUForTest()
	if size, ok := resolveVMSize(vmSpecModel{Preset: types.StringValue("small")}, p); !ok || size != (vmSize{cpu: "3", memory: "3"}) {
		t.Fatalf("preset names are case-insensitive: %+v %v", size, ok)
	}
	if _, ok := resolveVMSize(vmSpecModel{Preset: types.StringValue("Huge")}, p); ok {
		t.Fatal("an unknown preset must not resolve")
	}
	if _, ok := resolveVMSize(vmSpecModel{Preset: types.StringValue("Small")}, nil); ok {
		t.Fatal("a preset cannot resolve when the SKU is unreadable")
	}
	size, ok := resolveVMSize(vmSpecModel{CPUCount: types.StringValue("8"), Memory: types.StringValue("16")}, nil)
	if !ok || size != (vmSize{cpu: "8", memory: "16"}) {
		t.Fatalf("explicit values need no SKU: %+v %v", size, ok)
	}
}

func TestVMPresetForSizeNamesTheMatchingRow(t *testing.T) {
	p := vmSKUForTest()
	if got := vmPresetForSize(p, "8", "16", ""); got != "Big" {
		t.Fatalf("8/16 should be Big, got %q", got)
	}
	if got := vmPresetForSize(p, "4", "16", ""); got != "" {
		t.Fatalf("a size no row has must not match, got %q", got)
	}
	if got := vmPresetForSize(nil, "3", "3", ""); got != "" {
		t.Fatalf("an unreadable SKU must not match, got %q", got)
	}
	dup := vmSKUForTest()
	dup.Spec.CPUPreset = append(dup.Spec.CPUPreset, apiv1.VMPreset{Name: "Small2", CPU: "3", Memory: "3"})
	if got := vmPresetForSize(dup, "3", "3", ""); got != "" {
		t.Fatalf("two rows with the same size are ambiguous, got %q", got)
	}
}

func TestBuildVMSendsTheResolvedSizeAndSnakeCaseSpec(t *testing.T) {
	plan := vmResourceModel{
		Metadata: MetadataModel{Name: types.StringValue("v1"), Project: types.StringValue("p"), Workspace: types.StringValue("w")},
		Spec: vmSpecModel{
			Type: types.StringValue("vm-test"), Preset: types.StringValue("Big"), Image: types.StringValue("ubuntu"), SSHKey: types.StringValue("k"),
			GuestPassword: types.StringValue("pw"), BootDiskSize: types.Int64Value(25), AssignPublicIP: types.BoolValue(true),
		},
	}
	var diags diag.Diagnostics
	obj := buildVM(context.Background(), plan, nil, nil, vmSKUForTest(), &diags)
	if diags.HasError() {
		t.Fatal(diags)
	}
	s := obj.Spec
	if s.Type != "vm-test" || s.CPUCount != "8" || s.Memory != "16" || s.Image != "ubuntu" || s.SSHKey != "k" || s.GuestPassword != "pw" || s.BootDiskSize != 25 || !s.AssignPublicIP {
		t.Fatalf("spec = %+v", s)
	}
	if s.Sharing == nil || s.Sharing.ShareMode != "None" {
		t.Fatalf("a new VM is private unless sharing is configured: %#v", s.Sharing)
	}
	if obj.Kind != apiv1.KindVMInstance {
		t.Fatalf("kind = %q", obj.Kind)
	}

	// an unknown preset is an error, never a silently empty size
	plan.Spec.Preset = types.StringValue("Huge")
	var bad diag.Diagnostics
	buildVM(context.Background(), plan, nil, nil, vmSKUForTest(), &bad)
	if !bad.HasError() {
		t.Fatal("an unknown preset must fail the build")
	}
}

func TestBuildVMOnUpdateKeepsWhatThePlatformHolds(t *testing.T) {
	current := &apiv1.VMInstance{
		Metadata: apiv1.ObjectMeta{Name: "v1", Project: "p", Workspace: "w", Labels: map[string]string{"system": "x"}},
		Spec: apiv1.VMInstanceSpec{Type: "vm-test", CPUCount: "3", Memory: "3", Image: "ubuntu", VMID: "dev-1", UserData: "encoded",
			Sharing: &apiv1.DevSharingSpec{ShareMode: "None"}},
	}
	state := vmSpecModel{Type: types.StringValue("vm-test"), UserData: types.StringValue("plain")}
	plan := vmResourceModel{
		Metadata: MetadataModel{Name: types.StringValue("v1"), Project: types.StringValue("p"), Workspace: types.StringValue("w")},
		Spec: vmSpecModel{Type: types.StringValue("vm-test"), CPUCount: types.StringValue("3"), Memory: types.StringValue("3"), Image: types.StringValue("ubuntu"),
			SSHKey: types.StringValue("k"), UserData: types.StringValue("plain"), SecurityGroup: types.StringValue("sg")},
	}
	var diags diag.Diagnostics
	obj := buildVM(context.Background(), plan, &state, current, nil, &diags)
	if obj.Spec.VMID != "dev-1" || obj.Metadata.Labels["system"] != "x" {
		t.Fatalf("platform-held fields were dropped: %+v %+v", obj.Spec, obj.Metadata)
	}
	if obj.Spec.UserData != "encoded" {
		t.Fatalf("unchanged user data must keep the platform's encoded form, got %q", obj.Spec.UserData)
	}
	if obj.Spec.GuestPassword != "" {
		t.Fatal("the password is only sent when it changed")
	}
	if sameVMRequest(obj, current) {
		t.Fatal("a security group change must be sent")
	}
	current.Spec.SecurityGroup, current.Spec.SSHKey = "sg", "k"
	if !sameVMRequest(obj, current) {
		t.Fatal("a request that changes nothing must not be re-applied")
	}
}

func TestVMActionNeeded(t *testing.T) {
	cases := []struct {
		action, prior, power string
		want                 bool
	}{
		{"start", "", "stopped", true}, {"start", "start", "running", false},
		{"stop", "", "running", true}, {"stop", "stop", "stopped", false},
		{"reboot", "", "running", true}, {"reboot", "reboot", "running", false}, {"reboot", "stop", "running", true},
	}
	for _, c := range cases {
		if got := vmActionNeeded(c.action, c.prior, c.power); got != c.want {
			t.Errorf("%s (prior %q, power %s) = %v, want %v", c.action, c.prior, c.power, got, c.want)
		}
	}
}

func TestNumeric(t *testing.T) {
	for in, want := range map[string]bool{"3": true, "0.5": true, " 10 ": true, "": false, "abc": false, "-1": false} {
		if _, ok := numeric(in); ok != want {
			t.Errorf("numeric(%q) = %v, want %v", in, ok, want)
		}
	}
}

func TestVMStatusTreatsTheNoPublicIPTextAsNoAddress(t *testing.T) {
	srv := &apiv1.VMInstance{
		Metadata: apiv1.ObjectMeta{Name: "v1", Project: "p"},
		Spec:     apiv1.VMInstanceSpec{Type: "vm-test"},
		Status:   apiv1.VMInstanceStatus{Status: "running", Output: &apiv1.VMInstanceOutput{PrivateIP: "10.0.0.5", PublicIP: "No public IP configured"}},
	}
	var diags diag.Diagnostics
	m := vmModelFromServer(context.Background(), vmResourceModel{}, srv, false, &diags)
	if !m.Status.PublicIP.IsNull() || m.Status.PrivateIP.ValueString() != "10.0.0.5" {
		t.Fatalf("public_ip = %v, private_ip = %v", m.Status.PublicIP, m.Status.PrivateIP)
	}
	srv.Status.Output.PublicIP = "203.0.113.9"
	if m = vmModelFromServer(context.Background(), vmResourceModel{}, srv, false, &diags); m.Status.PublicIP.ValueString() != "203.0.113.9" {
		t.Fatalf("a real address must be kept, got %v", m.Status.PublicIP)
	}
}

func TestVMKeepsConfiguredDisplayNameAndDescriptionWhenThePlatformStoresNone(t *testing.T) {
	srv := &apiv1.VMInstance{Metadata: apiv1.ObjectMeta{Name: "v1", Project: "p", Workspace: "w"}, Spec: apiv1.VMInstanceSpec{Type: "vm-test"}, Status: apiv1.VMInstanceStatus{Status: "running"}}
	base := vmResourceModel{
		Metadata: MetadataModel{Name: types.StringValue("v1"), Project: types.StringValue("p"), Workspace: types.StringValue("w"),
			DisplayName: types.StringValue("Nice name"), Description: types.StringValue("live test"), Labels: types.MapNull(types.StringType), Annotations: types.MapNull(types.StringType)},
		Spec: vmSpecModel{Type: types.StringValue("vm-test")},
	}
	var diags diag.Diagnostics
	m := vmModelFromServer(context.Background(), base, srv, true, &diags)
	if m.Metadata.DisplayName.ValueString() != "Nice name" || m.Metadata.Description.ValueString() != "live test" {
		t.Fatalf("a refresh dropped the configured values: %q %q", m.Metadata.DisplayName.ValueString(), m.Metadata.Description.ValueString())
	}
	// when the platform does return a value, that one wins (drift)
	srv.Metadata.Description = "changed elsewhere"
	if m = vmModelFromServer(context.Background(), base, srv, true, &diags); m.Metadata.Description.ValueString() != "changed elsewhere" {
		t.Fatalf("a stored value must win, got %q", m.Metadata.Description.ValueString())
	}
}
