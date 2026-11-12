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
	back := sharingFromSDK(ctx, sdk, nil, &diags)
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
	if got := sharingFromSDK(context.Background(), nil, nil, &diags); got != nil {
		t.Fatalf("sharingFromSDK(nil) = %+v want nil", got)
	}
}

// A configured empty list, or the same list in another order, is what the state keeps when the platform holds the same
// sharing: Terraform requires the state after an apply to equal the plan.
func TestSharingFromSDKKeepsTheConfiguredFormWhenThePlatformHoldsTheSameSharing(t *testing.T) {
	ctx := context.Background()
	var diags diag.Diagnostics
	configured := &SharingModel{
		ShareMode:  types.StringValue("ALL_WORKSPACES"),
		Workspaces: types.ListValueMust(types.StringType, nil),
		Projects:   types.ListNull(projectSharingObjectType),
	}
	got := sharingFromSDK(ctx, &apiv1.DevSharingSpec{ShareMode: "ALL_WORKSPACES"}, configured, &diags)
	if got == nil || got.Workspaces.IsNull() || len(got.Workspaces.Elements()) != 0 {
		t.Fatalf("an empty configured list must stay an empty list: %+v", got)
	}

	ordered := &SharingModel{
		ShareMode:  types.StringValue("SPECIFIC_WORKSPACES"),
		Workspaces: types.ListValueMust(types.StringType, []attr.Value{types.StringValue("b"), types.StringValue("a")}),
		Projects:   types.ListNull(projectSharingObjectType),
	}
	got = sharingFromSDK(ctx, &apiv1.DevSharingSpec{ShareMode: "SPECIFIC_WORKSPACES", Workspaces: []string{"a", "b"}}, ordered, &diags)
	if first := got.Workspaces.Elements()[0].(types.String).ValueString(); first != "b" {
		t.Fatalf("the configured order must be kept, got first element %q", first)
	}

	// a real difference is still reported as the platform has it
	got = sharingFromSDK(ctx, &apiv1.DevSharingSpec{ShareMode: "SPECIFIC_WORKSPACES", Workspaces: []string{"a", "c"}}, ordered, &diags)
	if got.Workspaces.Elements()[1].(types.String).ValueString() != "c" {
		t.Fatalf("a different platform value must show as drift: %+v", got.Workspaces)
	}
}
