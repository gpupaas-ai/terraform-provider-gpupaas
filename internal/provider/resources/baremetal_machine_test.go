package resources

import (
	"context"
	"testing"

	apiv1 "github.com/gpupaas-ai/gpupaas-go/apis/v1alpha1"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func boolPtr(b bool) *bool { return &b }

// fullBaremetalModel returns a resource model with every spec field populated
// so the round-trip exercises all converters.
func fullBaremetalModel() baremetalMachineResourceModel {
	return baremetalMachineResourceModel{
		Metadata: MetadataModel{
			Name:    types.StringValue("bm-1"),
			Project: types.StringValue("demo"),
		},
		Spec: baremetalMachineSpec{
			Architecture:             types.StringValue("x86_64"),
			AutomatedCleaningMode:    types.StringValue("disabled"),
			BaremetalProvisionerName: types.StringValue("prov-1"),
			BootMode:                 types.StringValue("UEFI"),
			Datacenter:               types.StringValue("dc-1"),
			DeviceID:                 types.StringValue("dev-1"),
			Hostname:                 types.StringValue("host-1"),
			Image: &baremetalImageModel{
				Checksum:     types.StringValue("abc"),
				ChecksumType: types.StringValue("sha256"),
				Format:       types.StringValue("qcow2"),
				URL:          types.StringValue("https://img/os.qcow2"),
			},
			MACAddress: types.StringValue("00:11:22:33:44:55"),
			Online:     types.BoolValue(true),
			Raid: &baremetalRaidModel{
				HardwareRAIDVolumes: []baremetalHardwareRAIDVolumeModel{{
					Controller:            types.StringValue("ctrl-0"),
					Level:                 types.StringValue("1"),
					Name:                  types.StringValue("vol-0"),
					NumberOfPhysicalDisks: types.Int64Value(2),
					PhysicalDisks:         listFromStringSlice([]string{"sda", "sdb"}),
					Rotational:            types.BoolValue(false),
					SizeGibibytes:         types.Int64Value(500),
				}},
				SoftwareRAIDVolumes: []baremetalSoftwareRAIDVolumeModel{{
					Level:         types.StringValue("1"),
					SizeGibibytes: types.Int64Value(100),
					PhysicalDisks: []baremetalRootDeviceHintsModel{{
						DeviceName:       types.StringValue("/dev/sdc"),
						MinSizeGigabytes: types.Int64Value(50),
						Rotational:       types.BoolValue(true),
					}},
				}},
			},
			RootDeviceHints: &baremetalRootDeviceHintsModel{
				DeviceName:         types.StringValue("/dev/sda"),
				HCTL:               types.StringValue("1:0:0:0"),
				MinSizeGigabytes:   types.Int64Value(200),
				Model:              types.StringValue("SAMSUNG"),
				Rotational:         types.BoolValue(false),
				SerialNumber:       types.StringValue("SN123"),
				Vendor:             types.StringValue("Samsung"),
				WWN:                types.StringValue("0x5000"),
				WWNVendorExtension: types.StringValue("ext"),
				WWNWithExtension:   types.StringValue("0x5000ext"),
			},
			SSHKey:         types.StringValue("ssh-ed25519 AAAA"),
			SystemUserData: types.StringValue("#system"),
			UserData:       types.StringValue("#cloud-config"),
		},
	}
}

func TestBaremetalMachineRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	var diags diag.Diagnostics

	in := fullBaremetalModel()
	sdk := baremetalMachineModelToSDK(ctx, in, &diags)
	if diags.HasError() {
		t.Fatalf("modelToSDK diags: %v", diags)
	}

	// Sanity: TypeMeta + scope.
	if sdk.APIVersion != apiv1.APIVersion || sdk.Kind != apiv1.KindBaremetalMachine {
		t.Fatalf("unexpected TypeMeta: %s/%s", sdk.APIVersion, sdk.Kind)
	}
	if sdk.Metadata.Project != "demo" || sdk.Metadata.Name != "bm-1" {
		t.Fatalf("unexpected metadata: %+v", sdk.Metadata)
	}

	got := baremetalMachineSDKToModel(ctx, sdk, "demo", &diags)
	if diags.HasError() {
		t.Fatalf("sdkToModel diags: %v", diags)
	}

	// Spot-check scalar fields survive the round-trip.
	if got.Spec.Architecture.ValueString() != "x86_64" {
		t.Errorf("architecture lost: %q", got.Spec.Architecture.ValueString())
	}
	if got.Spec.Online.ValueBool() != true {
		t.Errorf("online lost: %v", got.Spec.Online)
	}
	if got.Spec.Image == nil || got.Spec.Image.URL.ValueString() != "https://img/os.qcow2" {
		t.Errorf("image lost: %+v", got.Spec.Image)
	}
	if got.Spec.RootDeviceHints == nil || got.Spec.RootDeviceHints.MinSizeGigabytes.ValueInt64() != 200 {
		t.Errorf("root device hints lost: %+v", got.Spec.RootDeviceHints)
	}
	if got.Spec.Raid == nil || len(got.Spec.Raid.HardwareRAIDVolumes) != 1 {
		t.Fatalf("hardware raid lost: %+v", got.Spec.Raid)
	}
	hw := got.Spec.Raid.HardwareRAIDVolumes[0]
	if hw.NumberOfPhysicalDisks.ValueInt64() != 2 {
		t.Errorf("hw disk count lost: %v", hw.NumberOfPhysicalDisks)
	}
	disks := stringSliceFromTF(ctx, hw.PhysicalDisks, &diags)
	if len(disks) != 2 || disks[0] != "sda" {
		t.Errorf("hw physical disks lost: %v", disks)
	}
	if len(got.Spec.Raid.SoftwareRAIDVolumes) != 1 ||
		len(got.Spec.Raid.SoftwareRAIDVolumes[0].PhysicalDisks) != 1 {
		t.Fatalf("software raid lost: %+v", got.Spec.Raid.SoftwareRAIDVolumes)
	}
}

func TestBaremetalMachineEmptyNestedRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	var diags diag.Diagnostics

	in := baremetalMachineResourceModel{
		Metadata: MetadataModel{Name: types.StringValue("bm-min"), Project: types.StringValue("demo")},
		Spec:     baremetalMachineSpec{Hostname: types.StringValue("h")},
	}
	sdk := baremetalMachineModelToSDK(ctx, in, &diags)
	got := baremetalMachineSDKToModel(ctx, sdk, "demo", &diags)
	if diags.HasError() {
		t.Fatalf("diags: %v", diags)
	}
	if got.Spec.Image != nil || got.Spec.Raid != nil || got.Spec.RootDeviceHints != nil {
		t.Fatalf("nil nested blocks should stay nil, got %+v", got.Spec)
	}
}

func TestBaremetalActionDispatchDecision(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name           string
		old, planned   types.String
		shouldDispatch bool
	}{
		{"unset-to-unset", types.StringNull(), types.StringNull(), false},
		{"unset-to-none", types.StringNull(), types.StringValue("none"), false},
		{"none-to-none", types.StringValue("none"), types.StringValue("none"), false},
		{"unset-to-power_on", types.StringNull(), types.StringValue("power_on"), true},
		{"power_on-to-power_on", types.StringValue("power_on"), types.StringValue("power_on"), false},
		{"power_on-to-power_off", types.StringValue("power_on"), types.StringValue("power_off"), true},
		{"reboot-to-provision", types.StringValue("reboot"), types.StringValue("provision"), true},
		{"provision-to-reinstall_os", types.StringValue("provision"), types.StringValue("reinstall_os"), true},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			oldA := normalizedActionValue(tc.old)
			newA := normalizedActionValue(tc.planned)
			got := newA != "" && newA != "none" && newA != oldA
			if got != tc.shouldDispatch {
				t.Fatalf("dispatch(old=%q, new=%q) = %v want %v", oldA, newA, got, tc.shouldDispatch)
			}
		})
	}
}

func TestBaremetalReinstallImage(t *testing.T) {
	t.Parallel()
	if baremetalReinstallImage(nil) != nil {
		t.Fatal("nil inputs must yield nil image")
	}
	if baremetalReinstallImage(&baremetalMachineActionInputs{}) != nil {
		t.Fatal("inputs without image must yield nil image")
	}
	img := baremetalReinstallImage(&baremetalMachineActionInputs{
		Image: &baremetalImageModel{URL: types.StringValue("https://img/new.qcow2")},
	})
	if img == nil || img.URL != "https://img/new.qcow2" {
		t.Fatalf("expected image url propagated, got %+v", img)
	}
}

func TestBaremetalStatusDataJSON(t *testing.T) {
	t.Parallel()
	if !baremetalStatusDataJSON(nil).IsNull() {
		t.Fatal("nil info must yield null")
	}
	if !baremetalStatusDataJSON(&apiv1.BaremetalMachineInfo{}).IsNull() {
		t.Fatal("empty fields must yield null")
	}
	out := baremetalStatusDataJSON(&apiv1.BaremetalMachineInfo{
		Data: apiv1.BaremetalMachineData{Fields: map[string]interface{}{"k": "v"}},
	})
	if out.IsNull() || out.ValueString() != `{"k":"v"}` {
		t.Fatalf("unexpected json: %q", out.ValueString())
	}
}

func TestBaremetalSmallHelpers(t *testing.T) {
	t.Parallel()
	if boolPtrFromTF(types.BoolNull()) != nil || boolPtrFromTF(types.BoolUnknown()) != nil {
		t.Fatal("null/unknown bool must produce nil pointer")
	}
	if p := boolPtrFromTF(types.BoolValue(true)); p == nil || *p != true {
		t.Fatalf("true bool must produce non-nil pointer")
	}
	if !boolFromPtr(nil).IsNull() {
		t.Fatal("nil pointer must produce null bool")
	}
	if boolFromPtr(boolPtr(true)).ValueBool() != true {
		t.Fatal("non-nil pointer must produce matching bool")
	}
	if !int64OrNull(0).IsNull() {
		t.Fatal("zero int64 must produce null")
	}
	if int64OrNull(7).ValueInt64() != 7 {
		t.Fatal("non-zero int64 must produce value")
	}
}
