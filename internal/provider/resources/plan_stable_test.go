package resources

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	rschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// bmPlanFixture builds a state of a running machine, a plan that marks every computed attribute
// unknown (what the framework does for a day-2 edit) and a configuration that leaves the
// description and display name unset.
func bmPlanFixture(t *testing.T, replaceType bool) (resource.ModifyPlanRequest, *resource.ModifyPlanResponse) {
	t.Helper()
	ctx := context.Background()
	var sr resource.SchemaResponse
	(&bareMetalResource{}).Schema(ctx, resource.SchemaRequest{}, &sr)
	sch := sr.Schema

	mdAttrs := sch.Attributes["metadata"].(rschema.SingleNestedAttribute).Attributes
	userType := mdAttrs["created_by"].GetType().(types.ObjectType)
	optionsType := userType.AttrTypes["options"].(types.ObjectType)
	user := func(name string) types.Object {
		return types.ObjectValueMust(userType.AttrTypes, map[string]attr.Value{
			"username": types.StringValue(name), "is_sso_user": types.BoolValue(false), "options": types.ObjectNull(optionsType.AttrTypes),
		})
	}
	statusType := sch.Attributes["status"].GetType().(types.ObjectType)

	prior := bareMetalResourceModel{
		ID: types.StringValue("system-catalog/ws/b1"),
		Metadata: MetadataModel{
			Name: types.StringValue("b1"), Project: types.StringValue("system-catalog"), Workspace: types.StringValue("ws"),
			DisplayName: types.StringValue("Nice"), Description: types.StringValue("the description"),
			Labels: types.MapValueMust(types.StringType, map[string]attr.Value{"a": types.StringValue("1")}), Annotations: types.MapNull(types.StringType),
			CreatedBy: user("creator"), ModifiedBy: user("creator"),
		},
		Spec: bareMetalSpecModel{
			Type: types.StringValue("bm-l40s"), Image: types.StringValue("ubuntu-22"), AssignPublicIP: types.BoolValue(false),
		},
		Status: bareMetalStatusModel{
			Phase: types.StringValue("running"), Status: types.StringValue("running"), Reason: types.StringValue("is running"),
			Action: types.StringValue("deploy"), PowerState: types.StringValue("running"), HostName: types.StringValue("h1"),
			PrivateIP: types.StringValue("10.0.0.2"), PublicIP: types.StringNull(), UserName: types.StringValue("ubuntu"),
		},
	}
	state := tfsdk.State{Schema: sch}
	if d := state.Set(ctx, &prior); d.HasError() {
		t.Fatalf("state: %v", d)
	}

	plan := tfsdk.Plan{Schema: sch, Raw: state.Raw.Copy()}
	set := func(s *tfsdk.Plan, p path.Path, v attr.Value) {
		if d := s.SetAttribute(ctx, p, v); d.HasError() {
			t.Fatalf("plan %s: %v", p, d)
		}
	}
	md := path.Root("metadata")
	set(&plan, path.Root("spec").AtName("assign_public_ip"), types.BoolValue(true))
	set(&plan, md.AtName("display_name"), types.StringUnknown())
	set(&plan, md.AtName("description"), types.StringUnknown())
	set(&plan, md.AtName("labels"), types.MapUnknown(types.StringType))
	set(&plan, md.AtName("created_by"), types.ObjectUnknown(userType.AttrTypes))
	set(&plan, md.AtName("modified_by"), types.ObjectUnknown(userType.AttrTypes))
	set(&plan, path.Root("status"), types.ObjectUnknown(statusType.AttrTypes))
	if replaceType {
		set(&plan, path.Root("spec").AtName("type"), types.StringValue("another-sku"))
	}

	cfgState := tfsdk.State{Schema: sch, Raw: state.Raw.Copy()}
	_ = cfgState.SetAttribute(ctx, md.AtName("display_name"), types.StringNull())
	_ = cfgState.SetAttribute(ctx, md.AtName("description"), types.StringNull())
	cfg := tfsdk.Config{Schema: sch, Raw: cfgState.Raw}

	req := resource.ModifyPlanRequest{Config: cfg, Plan: plan, State: state}
	return req, &resource.ModifyPlanResponse{Plan: plan}
}

// testStableStatus is the status attribute the tests treat as stable; the helper accepts any name.
var testStableStatus = []string{"user_name"}

func planValue(t *testing.T, resp *resource.ModifyPlanResponse, p path.Path) attr.Value {
	t.Helper()
	var v attr.Value
	if d := resp.Plan.GetAttribute(context.Background(), p, &v); d.HasError() {
		t.Fatalf("read %s: %v", p, d)
	}
	return v
}

// Flipping the public IP must plan one real change. What cannot change keeps its value, and only what the platform
// may change stays unknown.
func TestKeepStableOnUpdateLeavesOnlyWhatCanChangeUnknown(t *testing.T) {
	req, resp := bmPlanFixture(t, false)
	keepStableOnUpdate(context.Background(), req, resp, testStableStatus...)
	if resp.Diagnostics.HasError() {
		t.Fatalf("diagnostics: %v", resp.Diagnostics)
	}
	md, st := path.Root("metadata"), path.Root("status")

	known := map[string]path.Path{
		"display_name": md.AtName("display_name"), "description": md.AtName("description"), "created_by": md.AtName("created_by"),
		"status.user_name": st.AtName("user_name"),
	}
	for name, p := range known {
		if planValue(t, resp, p).IsUnknown() {
			t.Errorf("%s is unknown in the plan, want the value from the state", name)
		}
	}
	if got := planValue(t, resp, md.AtName("description")).(types.String).ValueString(); got != "the description" {
		t.Errorf("description = %q", got)
	}
	if got := planValue(t, resp, st.AtName("user_name")).(types.String).ValueString(); got != "ubuntu" {
		t.Errorf("user_name = %q", got)
	}

	unknown := map[string]path.Path{
		"labels": md.AtName("labels"), "modified_by": md.AtName("modified_by"),
		"status.phase": st.AtName("phase"), "status.public_ip": st.AtName("public_ip"), "status.private_ip": st.AtName("private_ip"),
		"status.reason": st.AtName("reason"), "status.action": st.AtName("action"), "status.power_state": st.AtName("power_state"),
		"status.host_name": st.AtName("host_name"),
	}
	for name, p := range unknown {
		if !planValue(t, resp, p).IsUnknown() {
			t.Errorf("%s is known in the plan, want unknown: the platform may change it", name)
		}
	}
}

// A plan that replaces the machine has no state to keep.
func TestKeepStableOnUpdateLeavesAReplacementAlone(t *testing.T) {
	req, resp := bmPlanFixture(t, true)
	resp.RequiresReplace = path.Paths{path.Root("spec").AtName("type")} // what the type's RequiresReplace modifier reports
	keepStableOnUpdate(context.Background(), req, resp, testStableStatus...)
	if !planValue(t, resp, path.Root("status")).IsUnknown() {
		t.Error("status of a replaced machine must stay unknown")
	}
	if !planValue(t, resp, path.Root("metadata").AtName("created_by")).IsUnknown() {
		t.Error("created_by of a replaced machine must stay unknown")
	}
}

// A create has no state at all.
func TestKeepStableOnUpdateIgnoresACreate(t *testing.T) {
	req, resp := bmPlanFixture(t, false)
	req.State = tfsdk.State{Schema: req.State.Schema}
	keepStableOnUpdate(context.Background(), req, resp, testStableStatus...)
	if !planValue(t, resp, path.Root("status")).IsUnknown() {
		t.Error("status must stay unknown on a create")
	}
}

// A workspace has no status: only its metadata is kept, and the creator stays known when the
// description is edited.
func TestKeepStableOnUpdateForAWorkspace(t *testing.T) {
	ctx := context.Background()
	var sr resource.SchemaResponse
	(&workspaceResource{}).Schema(ctx, resource.SchemaRequest{}, &sr)
	sch := sr.Schema
	mdAttrs := sch.Attributes["metadata"].(rschema.SingleNestedAttribute).Attributes
	userType := mdAttrs["created_by"].GetType().(types.ObjectType)
	optionsType := userType.AttrTypes["options"].(types.ObjectType)
	user := types.ObjectValueMust(userType.AttrTypes, map[string]attr.Value{
		"username": types.StringValue("creator"), "is_sso_user": types.BoolValue(false), "options": types.ObjectNull(optionsType.AttrTypes),
	})
	prior := workspaceResourceModel{ID: types.StringValue("p/w"), Metadata: MetadataModel{
		Name: types.StringValue("w"), Project: types.StringValue("p"), Workspace: types.StringNull(),
		DisplayName: types.StringValue("Nice"), Description: types.StringValue("old"),
		Labels: types.MapNull(types.StringType), Annotations: types.MapNull(types.StringType), CreatedBy: user, ModifiedBy: user,
	}}
	state := tfsdk.State{Schema: sch}
	if d := state.Set(ctx, &prior); d.HasError() {
		t.Fatal(d)
	}
	plan := tfsdk.Plan{Schema: sch, Raw: state.Raw.Copy()}
	md := path.Root("metadata")
	for _, e := range []struct {
		p path.Path
		v attr.Value
	}{
		{md.AtName("description"), types.StringValue("new")},
		{md.AtName("display_name"), types.StringUnknown()},
		{md.AtName("created_by"), types.ObjectUnknown(userType.AttrTypes)},
		{md.AtName("modified_by"), types.ObjectUnknown(userType.AttrTypes)},
	} {
		if d := plan.SetAttribute(ctx, e.p, e.v); d.HasError() {
			t.Fatal(d)
		}
	}
	cfgState := tfsdk.State{Schema: sch, Raw: state.Raw.Copy()}
	_ = cfgState.SetAttribute(ctx, md.AtName("display_name"), types.StringNull())
	req := resource.ModifyPlanRequest{Config: tfsdk.Config{Schema: sch, Raw: cfgState.Raw}, Plan: plan, State: state}
	resp := &resource.ModifyPlanResponse{Plan: plan}
	keepStableOnUpdate(ctx, req, resp)
	if planValue(t, resp, md.AtName("created_by")).IsUnknown() || planValue(t, resp, md.AtName("display_name")).IsUnknown() {
		t.Error("creator and display name must keep their value")
	}
	if got := planValue(t, resp, md.AtName("description")).(types.String).ValueString(); got != "new" {
		t.Errorf("the edited description = %q, want new", got)
	}
	if !planValue(t, resp, md.AtName("modified_by")).IsUnknown() {
		t.Error("modified_by changes on every edit and must stay unknown")
	}
}

// A description, display name, labels and annotations that nobody set stay null in the plan, so a
// day-2 edit does not list them as "known after apply". A labels map with entries stays unknown.
func TestKeepStableOnUpdateKeepsUnsetMetadataNull(t *testing.T) {
	ctx := context.Background()
	req, resp := bmPlanFixture(t, false)
	md := path.Root("metadata")
	nullStr, nullMap := types.StringNull(), types.MapNull(types.StringType)
	for _, name := range []string{"display_name", "description"} {
		if d := req.State.SetAttribute(ctx, md.AtName(name), nullStr); d.HasError() {
			t.Fatal(d)
		}
	}
	for _, name := range []string{"labels", "annotations"} {
		if d := req.State.SetAttribute(ctx, md.AtName(name), nullMap); d.HasError() {
			t.Fatal(d)
		}
		if d := resp.Plan.SetAttribute(ctx, md.AtName(name), types.MapUnknown(types.StringType)); d.HasError() {
			t.Fatal(d)
		}
	}
	// The fixture's configuration sets labels; here the user configured none.
	cfg := tfsdk.State{Schema: req.Config.Schema, Raw: req.Config.Raw.Copy()}
	for _, name := range []string{"labels", "annotations"} {
		if d := cfg.SetAttribute(ctx, md.AtName(name), nullMap); d.HasError() {
			t.Fatal(d)
		}
	}
	req.Config = tfsdk.Config{Schema: cfg.Schema, Raw: cfg.Raw}
	keepStableOnUpdate(ctx, req, resp, testStableStatus...)
	for _, name := range []string{"display_name", "description", "labels", "annotations"} {
		v := planValue(t, resp, md.AtName(name))
		if v.IsUnknown() || !v.IsNull() {
			t.Errorf("metadata.%s = %v, want null", name, v)
		}
	}

	// A configured value is not the platform's to fill: it stays as the user planned it.
	req2, resp2 := bmPlanFixture(t, false)
	if d := req2.State.SetAttribute(ctx, md.AtName("labels"), nullMap); d.HasError() {
		t.Fatal(d)
	}
	keepStableOnUpdate(ctx, req2, resp2, testStableStatus...)
	if !planValue(t, resp2, md.AtName("labels")).IsUnknown() {
		t.Error("labels the user configured must not be pinned to a null state")
	}
}

// The apply side: a metadata value the plan holds as a known null is stored as null, even when the
// platform returned a value, and a value that is only known after apply is left as the platform
// returned it.
func TestKeepPlannedNullMetadataStoresPlannedNulls(t *testing.T) {
	ctx := context.Background()
	req, resp := bmPlanFixture(t, false)
	md := path.Root("metadata")
	// Plan: description pinned to null (a kept null), display_name unknown (known after apply).
	if d := resp.Plan.SetAttribute(ctx, md.AtName("description"), types.StringNull()); d.HasError() {
		t.Fatal(d)
	}
	if d := resp.Plan.SetAttribute(ctx, md.AtName("display_name"), types.StringUnknown()); d.HasError() {
		t.Fatal(d)
	}
	// State after the write holds what the platform returned.
	state := req.State
	if d := state.SetAttribute(ctx, md.AtName("description"), types.StringValue("added by platform")); d.HasError() {
		t.Fatal(d)
	}
	if d := state.SetAttribute(ctx, md.AtName("display_name"), types.StringValue("dn")); d.HasError() {
		t.Fatal(d)
	}
	if d := keepPlannedNullMetadata(ctx, resp.Plan, &state); d.HasError() {
		t.Fatal(d)
	}
	var desc, name types.String
	_ = state.GetAttribute(ctx, md.AtName("description"), &desc)
	_ = state.GetAttribute(ctx, md.AtName("display_name"), &name)
	if !desc.IsNull() {
		t.Errorf("description = %v, want the planned null", desc)
	}
	if name.ValueString() != "dn" {
		t.Errorf("display_name = %v, want the platform's value for an unknown plan", name)
	}
}
