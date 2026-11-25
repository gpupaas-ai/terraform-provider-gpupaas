package resources

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// keepStableOnUpdate stops the plan of an in-place update from marking values that cannot change
// as "known after apply".
//
// Any change to a resource (a day-2 edit such as the public IP of a machine) makes the framework
// mark every computed attribute without a plan modifier unknown. The plan then shows dozens of
// lines that are not changes: the creator, the description, the instance id. This keeps, at the
// value in the state, the creator, the display name and the description (when the configuration
// leaves the last two unset) and the attributes of status named in stableStatus.
//
// A description, display name, labels or annotations that are null in the state and not configured
// stay null.
//
// It leaves unknown what the platform may change: the rest of status, the modifier, and the
// labels and annotations, which the platform can add to. A plan that replaces the resource is left
// alone (a new resource has no state to keep), and so is a create, and a value that is empty in
// the state, so that a stale empty value cannot make the apply return something the plan did not
// promise.
func keepStableOnUpdate(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse, stableStatus ...string) {
	if req.State.Raw.IsNull() || req.Plan.Raw.IsNull() || len(resp.RequiresReplace) > 0 {
		return
	}

	// keep sets the planned value of p from the state.
	keep := func(p path.Path, onlyIfNotConfigured bool) {
		var planned, prior, configured attr.Value
		if resp.Plan.GetAttribute(ctx, p, &planned).HasError() || !planned.IsUnknown() {
			return
		}
		if req.State.GetAttribute(ctx, p, &prior).HasError() || prior.IsNull() || prior.IsUnknown() {
			return
		}
		if s, ok := prior.(types.String); ok && s.ValueString() == "" {
			return
		}
		if onlyIfNotConfigured {
			if req.Config.GetAttribute(ctx, p, &configured).HasError() || !configured.IsNull() {
				return
			}
		}
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, p, prior)...)
	}

	md := path.Root("metadata")
	keep(md.AtName("display_name"), true)
	keep(md.AtName("description"), true)
	keep(md.AtName("created_by"), false)

	// A metadata value that is null in the state and not configured stays null: nobody has set it,
	// and the apply stores the planned null (metadataFromServer), so it cannot come back as a value
	// the plan did not promise. This is the common case for a description, a display name, labels
	// and annotations. A labels map that has entries stays unknown, because the platform adds its own.
	for _, name := range plannedNullMetadata {
		p := md.AtName(name)
		var planned, prior, configured attr.Value
		if resp.Plan.GetAttribute(ctx, p, &planned).HasError() || !planned.IsUnknown() {
			continue
		}
		if req.State.GetAttribute(ctx, p, &prior).HasError() || !prior.IsNull() {
			continue
		}
		if req.Config.GetAttribute(ctx, p, &configured).HasError() || !configured.IsNull() {
			continue
		}
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, p, prior)...)
	}

	if len(stableStatus) == 0 {
		return
	}
	stable := make(map[string]bool, len(stableStatus))
	for _, name := range stableStatus {
		stable[name] = true
	}
	// status is one computed object, unknown as a whole. Rebuild it from the state, with
	// everything but the stable attributes left unknown.
	var plannedStatus, priorStatus types.Object
	if resp.Plan.GetAttribute(ctx, path.Root("status"), &plannedStatus).HasError() || !plannedStatus.IsUnknown() {
		return
	}
	if req.State.GetAttribute(ctx, path.Root("status"), &priorStatus).HasError() || priorStatus.IsNull() || priorStatus.IsUnknown() {
		return
	}
	types_ := priorStatus.AttributeTypes(ctx)
	attrs := make(map[string]attr.Value, len(priorStatus.Attributes()))
	for name, v := range priorStatus.Attributes() {
		empty := false
		if s, ok := v.(types.String); ok && s.ValueString() == "" {
			empty = true
		}
		if stable[name] && !v.IsNull() && !v.IsUnknown() && !empty {
			attrs[name] = v
			continue
		}
		attrs[name] = unknownOf(types_[name])
	}
	next, diags := types.ObjectValue(types_, attrs)
	resp.Diagnostics.Append(diags...)
	if diags.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("status"), next)...)
}

// unknownOf returns the unknown value of an attribute type.
func unknownOf(t attr.Type) attr.Value {
	switch tt := t.(type) {
	case types.ObjectType:
		return types.ObjectUnknown(tt.AttrTypes)
	case types.ListType:
		return types.ListUnknown(tt.ElemType)
	case types.MapType:
		return types.MapUnknown(tt.ElemType)
	case types.SetType:
		return types.SetUnknown(tt.ElemType)
	}
	switch t {
	case types.BoolType:
		return types.BoolUnknown()
	case types.Int64Type:
		return types.Int64Unknown()
	}
	return types.StringUnknown()
}

// vmStableStatus are the identity attributes of a VM; an in-place update does not change them.
var vmStableStatus = []string{"instance_id", "os_name"}

// clusterStableStatus is the identifier of a cluster in the Kubernetes management plane.
var clusterStableStatus = []string{"cluster_id"}

// plannedNullMetadata are the metadata values keepStableOnUpdate keeps null in a plan.
var plannedNullMetadata = []string{"display_name", "description", "labels", "annotations"}

// keepPlannedNullMetadata is the apply side of keepStableOnUpdate. Call it at the end of Update:
// a metadata value the plan holds as a known null is stored as null, even when the platform has
// meanwhile filled it in, so the apply cannot return a value the plan did not promise (Terraform
// would fail it as an inconsistent result). The next refresh shows what the platform holds.
//
// The planned values must be read from the raw plan: decodePlan turns every unknown into null, so
// a decoded model cannot tell a kept null from a value that is only known after apply.
func keepPlannedNullMetadata(ctx context.Context, plan tfsdk.Plan, state *tfsdk.State) diag.Diagnostics {
	var diags diag.Diagnostics
	for _, name := range plannedNullMetadata {
		p := path.Root("metadata").AtName(name)
		var planned attr.Value
		if plan.GetAttribute(ctx, p, &planned).HasError() || planned.IsUnknown() || !planned.IsNull() {
			continue
		}
		diags.Append(state.SetAttribute(ctx, p, planned)...)
	}
	return diags
}
