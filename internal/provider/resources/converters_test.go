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
		Labels:      labels,
		Annotations: types.MapNull(types.StringType),
	}
	sdk := metadataToSDK(in)
	if sdk.Name != "vm-1" || sdk.Project != "proj" || sdk.Workspace != "ws" {
		t.Fatalf("metadataToSDK lost fields: %+v", sdk)
	}
	if sdk.Labels["env"] != "dev" {
		t.Fatalf("labels lost: %+v", sdk.Labels)
	}
	if sdk.Annotations != nil {
		t.Fatalf("expected nil annotations, got %+v", sdk.Annotations)
	}

	back := metadataFromSDK(sdk)
	if back.Name.ValueString() != "vm-1" {
		t.Fatalf("metadataFromSDK lost name: %v", back)
	}
	if back.Project.ValueString() != "proj" {
		t.Fatalf("metadataFromSDK lost project: %v", back)
	}
	if !back.Annotations.IsNull() {
		t.Fatalf("expected null annotations on roundtrip, got %v", back.Annotations)
	}
}

func TestDevSharingRoundtrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	var diags diag.Diagnostics

	wsList, _ := types.ListValueFrom(ctx, types.StringType, []string{"ws-a", "ws-b"})
	in := &SharingModel{
		ShareMode:  types.StringValue("SPECIFIC_WORKSPACES"),
		Workspaces: wsList,
		Projects:   types.ListNull(projectSharingObjectType),
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

	wsList, _ := types.ListValueFrom(ctx, types.StringType, []string{"ws-a"})
	projObj, _ := types.ObjectValue(projectSharingObjectType.AttrTypes, map[string]attr.Value{
		"name":       types.StringValue("other-proj"),
		"workspaces": wsList,
	})
	projList, _ := types.ListValue(projectSharingObjectType, []attr.Value{projObj})

	in := &SharingModel{
		ShareMode:  types.StringValue("SPECIFIC_PROJECTS"),
		Workspaces: types.ListNull(types.StringType),
		Projects:   projList,
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
			DNSServers:            types.ListNull(types.StringType),
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
			IPRules: []ipRuleModel{{
				SourceCIDR:  types.StringValue("0.0.0.0/0"),
				Application: types.StringValue("HTTPS"),
				Action:      types.StringValue("ACCEPT"),
			}},
		},
	}
	sdk := securityGroupModelToSDK(ctx, in, &diags)
	if len(sdk.Spec.IPRules) != 1 || sdk.Spec.IPRules[0].SourceCIDR != "0.0.0.0/0" {
		t.Fatalf("securityGroupModelToSDK lost ip rules: %+v", sdk.Spec.IPRules)
	}
	back := securityGroupSDKToModel(ctx, sdk, "p", "", &diags)
	if len(back.Spec.IPRules) != 1 {
		t.Fatalf("securityGroupSDKToModel lost ip rules: %+v", back.Spec.IPRules)
	}
}
