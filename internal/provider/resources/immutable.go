package resources

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	rschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/objectplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
)

// immutableModeValue selects how the provider treats Day-2 edits of immutable
// attributes (plan.md D4).
type immutableModeValue int

const (
	// ImmutableError blocks the edit with a hard plan-time error diagnostic
	// (D4 primary: an error can never destroy a VM by accident).
	ImmutableError immutableModeValue = iota
	// ImmutableReplace maps the edit onto the Terraform-idiomatic
	// destroy+recreate via the stock RequiresReplace modifiers (D4 backup).
	ImmutableReplace
)

// immutableMode is the provider-wide switch. Flipping to ImmutableReplace is
// the documented one-line change; the per-mode unit tests cover both.
const immutableMode = ImmutableError

func immutableString() planmodifier.String { return immutableStringForMode(immutableMode) }
func immutableBool() planmodifier.Bool     { return immutableBoolForMode(immutableMode) }
func immutableInt64() planmodifier.Int64   { return immutableInt64ForMode(immutableMode) }
func immutableSet() planmodifier.Set       { return immutableSetForMode(immutableMode) }
func immutableList() planmodifier.List     { return immutableListForMode(immutableMode) }
func immutableObject() planmodifier.Object { return immutableObjectForMode(immutableMode) }

func immutableStringForMode(mode immutableModeValue) planmodifier.String {
	if mode == ImmutableReplace {
		return stringplanmodifier.RequiresReplace()
	}
	return immutableGuard{}
}

func immutableBoolForMode(mode immutableModeValue) planmodifier.Bool {
	if mode == ImmutableReplace {
		return boolplanmodifier.RequiresReplace()
	}
	return immutableGuard{}
}

func immutableInt64ForMode(mode immutableModeValue) planmodifier.Int64 {
	if mode == ImmutableReplace {
		return int64planmodifier.RequiresReplace()
	}
	return immutableGuard{}
}

func immutableSetForMode(mode immutableModeValue) planmodifier.Set {
	if mode == ImmutableReplace {
		return setplanmodifier.RequiresReplace()
	}
	return immutableGuard{}
}

func immutableListForMode(mode immutableModeValue) planmodifier.List {
	if mode == ImmutableReplace {
		return listplanmodifier.RequiresReplace()
	}
	return immutableGuard{}
}

func immutableObjectForMode(mode immutableModeValue) planmodifier.Object {
	if mode == ImmutableReplace {
		return objectplanmodifier.RequiresReplace()
	}
	return immutableGuard{}
}

// immutableGuard is the ImmutableError-mode plan modifier: when the resource
// already exists (state value known and non-null) and the planned value
// differs, it fails the plan with a hard error instead of replacing the
// resource.
type immutableGuard struct{}

func (immutableGuard) Description(context.Context) string {
	return "Attribute is immutable on an existing resource; changing it fails the plan."
}

func (g immutableGuard) MarkdownDescription(ctx context.Context) string {
	return g.Description(ctx)
}

// immutableCheck implements the shared guard logic.
//
// A null plan value is deliberately NOT treated as a change: several optional
// spec attributes are server-populated on Read (e.g. spec.name falls back to
// metadata.name), so "configured nothing" must never hard-fail the plan — the
// upsert simply omits the field.
func immutableCheck(state, plan attr.Value, p path.Path, diags *diag.Diagnostics) {
	if state.IsNull() || state.IsUnknown() {
		return // resource creation, or no prior value to protect
	}
	if plan.IsNull() || plan.IsUnknown() {
		return
	}
	if plan.Equal(state) {
		return
	}
	diags.AddAttributeError(p,
		fmt.Sprintf("%s cannot be modified on an existing resource", p),
		"Day-2 changes are limited to guest_password, security_group, and sharing. "+
			"Destroy and recreate explicitly, or revert the change.")
}

func (immutableGuard) PlanModifyString(_ context.Context, req planmodifier.StringRequest, resp *planmodifier.StringResponse) {
	immutableCheck(req.StateValue, req.PlanValue, req.Path, &resp.Diagnostics)
}

func (immutableGuard) PlanModifyBool(_ context.Context, req planmodifier.BoolRequest, resp *planmodifier.BoolResponse) {
	immutableCheck(req.StateValue, req.PlanValue, req.Path, &resp.Diagnostics)
}

func (immutableGuard) PlanModifyInt64(_ context.Context, req planmodifier.Int64Request, resp *planmodifier.Int64Response) {
	immutableCheck(req.StateValue, req.PlanValue, req.Path, &resp.Diagnostics)
}

func (immutableGuard) PlanModifySet(_ context.Context, req planmodifier.SetRequest, resp *planmodifier.SetResponse) {
	immutableCheck(req.StateValue, req.PlanValue, req.Path, &resp.Diagnostics)
}

func (immutableGuard) PlanModifyList(_ context.Context, req planmodifier.ListRequest, resp *planmodifier.ListResponse) {
	immutableCheck(req.StateValue, req.PlanValue, req.Path, &resp.Diagnostics)
}

func (immutableGuard) PlanModifyObject(_ context.Context, req planmodifier.ObjectRequest, resp *planmodifier.ObjectResponse) {
	immutableCheck(req.StateValue, req.PlanValue, req.Path, &resp.Diagnostics)
}

// immutableMetadataAttribute wraps MetadataResourceAttribute and marks
// name/project/workspace immutable — moving or renaming a first-class object
// is never an in-place update.
func immutableMetadataAttribute(includeWorkspace, requireProject bool) rschema.SingleNestedAttribute {
	meta := MetadataResourceAttribute(includeWorkspace, requireProject)
	for _, key := range []string{"name", "project", "workspace"} {
		a, ok := meta.Attributes[key].(rschema.StringAttribute)
		if !ok {
			continue // project-scoped resources expose workspace as Computed-only
		}
		if a.Computed && !a.Optional && !a.Required {
			continue
		}
		a.PlanModifiers = append(a.PlanModifiers, immutableString())
		meta.Attributes[key] = a
	}
	return meta
}
