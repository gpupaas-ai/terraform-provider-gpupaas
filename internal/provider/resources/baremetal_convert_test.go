package resources

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	apiv1 "github.com/gpupaas-ai/gpupaas-go/apis/v1alpha1"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func serverBareMetal() *apiv1.BareMetal {
	return &apiv1.BareMetal{
		Metadata: apiv1.ObjectMeta{Name: "b1", Project: "p", Workspace: "ws", Labels: map[string]string{"paas.envmgmt.io/compute_type": "baremetal"}},
		Spec: apiv1.BareMetalSpec{
			Type: "bm-l40s", Image: "ubuntu-22", SSHKey: "mine", Datacenter: "dc1", UserData: "I2Nsb3VkLWNvbmZpZw==", GPUName: "L40S",
			ComputeInstanceID: "ci-1", ProvisionerType: "bcm", Sharing: &apiv1.DevSharingSpec{ShareMode: "None"},
			Extra: map[string]json.RawMessage{"serverOnly": json.RawMessage(`"keep"`)},
		},
		Status: apiv1.BareMetalStatus{Status: "running", Reason: "Instance b1 is running.", Action: "deploy", Output: &apiv1.BareMetalOutput{HostName: "h", UserName: "ubuntu"}},
	}
}

func plannedBM() bareMetalResourceModel {
	return bareMetalResourceModel{
		Metadata: MetadataModel{Name: str("b1"), Project: str("p"), Workspace: str("ws"), Labels: types.MapNull(types.StringType), Annotations: types.MapNull(types.StringType)},
		Spec: bareMetalSpecModel{
			Type: str("bm-l40s"), Image: str("ubuntu-22"), SSHKey: str("mine"),
			// what UseStateForUnknown carries into an update plan
			Datacenter: str("dc1"), AssignPublicIP: types.BoolValue(false), ContractTerm: types.Int64Value(0),
			UserData: str("#cloud-config"),
		},
	}
}

func TestBuildBareMetalCreateSendsThePasswordAndDefaultsSharing(t *testing.T) {
	plan := plannedBM()
	plan.Spec.SSHKey = types.StringNull()
	plan.Spec.UserPassword = str("pw")
	var d diag.Diagnostics
	obj := buildBareMetal(context.Background(), plan, nil, nil, &d)
	if d.HasError() {
		t.Fatal(d)
	}
	if obj.Spec.UserPassword != "pw" || obj.Spec.Type != "bm-l40s" || obj.Spec.Image != "ubuntu-22" || obj.Spec.UserData != "#cloud-config" {
		t.Fatalf("spec = %+v", obj.Spec)
	}
	if obj.Spec.Sharing == nil || obj.Spec.Sharing.ShareMode != "None" {
		t.Fatalf("sharing = %#v, want None", obj.Spec.Sharing)
	}
}

func TestBuildBareMetalUpdateKeepsWhatThePlatformHoldsAndIsQuietWhenNothingChanged(t *testing.T) {
	cur := serverBareMetal()
	plan := plannedBM()
	plan.Spec.UserPassword = str("pw")
	prior := plan.Spec // the state the plan was made against: same password, same user data
	var d diag.Diagnostics
	obj := buildBareMetal(context.Background(), plan, &prior, cur, &d)
	if obj.Spec.UserPassword != "" {
		t.Fatal("an unchanged password was sent again")
	}
	// User data comes back in the platform's own encoding: an unchanged value keeps it.
	if obj.Spec.UserData != "I2Nsb3VkLWNvbmZpZw==" {
		t.Fatalf("user data = %q", obj.Spec.UserData)
	}
	if obj.Spec.ComputeInstanceID != "ci-1" || obj.Spec.ProvisionerType != "bcm" || obj.Spec.GPUName != "L40S" {
		t.Fatalf("platform fields lost: %+v", obj.Spec)
	}
	if string(obj.Spec.Extra["serverOnly"]) != `"keep"` {
		t.Fatalf("unmodeled field lost: %v", obj.Spec.Extra)
	}
	if obj.Metadata.Labels["paas.envmgmt.io/compute_type"] != "baremetal" {
		t.Fatalf("system labels lost: %v", obj.Metadata.Labels)
	}
	if !sameBareMetalRequest(obj, cur) {
		t.Fatal("nothing changed but the request differs from what the platform holds")
	}

	// A new password, new user data, or a changed field is a real change.
	plan.Spec.UserPassword = str("other")
	if o := buildBareMetal(context.Background(), plan, &prior, cur, &d); o.Spec.UserPassword != "other" || sameBareMetalRequest(o, cur) {
		t.Fatal("a changed password was not sent")
	}
	plan = plannedBM()
	plan.Spec.UserData = str("#new")
	if o := buildBareMetal(context.Background(), plan, &prior, cur, &d); o.Spec.UserData != "#new" || sameBareMetalRequest(o, cur) {
		t.Fatalf("changed user data = %q", o.Spec.UserData)
	}
	plan = plannedBM()
	plan.Spec.SecurityGroup = str("sg")
	if o := buildBareMetal(context.Background(), plan, &prior, cur, &d); o.Spec.SecurityGroup != "sg" || sameBareMetalRequest(o, cur) {
		t.Fatal("a changed security group was not sent")
	}
}

func TestBuildBareMetalUpdateClearsRemovedOptionalAttributes(t *testing.T) {
	cur := serverBareMetal()
	cur.Spec.SecurityGroup, cur.Spec.HSSStorage = "sg", "fs"
	plan := plannedBM() // security_group and hss_storage no longer configured
	prior := plan.Spec
	var d diag.Diagnostics
	obj := buildBareMetal(context.Background(), plan, &prior, cur, &d)
	if obj.Spec.SecurityGroup != "" || obj.Spec.HSSStorage != "" {
		t.Fatalf("removed attributes were kept: %+v", obj.Spec)
	}
}

func TestBareMetalModelFromServer(t *testing.T) {
	var d diag.Diagnostics
	ctx := context.Background()

	// Create/update: configured values stay, computed ones come from the platform, default
	// sharing is not shown, the password and user data stay as configured.
	base := bareMetalResourceModel{
		Metadata: MetadataModel{Name: str("b1"), Project: str("p"), Workspace: str("ws")},
		Spec: bareMetalSpecModel{
			Type: str("bm-l40s"), Image: str("ubuntu-22"), SSHKey: str("mine"), UserPassword: str("pw"), UserData: str("#cloud-config"),
			Datacenter: types.StringNull(), VPC: types.StringNull(), Subnet: types.StringNull(),
			AssignPublicIP: types.BoolNull(), ContractTerm: types.Int64Null(),
			// decodeState hands an unset block back as an object whose attributes are all null
			Sharing: &SharingModel{ShareMode: types.StringNull(), Workspaces: types.ListNull(types.StringType), Projects: types.ListNull(projectSharingObjectType)},
		},
	}
	out := bareMetalModelFromServer(ctx, base, serverBareMetal(), true, &d)
	if d.HasError() {
		t.Fatal(d)
	}
	s := out.Spec
	if s.Datacenter.ValueString() != "dc1" || s.GPUName.ValueString() != "L40S" || s.ProvisionerType.ValueString() != "bcm" || s.ComputeInstanceID.ValueString() != "ci-1" {
		t.Fatalf("computed fields = %+v", s)
	}
	if s.Sharing != nil {
		t.Fatalf("default sharing shown: %+v", s.Sharing)
	}
	if s.UserPassword.ValueString() != "pw" || s.UserData.ValueString() != "#cloud-config" {
		t.Fatalf("write-only/encoded fields changed: password=%v userdata=%v", s.UserPassword.IsNull(), s.UserData)
	}
	if !s.VPC.IsNull() || !s.Subnet.IsNull() {
		t.Fatal("empty server values must be null, not unknown")
	}
	if out.Status.Phase.ValueString() != "running" || out.Status.PowerState.ValueString() != "running" || out.Status.HostName.ValueString() != "h" || !out.Status.PublicIP.IsNull() {
		t.Fatalf("status = %+v", out.Status)
	}
	if out.ID.ValueString() != "p/ws/b1" {
		t.Fatalf("id = %v", out.ID)
	}

	// Drift: a value changed outside Terraform is reported.
	drifted := serverBareMetal()
	drifted.Spec.Image = "ubuntu-24"
	drifted.Spec.Datacenter = "dc2"
	out = bareMetalModelFromServer(ctx, out, drifted, true, &d)
	if out.Spec.Image.ValueString() != "ubuntu-24" || out.Spec.Datacenter.ValueString() != "dc2" {
		t.Fatalf("drift not reported: image=%v datacenter=%v", out.Spec.Image, out.Spec.Datacenter)
	}

	// Import: nothing is known yet, every field takes the platform's value.
	imp := bareMetalModelFromServer(ctx, bareMetalResourceModel{Metadata: MetadataModel{Name: str("b1"), Project: str("p"), Workspace: str("ws")}}, serverBareMetal(), true, &d)
	if imp.Spec.Type.ValueString() != "bm-l40s" || imp.Spec.Image.ValueString() != "ubuntu-22" || imp.Spec.SSHKey.ValueString() != "mine" || imp.Spec.UserData.ValueString() != "I2Nsb3VkLWNvbmZpZw==" {
		t.Fatalf("import = %+v", imp.Spec)
	}

	// Non-default sharing is shown even when none was configured (someone shared it).
	shared := serverBareMetal()
	shared.Spec.Sharing = &apiv1.DevSharingSpec{ShareMode: "ALL_WORKSPACES"}
	out = bareMetalModelFromServer(ctx, base, shared, true, &d)
	if out.Spec.Sharing == nil || out.Spec.Sharing.ShareMode.ValueString() != "ALL_WORKSPACES" {
		t.Fatalf("sharing = %+v", out.Spec.Sharing)
	}
}

func TestBareMetalActionNeeded(t *testing.T) {
	cases := []struct {
		action, prior, power string
		want                 bool
	}{
		{"start", "", "stopped", true},
		{"start", "start", "stopped", true}, // level triggered: the machine is down
		{"start", "", "running", false},
		{"stop", "", "running", true},
		{"stop", "stop", "stopped", false},
		{"reboot", "", "running", true},
		{"reboot", "reboot", "running", false}, // edge triggered
		{"reboot", "stop", "running", true},
		{"power_cycle", "", "running", true},
		{"power_cycle", "power_cycle", "running", false}, // edge triggered
		{"power_reset", "power_cycle", "running", true},
	}
	for _, c := range cases {
		if got := bareMetalActionNeeded(c.action, c.prior, c.power); got != c.want {
			t.Errorf("bareMetalActionNeeded(%q, %q, %q) = %v, want %v", c.action, c.prior, c.power, got, c.want)
		}
	}
}

func TestBareMetalGoneToleratesAFailedRecordThatHasNotStartedItsTeardown(t *testing.T) {
	failedDeploy := &apiv1.BareMetal{Metadata: apiv1.ObjectMeta{Name: "b1"}, Status: apiv1.BareMetalStatus{Status: "failed", Action: "deploy", Reason: "no capacity"}}
	done := bareMetalGone(2)
	for i := 0; i < 2; i++ {
		if _, err := done(failedDeploy); err != nil {
			t.Fatalf("poll %d: failed with %v; the teardown may simply not have started", i+1, err)
		}
	}
	if _, err := done(failedDeploy); err == nil {
		t.Fatal("a record still failed after the tolerated polls must fail the delete")
	}

	// Progress resets the allowance.
	done = bareMetalGone(1)
	deleting := &apiv1.BareMetal{Status: apiv1.BareMetalStatus{Status: "deleting", Action: "destroy"}}
	_, _ = done(failedDeploy)
	if _, err := done(deleting); err != nil {
		t.Fatal(err)
	}
	if _, err := done(failedDeploy); err != nil {
		t.Fatalf("allowance was not reset: %v", err)
	}

	// A failed teardown ends the wait at once.
	for _, st := range []apiv1.BareMetalStatus{
		{Status: "destroyfailed", Action: "destroy", Reason: "BMC did not answer"},
		{Status: "delete_pending", Action: "destroy"},
		{Status: "failed", Action: "destroy"},
	} {
		if _, err := bareMetalGone(8)(&apiv1.BareMetal{Status: st}); err == nil {
			t.Errorf("status %q after destroy did not fail the delete", st.Status)
		}
	}
}

func TestValidateBareMetalAgainstProfileOnlyWarnsAboutUnchangedValues(t *testing.T) {
	profile := &apiv1.BmProfile{
		Metadata: apiv1.ObjectMeta{Name: "bm-l40s"},
		Spec:     apiv1.BmProfileSpec{Image: []string{"ubuntu-24"}, Datacenter: []string{"dc1"}, AssignPublicIP: "Disabled", MRCContractTerm: []int32{12}},
	}
	spec := bareMetalSpecModel{Image: str("ubuntu-22"), Datacenter: str("dc9"), AssignPublicIP: types.BoolValue(true), ContractTerm: types.Int64Value(3)}

	// create: every mismatch is an error
	var d diag.Diagnostics
	validateBareMetalAgainstProfile(spec, nil, profile, &d)
	if d.ErrorsCount() != 4 {
		t.Fatalf("create: %d errors, want 4: %v", d.ErrorsCount(), d)
	}

	// existing resource, values unchanged since the SKU was edited: warnings only, so the
	// resource stays updatable and destroyable
	prior := spec
	d = nil
	validateBareMetalAgainstProfile(spec, &prior, profile, &d)
	if d.HasError() || d.WarningsCount() != 4 {
		t.Fatalf("unchanged: errors=%d warnings=%d: %v", d.ErrorsCount(), d.WarningsCount(), d)
	}
	for _, w := range d.Warnings() {
		if !strings.Contains(w.Detail(), "not blocking this plan") {
			t.Errorf("warning does not say it is not blocking: %s", w.Detail())
		}
	}

	// a changed value is an error again
	changed := spec
	changed.Image = str("ubuntu-20")
	d = nil
	validateBareMetalAgainstProfile(changed, &prior, profile, &d)
	if d.ErrorsCount() != 1 || d.WarningsCount() != 3 {
		t.Fatalf("changed: errors=%d warnings=%d: %v", d.ErrorsCount(), d.WarningsCount(), d)
	}
}

// Secrets must be hidden in plans and CLI output: the password, and the cloud-init script, which
// commonly carries credentials (the platform's own node cloud-init embeds them).
func TestBareMetalSecretAttributesAreSensitive(t *testing.T) {
	var resp resource.SchemaResponse
	NewBareMetalResource().Schema(context.Background(), resource.SchemaRequest{}, &resp)
	spec, ok := resp.Schema.Attributes["spec"].(schema.SingleNestedAttribute)
	if !ok {
		t.Fatal("spec is not a nested attribute")
	}
	for _, name := range []string{"user_password", "user_data"} {
		a, ok := spec.Attributes[name].(schema.StringAttribute)
		if !ok || !a.Sensitive {
			t.Errorf("spec.%s must be sensitive", name)
		}
	}
}
