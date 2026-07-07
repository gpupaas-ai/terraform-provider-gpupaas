package resources

import (
	"context"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	rschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// ---- ImmutableError mode ----------------------------------------------------

func TestImmutableErrorModeString(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mod := immutableStringForMode(ImmutableError)

	cases := []struct {
		name    string
		state   types.String
		plan    types.String
		wantErr bool
	}{
		{"create-null-state", types.StringNull(), types.StringValue("4"), false},
		{"unchanged", types.StringValue("4"), types.StringValue("4"), false},
		{"changed", types.StringValue("4"), types.StringValue("8"), true},
		{"unconfigured-null-plan", types.StringValue("4"), types.StringNull(), false},
		{"unknown-plan", types.StringValue("4"), types.StringUnknown(), false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := planmodifier.StringRequest{
				Path:       path.Root("spec").AtName("cpu_count"),
				StateValue: tc.state,
				PlanValue:  tc.plan,
			}
			resp := &planmodifier.StringResponse{PlanValue: tc.plan}
			mod.PlanModifyString(ctx, req, resp)
			if resp.Diagnostics.HasError() != tc.wantErr {
				t.Fatalf("HasError = %v want %v (diags: %v)", resp.Diagnostics.HasError(), tc.wantErr, resp.Diagnostics)
			}
			if tc.wantErr {
				summary := resp.Diagnostics.Errors()[0].Summary()
				if !strings.Contains(summary, "cannot be modified on an existing resource") {
					t.Fatalf("unexpected summary %q", summary)
				}
				detail := resp.Diagnostics.Errors()[0].Detail()
				if !strings.Contains(detail, "guest_password, security_group, and sharing") {
					t.Fatalf("unexpected detail %q", detail)
				}
			}
		})
	}
}

func TestImmutableErrorModeBoolInt64(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	bresp := &planmodifier.BoolResponse{PlanValue: types.BoolValue(false)}
	immutableBoolForMode(ImmutableError).PlanModifyBool(ctx, planmodifier.BoolRequest{
		Path:       path.Root("spec").AtName("assign_public_ip"),
		StateValue: types.BoolValue(true),
		PlanValue:  types.BoolValue(false),
	}, bresp)
	if !bresp.Diagnostics.HasError() {
		t.Fatal("bool change must error in ImmutableError mode")
	}

	iresp := &planmodifier.Int64Response{PlanValue: types.Int64Value(200)}
	immutableInt64ForMode(ImmutableError).PlanModifyInt64(ctx, planmodifier.Int64Request{
		Path:       path.Root("spec").AtName("boot_disk_size"),
		StateValue: types.Int64Value(100),
		PlanValue:  types.Int64Value(200),
	}, iresp)
	if !iresp.Diagnostics.HasError() {
		t.Fatal("int64 change must error in ImmutableError mode")
	}
}

func TestImmutableErrorModeSetOrderInsensitive(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	a, _ := types.SetValueFrom(ctx, types.StringType, []string{"1.1.1.1", "8.8.8.8"})
	b, _ := types.SetValueFrom(ctx, types.StringType, []string{"8.8.8.8", "1.1.1.1"})
	c, _ := types.SetValueFrom(ctx, types.StringType, []string{"9.9.9.9"})

	// Same elements, different order: sets are semantically equal — no error.
	resp := &planmodifier.SetResponse{PlanValue: b}
	immutableSetForMode(ImmutableError).PlanModifySet(ctx, planmodifier.SetRequest{
		Path: path.Root("spec").AtName("dns_servers"), StateValue: a, PlanValue: b,
	}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("permuted set must not trip the immutable guard: %v", resp.Diagnostics)
	}

	resp = &planmodifier.SetResponse{PlanValue: c}
	immutableSetForMode(ImmutableError).PlanModifySet(ctx, planmodifier.SetRequest{
		Path: path.Root("spec").AtName("dns_servers"), StateValue: a, PlanValue: c,
	}, resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("real set change must error in ImmutableError mode")
	}
}

func TestImmutableErrorModeObject(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	attrTypes := map[string]attr.Type{
		"name":           types.StringType,
		"system_catalog": types.BoolType,
	}
	mk := func(name string) types.Object {
		o, _ := types.ObjectValue(attrTypes, map[string]attr.Value{
			"name":           types.StringValue(name),
			"system_catalog": types.BoolValue(true),
		})
		return o
	}

	resp := &planmodifier.ObjectResponse{PlanValue: mk("A100")}
	immutableObjectForMode(ImmutableError).PlanModifyObject(ctx, planmodifier.ObjectRequest{
		Path: path.Root("spec").AtName("virtual_machine"), StateValue: mk("A100"), PlanValue: mk("A100"),
	}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("unchanged object must not error: %v", resp.Diagnostics)
	}

	resp = &planmodifier.ObjectResponse{PlanValue: mk("H200")}
	immutableObjectForMode(ImmutableError).PlanModifyObject(ctx, planmodifier.ObjectRequest{
		Path: path.Root("spec").AtName("virtual_machine"), StateValue: mk("A100"), PlanValue: mk("H200"),
	}, resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("object change must error in ImmutableError mode")
	}
}

// ---- ImmutableReplace mode ---------------------------------------------------

// replaceTestHarness builds the tfsdk State/Plan a stock RequiresReplace
// modifier needs to distinguish create/destroy from update.
func replaceTestHarness(t *testing.T, stateVal, planVal *string) (planmodifier.StringRequest, *planmodifier.StringResponse) {
	t.Helper()
	ctx := context.Background()
	schema := rschema.Schema{Attributes: map[string]rschema.Attribute{
		"cpu_count": rschema.StringAttribute{Optional: true},
	}}
	objType := schema.Type().TerraformType(ctx).(tftypes.Object)

	raw := func(v *string) tftypes.Value {
		if v == nil {
			return tftypes.NewValue(objType, nil)
		}
		return tftypes.NewValue(objType, map[string]tftypes.Value{
			"cpu_count": tftypes.NewValue(tftypes.String, *v),
		})
	}
	tfString := func(v *string) types.String {
		if v == nil {
			return types.StringNull()
		}
		return types.StringValue(*v)
	}

	req := planmodifier.StringRequest{
		Path:       path.Root("cpu_count"),
		State:      tfsdk.State{Schema: schema, Raw: raw(stateVal)},
		Plan:       tfsdk.Plan{Schema: schema, Raw: raw(planVal)},
		StateValue: tfString(stateVal),
		PlanValue:  tfString(planVal),
	}
	return req, &planmodifier.StringResponse{PlanValue: req.PlanValue}
}

func strPtr(s string) *string { return &s }

func TestImmutableReplaceModeString(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mod := immutableStringForMode(ImmutableReplace)

	// Changed value on an existing resource: flags replacement, no error.
	req, resp := replaceTestHarness(t, strPtr("4"), strPtr("8"))
	mod.PlanModifyString(ctx, req, resp)
	if !resp.RequiresReplace {
		t.Fatal("changed value must set RequiresReplace in ImmutableReplace mode")
	}
	if resp.Diagnostics.HasError() {
		t.Fatalf("ImmutableReplace mode must not add error diagnostics: %v", resp.Diagnostics)
	}

	// Unchanged value: no replacement.
	req, resp = replaceTestHarness(t, strPtr("4"), strPtr("4"))
	mod.PlanModifyString(ctx, req, resp)
	if resp.RequiresReplace {
		t.Fatal("unchanged value must not require replacement")
	}

	// Resource creation (null state): no replacement.
	req, resp = replaceTestHarness(t, nil, strPtr("4"))
	mod.PlanModifyString(ctx, req, resp)
	if resp.RequiresReplace {
		t.Fatal("creation must not require replacement")
	}
}

// ---- Wiring ------------------------------------------------------------------

func TestImmutableMetadataAttributeWiresModifiers(t *testing.T) {
	t.Parallel()
	meta := immutableMetadataAttribute(true, true)
	for _, key := range []string{"name", "project", "workspace"} {
		a, ok := meta.Attributes[key].(rschema.StringAttribute)
		if !ok {
			t.Fatalf("metadata.%s is not a StringAttribute", key)
		}
		if len(a.PlanModifiers) == 0 {
			t.Fatalf("metadata.%s must carry the immutable plan modifier", key)
		}
	}
	// display_name stays freely mutable.
	dn := meta.Attributes["display_name"].(rschema.StringAttribute)
	if len(dn.PlanModifiers) != 0 {
		t.Fatal("metadata.display_name must not be immutable")
	}
}
