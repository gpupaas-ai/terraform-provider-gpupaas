package resources

import (
	"context"
	"testing"

	apiv1 "github.com/gpupaas-ai/gpupaas-go/apis/v1alpha1"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestMetadataRoundtrip(t *testing.T) {
	t.Parallel()
	labels, _ := types.MapValueFrom(context.Background(), types.StringType, map[string]string{"env": "dev"})
	in := MetadataModel{
		Name:        types.StringValue("vm-1"),
		Project:     types.StringValue("proj"),
		Workspace:   types.StringValue("ws"),
		DisplayName: types.StringValue("VM One"),
		Description: types.StringValue("First trainer"),
		Labels:      labels,
		Annotations: types.MapNull(types.StringType),
		CreatedBy:   types.ObjectNull(userMetaAttrTypes),
		ModifiedBy:  types.ObjectNull(userMetaAttrTypes),
	}
	sdk := metadataToSDK(in)
	if sdk.Name != "vm-1" || sdk.Project != "proj" || sdk.Workspace != "ws" {
		t.Fatalf("metadataToSDK lost fields: %+v", sdk)
	}
	if sdk.DisplayName != "VM One" || sdk.Description != "First trainer" {
		t.Fatalf("metadataToSDK lost display_name/description: %+v", sdk)
	}
	if sdk.Labels["env"] != "dev" {
		t.Fatalf("labels lost: %+v", sdk.Labels)
	}
	if sdk.Annotations != nil {
		t.Fatalf("expected nil annotations, got %+v", sdk.Annotations)
	}
	if sdk.CreatedBy != nil || sdk.ModifiedBy != nil {
		t.Fatalf("metadataToSDK must strip CreatedBy/ModifiedBy on writes, got: %+v / %+v", sdk.CreatedBy, sdk.ModifiedBy)
	}

	back := metadataFromSDK(sdk)
	if back.Name.ValueString() != "vm-1" {
		t.Fatalf("metadataFromSDK lost name: %v", back)
	}
	if back.Project.ValueString() != "proj" {
		t.Fatalf("metadataFromSDK lost project: %v", back)
	}
	if back.DisplayName.ValueString() != "VM One" || back.Description.ValueString() != "First trainer" {
		t.Fatalf("metadataFromSDK lost display_name/description: %+v", back)
	}
	if !back.Annotations.IsNull() {
		t.Fatalf("expected null annotations on roundtrip, got %v", back.Annotations)
	}
	if !back.CreatedBy.IsNull() || !back.ModifiedBy.IsNull() {
		t.Fatalf("expected null created_by/modified_by, got: %+v / %+v", back.CreatedBy, back.ModifiedBy)
	}
}

func TestMetadataUserMetaObservedFromSDK(t *testing.T) {
	t.Parallel()
	sdkMeta := apiv1.ObjectMeta{
		Name: "vm-1",
		CreatedBy: &apiv1.UserMeta{
			Username:  "alice",
			IsSSOUser: true,
			Options: &apiv1.UserMetaOptions{
				Description: "primary owner",
				Required:    true,
				Override: &apiv1.UserMetaOverrideOptions{
					Type:             "restricted",
					RestrictedValues: []string{"alice", "bob"},
				},
			},
		},
	}
	out := metadataFromSDK(sdkMeta)
	if out.CreatedBy.IsNull() {
		t.Fatalf("metadataFromSDK should surface created_by, got null")
	}
	createdAttrs := out.CreatedBy.Attributes()
	if u := createdAttrs["username"].(types.String); u.ValueString() != "alice" {
		t.Fatalf("created_by.username = %q want alice", u.ValueString())
	}
	if u := createdAttrs["is_sso_user"].(types.Bool); !u.ValueBool() {
		t.Fatalf("created_by.is_sso_user must be true")
	}
	opts := createdAttrs["options"].(types.Object)
	if opts.IsNull() {
		t.Fatalf("created_by.options must not be null")
	}
}

func TestDevSharingRoundtrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	var diags diag.Diagnostics

	wsSet, _ := types.SetValueFrom(ctx, types.StringType, []string{"ws-a", "ws-b"})
	in := &SharingModel{
		ShareMode:  types.StringValue("SPECIFIC_WORKSPACES"),
		Workspaces: wsSet,
		Projects:   types.SetNull(projectSharingObjectType),
	}
	sdk := sharingToSDK(ctx, in, &diags)
	if diags.HasError() {
		t.Fatalf("unexpected diags: %v", diags)
	}
	if sdk == nil || sdk.ShareMode != "SPECIFIC_WORKSPACES" || len(sdk.Workspaces) != 2 {
		t.Fatalf("sharingToSDK lost data: %+v", sdk)
	}
	back := sharingFromSDK(ctx, sdk, &diags)
	if diags.HasError() {
		t.Fatalf("unexpected diags on FromSDK: %v", diags)
	}
	if back == nil || back.ShareMode.ValueString() != "SPECIFIC_WORKSPACES" {
		t.Fatalf("sharingFromSDK lost share_mode: %+v", back)
	}
}

// TestDevSharingWorkspacesOrderInsensitive proves FR-4(a)/(b): reordering
// workspaces in HCL, or the server returning them in a different order,
// produces a semantically-equal types.Set value.
func TestDevSharingWorkspacesOrderInsensitive(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	var diags diag.Diagnostics

	setA, _ := types.SetValueFrom(ctx, types.StringType, []string{"ws-a", "ws-b", "ws-c"})
	setB, _ := types.SetValueFrom(ctx, types.StringType, []string{"ws-c", "ws-a", "ws-b"})

	sdkA := sharingToSDK(ctx, &SharingModel{ShareMode: types.StringValue("SPECIFIC_WORKSPACES"), Workspaces: setA, Projects: types.SetNull(projectSharingObjectType)}, &diags)
	sdkB := sharingToSDK(ctx, &SharingModel{ShareMode: types.StringValue("SPECIFIC_WORKSPACES"), Workspaces: setB, Projects: types.SetNull(projectSharingObjectType)}, &diags)
	if diags.HasError() {
		t.Fatalf("unexpected diags: %v", diags)
	}
	if len(sdkA.Workspaces) != len(sdkB.Workspaces) {
		t.Fatalf("workspace count mismatch: %v vs %v", sdkA.Workspaces, sdkB.Workspaces)
	}
	for i := range sdkA.Workspaces {
		if sdkA.Workspaces[i] != sdkB.Workspaces[i] {
			t.Fatalf("sharingToSDK is not order-canonical: %v vs %v", sdkA.Workspaces, sdkB.Workspaces)
		}
	}

	// Server returns membership in a different order than we sent — FromSDK
	// must still produce an equal set value.
	backA := sharingFromSDK(ctx, sdkA, &diags)
	backB := sharingFromSDK(ctx, sdkB, &diags)
	if !backA.Workspaces.Equal(backB.Workspaces) {
		t.Fatalf("sharingFromSDK produced non-equal sets from permuted input: %v vs %v", backA.Workspaces, backB.Workspaces)
	}
}

func TestDevSharingNilPassthrough(t *testing.T) {
	t.Parallel()
	var diags diag.Diagnostics
	if got := sharingToSDK(context.Background(), nil, &diags); got != nil {
		t.Fatalf("sharingToSDK(nil) = %+v want nil", got)
	}
	if got := sharingFromSDK(context.Background(), nil, &diags); got != nil {
		t.Fatalf("sharingFromSDK(nil) = %+v want nil", got)
	}
}

func TestVMSharingRoundtripWithProjects(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	var diags diag.Diagnostics

	wsSet, _ := types.SetValueFrom(ctx, types.StringType, []string{"ws-a"})
	projObj, _ := types.ObjectValue(projectSharingObjectType.AttrTypes, map[string]attr.Value{
		"name":       types.StringValue("other-proj"),
		"workspaces": wsSet,
	})
	projSet, _ := types.SetValue(projectSharingObjectType, []attr.Value{projObj})

	in := &SharingModel{
		ShareMode:  types.StringValue("SPECIFIC_PROJECTS"),
		Workspaces: types.SetNull(types.StringType),
		Projects:   projSet,
	}
	sdk := vmSharingToSDK(ctx, in, &diags)
	if diags.HasError() {
		t.Fatalf("unexpected diags: %v", diags)
	}
	if sdk == nil || len(sdk.Projects) != 1 || sdk.Projects[0].Name != "other-proj" {
		t.Fatalf("vmSharingToSDK lost project entry: %+v", sdk)
	}
	if len(sdk.Projects[0].Workspaces) != 1 {
		t.Fatalf("vmSharingToSDK lost project workspaces: %+v", sdk.Projects[0].Workspaces)
	}
	back := vmSharingFromSDK(ctx, sdk, &diags)
	if back == nil || back.ShareMode.ValueString() != "SPECIFIC_PROJECTS" {
		t.Fatalf("vmSharingFromSDK lost share_mode: %+v", back)
	}
}

// TestVMSharingProjectsOrderInsensitive proves FR-4 for VM project sharing:
// reordering the `projects` set (and each project's nested `workspaces` set)
// produces the same canonical wire payload and an equal state value back.
func TestVMSharingProjectsOrderInsensitive(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	var diags diag.Diagnostics

	mkProjects := func(order []string, wsOrder []string) types.Set {
		objs := make([]attr.Value, 0, len(order))
		for _, name := range order {
			ws, _ := types.SetValueFrom(ctx, types.StringType, wsOrder)
			o, _ := types.ObjectValue(projectSharingObjectType.AttrTypes, map[string]attr.Value{
				"name":       types.StringValue(name),
				"workspaces": ws,
			})
			objs = append(objs, o)
		}
		s, _ := types.SetValue(projectSharingObjectType, objs)
		return s
	}

	projA := mkProjects([]string{"proj-1", "proj-2"}, []string{"ws-a", "ws-b"})
	projB := mkProjects([]string{"proj-2", "proj-1"}, []string{"ws-b", "ws-a"})

	sdkA := vmSharingToSDK(ctx, &SharingModel{ShareMode: types.StringValue("SPECIFIC_PROJECTS"), Workspaces: types.SetNull(types.StringType), Projects: projA}, &diags)
	sdkB := vmSharingToSDK(ctx, &SharingModel{ShareMode: types.StringValue("SPECIFIC_PROJECTS"), Workspaces: types.SetNull(types.StringType), Projects: projB}, &diags)
	if diags.HasError() {
		t.Fatalf("unexpected diags: %v", diags)
	}
	if len(sdkA.Projects) != 2 || len(sdkB.Projects) != 2 {
		t.Fatalf("expected 2 projects each: %+v / %+v", sdkA.Projects, sdkB.Projects)
	}
	for i := range sdkA.Projects {
		if sdkA.Projects[i].Name != sdkB.Projects[i].Name {
			t.Fatalf("vmSharingToSDK project order not canonical: %+v vs %+v", sdkA.Projects, sdkB.Projects)
		}
		if len(sdkA.Projects[i].Workspaces) != len(sdkB.Projects[i].Workspaces) {
			t.Fatalf("workspace count mismatch: %+v vs %+v", sdkA.Projects[i], sdkB.Projects[i])
		}
		for j := range sdkA.Projects[i].Workspaces {
			if sdkA.Projects[i].Workspaces[j] != sdkB.Projects[i].Workspaces[j] {
				t.Fatalf("nested workspaces not canonical: %+v vs %+v", sdkA.Projects[i], sdkB.Projects[i])
			}
		}
	}

	backA := vmSharingFromSDK(ctx, sdkA, &diags)
	backB := vmSharingFromSDK(ctx, sdkB, &diags)
	if !backA.Projects.Equal(backB.Projects) {
		t.Fatalf("vmSharingFromSDK produced non-equal project sets from permuted input: %v vs %v", backA.Projects, backB.Projects)
	}
}

func TestResourceRefRoundtrip(t *testing.T) {
	t.Parallel()
	in := resourceRefModel{
		Name:          types.StringValue("Ubuntu-22.04"),
		SystemCatalog: types.BoolValue(true),
	}
	sdk := resourceRefToSDK(in)
	if sdk.Name != "Ubuntu-22.04" || !sdk.SystemCatalog {
		t.Fatalf("resourceRefToSDK lost data: %+v", sdk)
	}
	back := resourceRefFromSDK(sdk)
	if back.Name.ValueString() != "Ubuntu-22.04" || !back.SystemCatalog.ValueBool() {
		t.Fatalf("resourceRefFromSDK lost data: %+v", back)
	}
}

func TestVirtualMachineRoundtrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	var diags diag.Diagnostics

	in := virtualMachineResourceModel{
		Metadata: MetadataModel{
			Name:        types.StringValue("vm-1"),
			Project:     types.StringValue("proj"),
			Workspace:   types.StringValue("ws"),
			Labels:      types.MapNull(types.StringType),
			Annotations: types.MapNull(types.StringType),
		},
		Spec: virtualMachineSpec{
			VirtualMachine:        resourceRefModel{Name: types.StringValue("Small"), SystemCatalog: types.BoolValue(true)},
			Type:                  types.StringValue("VM"),
			Name:                  types.StringValue("vm-1"),
			CPUCount:              types.StringValue("4"),
			Memory:                types.StringValue("16G"),
			AssignPublicIP:        types.BoolValue(true),
			BootDiskSize:          types.Int64Value(100),
			CreateAdditionalBlock: types.BoolValue(false),
			AdditionalBlockSize:   types.Int64Value(0),
			DNSServers:            types.SetNull(types.StringType),
		},
	}
	sdk := virtualMachineModelToSDK(ctx, in, &diags)
	if diags.HasError() {
		t.Fatalf("unexpected diags: %v", diags)
	}
	if sdk.Spec.VirtualMachine.Name != "Small" || !sdk.Spec.AssignPublicIP || sdk.Spec.BootDiskSize != 100 {
		t.Fatalf("virtualMachineModelToSDK lost fields: %+v", sdk.Spec)
	}
	if sdk.Kind != apiv1.KindVirtualMachine {
		t.Fatalf("kind not set: %q", sdk.Kind)
	}

	// Apply a status to verify SDKToModel maps it.
	sdk.Status.Status = "RUNNING"
	sdk.Status.Output = &apiv1.VirtualMachineOutput{PublicIP: "1.2.3.4"}
	back := virtualMachineSDKToModel(ctx, sdk, "proj", "ws", &diags)
	if back.Status.Status.ValueString() != "RUNNING" {
		t.Fatalf("status lost: %v", back.Status)
	}
	if back.Status.Output == nil || back.Status.Output.PublicIP.ValueString() != "1.2.3.4" {
		t.Fatalf("output lost: %+v", back.Status.Output)
	}
	if back.ID.ValueString() != "proj/ws/vm-1" {
		t.Fatalf("id wrong: %q", back.ID.ValueString())
	}
}

func TestStorageRoundtrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	var diags diag.Diagnostics
	in := storageResourceModel{
		Metadata: MetadataModel{
			Name:        types.StringValue("s1"),
			Project:     types.StringValue("p"),
			Workspace:   types.StringValue(""),
			Labels:      types.MapNull(types.StringType),
			Annotations: types.MapNull(types.StringType),
		},
		Spec: storageSpec{
			Storage: resourceRefModel{Name: types.StringValue("ssd"), SystemCatalog: types.BoolValue(true)},
			Size:    types.StringValue("100G"),
		},
	}
	sdk := storageModelToSDK(ctx, in, &diags)
	if sdk.Spec.Size != "100G" || sdk.Spec.Storage.Name != "ssd" {
		t.Fatalf("storageModelToSDK lost data: %+v", sdk.Spec)
	}
	back := storageSDKToModel(ctx, sdk, "p", "", &diags)
	if back.ID.ValueString() != "p/s1" {
		t.Fatalf("id wrong: %q", back.ID.ValueString())
	}
}

func TestSshKeyRoundtrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	var diags diag.Diagnostics
	in := sshKeyResourceModel{
		Metadata: MetadataModel{
			Name:        types.StringValue("dev-key"),
			Project:     types.StringValue("p"),
			Workspace:   types.StringValue(""),
			Labels:      types.MapNull(types.StringType),
			Annotations: types.MapNull(types.StringType),
		},
		Spec: sshKeySpec{
			SSHKey:    resourceRefModel{Name: types.StringValue("dev-key"), SystemCatalog: types.BoolValue(false)},
			PublicKey: types.StringValue("ssh-ed25519 AAAA..."),
		},
	}
	sdk := sshKeyModelToSDK(ctx, in, &diags)
	if sdk.Spec.PublicKey == "" {
		t.Fatalf("sshKeyModelToSDK lost public key")
	}
	back := sshKeySDKToModel(ctx, sdk, "p", "", &diags)
	if back.Spec.PublicKey.ValueString() == "" {
		t.Fatalf("sshKeySDKToModel lost public key")
	}
}

func mkIPRuleSet(t *testing.T, ctx context.Context, rules []ipRuleModel) types.Set {
	t.Helper()
	objs := make([]attr.Value, 0, len(rules))
	for _, r := range rules {
		o, d := types.ObjectValue(ipRuleObjectType.AttrTypes, map[string]attr.Value{
			"source_cidr": r.SourceCIDR,
			"application": r.Application,
			"action":      r.Action,
		})
		if d.HasError() {
			t.Fatalf("unexpected diags building ip rule object: %v", d)
		}
		objs = append(objs, o)
	}
	set, d := types.SetValue(ipRuleObjectType, objs)
	if d.HasError() {
		t.Fatalf("unexpected diags building ip rule set: %v", d)
	}
	return set
}

func TestSecurityGroupRoundtrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	var diags diag.Diagnostics
	in := securityGroupResourceModel{
		Metadata: MetadataModel{
			Name:        types.StringValue("sg1"),
			Project:     types.StringValue("p"),
			Workspace:   types.StringValue(""),
			Labels:      types.MapNull(types.StringType),
			Annotations: types.MapNull(types.StringType),
		},
		Spec: securityGroupSpec{
			SecurityGroup: resourceRefModel{Name: types.StringValue("default"), SystemCatalog: types.BoolValue(true)},
			IPRules: mkIPRuleSet(t, ctx, []ipRuleModel{{
				SourceCIDR:  types.StringValue("0.0.0.0/0"),
				Application: types.StringValue("HTTPS"),
				Action:      types.StringValue("ACCEPT"),
			}}),
			PortForwardRules: types.SetNull(portForwardRuleObjectType),
			Rules:            types.SetNull(ruleObjectType),
		},
	}
	sdk := securityGroupModelToSDK(ctx, in, &diags)
	if diags.HasError() {
		t.Fatalf("unexpected diags: %v", diags)
	}
	if len(sdk.Spec.IPRules) != 1 || sdk.Spec.IPRules[0].SourceCIDR != "0.0.0.0/0" {
		t.Fatalf("securityGroupModelToSDK lost ip rules: %+v", sdk.Spec.IPRules)
	}
	back := securityGroupSDKToModel(ctx, sdk, "p", "", &diags)
	var backRules []ipRuleModel
	diags.Append(back.Spec.IPRules.ElementsAs(ctx, &backRules, false)...)
	if len(backRules) != 1 {
		t.Fatalf("securityGroupSDKToModel lost ip rules: %+v", backRules)
	}
}

// TestSecurityGroupRulesOrderInsensitive proves FR-5: reordering ip_rules in
// HCL (or receiving them from the server in a different order) produces a
// semantically-equal types.Set and an identical canonical wire payload.
func TestSecurityGroupRulesOrderInsensitive(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	var diags diag.Diagnostics

	ruleA := ipRuleModel{SourceCIDR: types.StringValue("10.0.0.0/8"), Application: types.StringValue("SSH"), Action: types.StringValue("ACCEPT")}
	ruleB := ipRuleModel{SourceCIDR: types.StringValue("0.0.0.0/0"), Application: types.StringValue("HTTPS"), Action: types.StringValue("ACCEPT")}

	setForward := mkIPRuleSet(t, ctx, []ipRuleModel{ruleA, ruleB})
	setReverse := mkIPRuleSet(t, ctx, []ipRuleModel{ruleB, ruleA})

	if !setForward.Equal(setReverse) {
		t.Fatalf("Terraform set values should already be order-insensitive: %v vs %v", setForward, setReverse)
	}

	sdkForward := ipRulesFromTF(ctx, setForward, &diags)
	sdkReverse := ipRulesFromTF(ctx, setReverse, &diags)
	if diags.HasError() {
		t.Fatalf("unexpected diags: %v", diags)
	}
	if len(sdkForward) != 2 || len(sdkReverse) != 2 {
		t.Fatalf("expected 2 rules each: %+v / %+v", sdkForward, sdkReverse)
	}
	for i := range sdkForward {
		if sdkForward[i] != sdkReverse[i] {
			t.Fatalf("ipRulesFromTF is not order-canonical: %+v vs %+v", sdkForward, sdkReverse)
		}
	}

	// Server returns the rules in yet another order — FromSDK canonicalizes.
	shuffled := []apiv1.IpRule{
		{SourceCIDR: ruleB.SourceCIDR.ValueString(), Application: ruleB.Application.ValueString(), Action: ruleB.Action.ValueString()},
		{SourceCIDR: ruleA.SourceCIDR.ValueString(), Application: ruleA.Application.ValueString(), Action: ruleA.Action.ValueString()},
	}
	got := ipRulesToTFSet(shuffled, &diags)
	if !got.Equal(setForward) {
		t.Fatalf("ipRulesToTFSet produced non-equal set from permuted input: %v vs %v", got, setForward)
	}
}

// TestVirtualMachineGuestPasswordWriteOnly proves FR-10: the SDK->model
// converter must never surface a guest_password value, even when the SDK
// object carries one (e.g. an encrypted echo). Callers (Create/Read/Update)
// are responsible for re-attaching the prior plan/state value afterwards.
func TestVirtualMachineGuestPasswordWriteOnly(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	var diags diag.Diagnostics

	sdk := &apiv1.VirtualMachine{
		Metadata: apiv1.ObjectMeta{Name: "vm-1", Project: "p"},
		Spec: apiv1.VirtualMachineSpec{
			VirtualMachine: apiv1.ResourceRef{Name: "S"},
			GuestPassword:  "s3cr3t-or-encrypted-echo",
		},
	}
	model := virtualMachineSDKToModel(ctx, sdk, "p", "", &diags)
	if diags.HasError() {
		t.Fatalf("unexpected diags: %v", diags)
	}
	if !model.Spec.GuestPassword.IsNull() {
		t.Fatalf("virtualMachineSDKToModel must never populate guest_password, got %q", model.Spec.GuestPassword.ValueString())
	}
}
