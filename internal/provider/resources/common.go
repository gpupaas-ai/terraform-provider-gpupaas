// Package resources contains the Terraform Plugin Framework resource and
// data source implementations for the GPU PaaS provider.
package resources

import (
	"context"
	"fmt"
	"strings"

	gpupaas "github.com/gpupaas-ai/gpupaas-go"
	apiv1 "github.com/gpupaas-ai/gpupaas-go/apis/v1alpha1"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/gpupaas-ai/terraform-provider-gpupaas/internal/client"
)

// providerData extracts the shared *client.ProviderData from a Configure
// request's ProviderData. Returns nil on first-call when the provider is not
// yet configured.
func providerData(providerDataAny any, diags *diag.Diagnostics) *client.ProviderData {
	if providerDataAny == nil {
		return nil
	}
	pd, ok := providerDataAny.(*client.ProviderData)
	if !ok {
		diags.AddError(
			"Unexpected provider data type",
			fmt.Sprintf("Expected *client.ProviderData, got %T. This is a bug in the provider.", providerDataAny),
		)
		return nil
	}
	return pd
}

// handleAPIError maps an SDK error onto Terraform diagnostics. NotFound is
// routed to the optional notFound callback so Read implementations can drop
// the resource from state.
func handleAPIError(diags *diag.Diagnostics, err error, op string, notFound func()) {
	if err == nil {
		return
	}
	switch {
	case gpupaas.IsNotFound(err):
		if notFound != nil {
			notFound()
		} else {
			diags.AddError(op+" failed: not found", err.Error())
		}
	case gpupaas.IsUnauthorized(err):
		diags.AddError(op+" failed: unauthorized", err.Error())
	case gpupaas.IsForbidden(err):
		diags.AddError(op+" failed: forbidden", err.Error())
	case gpupaas.IsConflict(err):
		diags.AddError(op+" failed: conflict", err.Error())
	case gpupaas.IsServerError(err):
		diags.AddError(op+" failed: server error", err.Error())
	default:
		diags.AddError(op+" failed", err.Error())
	}
}

// ---- Import ID parsers ----------------------------------------------------

// ParseClusterScopedImportID returns the resource name from a single-segment ID.
func ParseClusterScopedImportID(id string) (name string, err error) {
	parts := strings.Split(id, "/")
	if len(parts) != 1 || parts[0] == "" {
		return "", fmt.Errorf("expected import ID of the form <name>, got %q", id)
	}
	return parts[0], nil
}

// ParseProjectScopedImportID returns (project, name) from a two-segment ID.
func ParseProjectScopedImportID(id string) (project, name string, err error) {
	parts := strings.Split(id, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("expected import ID of the form <project>/<name>, got %q", id)
	}
	return parts[0], parts[1], nil
}

// ParseWorkspaceScopedImportID returns (project, workspace, name) from a
// three-segment ID.
func ParseWorkspaceScopedImportID(id string) (project, workspace, name string, err error) {
	parts := strings.Split(id, "/")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return "", "", "", fmt.Errorf("expected import ID of the form <project>/<workspace>/<name>, got %q", id)
	}
	return parts[0], parts[1], parts[2], nil
}

// ParseFlexibleScopedImportID accepts either a 2- or 3-segment ID, used by
// resources that can live at either project or workspace scope (e.g.
// VirtualMachine). Returns workspace="" when only 2 segments are provided.
func ParseFlexibleScopedImportID(id string) (project, workspace, name string, err error) {
	parts := strings.Split(id, "/")
	switch len(parts) {
	case 2:
		if parts[0] == "" || parts[1] == "" {
			return "", "", "", fmt.Errorf("expected import ID of the form <project>/<name> or <project>/<workspace>/<name>, got %q", id)
		}
		return parts[0], "", parts[1], nil
	case 3:
		if parts[0] == "" || parts[1] == "" || parts[2] == "" {
			return "", "", "", fmt.Errorf("expected import ID of the form <project>/<name> or <project>/<workspace>/<name>, got %q", id)
		}
		return parts[0], parts[1], parts[2], nil
	default:
		return "", "", "", fmt.Errorf("expected import ID of the form <project>/<name> or <project>/<workspace>/<name>, got %q", id)
	}
}

// ---- Type helpers --------------------------------------------------------

// nullableString returns types.StringValue(s) when s != "", else StringNull.
func nullableString(s string) types.String {
	if s == "" {
		return types.StringNull()
	}
	return types.StringValue(s)
}

// stringOr returns the inner value of v or fallback when null/unknown.
func stringOr(v types.String, fallback string) string {
	if v.IsNull() || v.IsUnknown() {
		return fallback
	}
	return v.ValueString()
}

// firstNonEmpty returns the first non-empty string from values.
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// ---- Metadata conversion --------------------------------------------------

// metadataToSDK converts a Terraform MetadataModel to an apiv1.ObjectMeta.
// Labels/annotations are copied best-effort (ignoring null/unknown entries).
// CreatedBy / ModifiedBy are intentionally omitted — they are backend-observed
// and stripped on writes by the SDK as well; setting them here would be
// silently ignored.
func metadataToSDK(m MetadataModel) apiv1.ObjectMeta {
	out := apiv1.ObjectMeta{
		Name:        stringOr(m.Name, ""),
		Project:     stringOr(m.Project, ""),
		Workspace:   stringOr(m.Workspace, ""),
		DisplayName: stringOr(m.DisplayName, ""),
		Description: stringOr(m.Description, ""),
	}
	if !m.Labels.IsNull() && !m.Labels.IsUnknown() {
		out.Labels = elementsToStringMap(m.Labels)
	}
	if !m.Annotations.IsNull() && !m.Annotations.IsUnknown() {
		out.Annotations = elementsToStringMap(m.Annotations)
	}
	return out
}

// metadataFromSDK converts an apiv1.ObjectMeta into a Terraform MetadataModel.
func metadataFromSDK(m apiv1.ObjectMeta) MetadataModel {
	return MetadataModel{
		Name:        types.StringValue(m.Name),
		Project:     nullableString(m.Project),
		Workspace:   nullableString(m.Workspace),
		DisplayName: nullableString(m.DisplayName),
		Description: nullableString(m.Description),
		Labels:      mapFromStringMap(m.Labels),
		Annotations: mapFromStringMap(m.Annotations),
		CreatedBy:   userMetaFromSDK(m.CreatedBy),
		ModifiedBy:  userMetaFromSDK(m.ModifiedBy),
	}
}

// userMetaFromSDK converts an apiv1.UserMeta (always nilable) to a Terraform
// types.Object value. Returns ObjectNull when the SDK supplies no value so
// that Terraform diffs stay quiet for resources without audit metadata.
func userMetaFromSDK(u *apiv1.UserMeta) types.Object {
	if u == nil {
		return types.ObjectNull(userMetaAttrTypes)
	}
	obj, _ := types.ObjectValue(userMetaAttrTypes, map[string]attr.Value{
		"username":    nullableString(u.Username),
		"is_sso_user": types.BoolValue(u.IsSSOUser),
		"options":     userMetaOptionsFromSDK(u.Options),
	})
	return obj
}

func userMetaOptionsFromSDK(o *apiv1.UserMetaOptions) types.Object {
	if o == nil {
		return types.ObjectNull(userMetaOptionsAttrTypes)
	}
	obj, _ := types.ObjectValue(userMetaOptionsAttrTypes, map[string]attr.Value{
		"description": nullableString(o.Description),
		"required":    types.BoolValue(o.Required),
		"override":    userMetaOverrideFromSDK(o.Override),
	})
	return obj
}

func userMetaOverrideFromSDK(o *apiv1.UserMetaOverrideOptions) types.Object {
	if o == nil {
		return types.ObjectNull(userMetaOverrideAttrTypes)
	}
	obj, _ := types.ObjectValue(userMetaOverrideAttrTypes, map[string]attr.Value{
		"type":              nullableString(o.Type),
		"restricted_values": listFromStringSlice(o.RestrictedValues),
	})
	return obj
}

func elementsToStringMap(m types.Map) map[string]string {
	if m.IsNull() || m.IsUnknown() {
		return nil
	}
	result := map[string]string{}
	for k, v := range m.Elements() {
		sv, ok := v.(types.String)
		if !ok || sv.IsNull() || sv.IsUnknown() {
			continue
		}
		result[k] = sv.ValueString()
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func mapFromStringMap(in map[string]string) types.Map {
	if in == nil {
		return types.MapNull(types.StringType)
	}
	m, _ := types.MapValueFrom(context.Background(), types.StringType, in)
	return m
}

// stringSliceFromTF converts a Terraform list of strings to []string.
func stringSliceFromTF(ctx context.Context, l types.List, diags *diag.Diagnostics) []string {
	if l.IsNull() || l.IsUnknown() {
		return nil
	}
	out := []string{}
	diags.Append(l.ElementsAs(ctx, &out, false)...)
	if len(out) == 0 {
		return nil
	}
	return out
}

// listFromStringSlice converts a Go []string to a Terraform list value. Returns
// a null list when in is empty/nil to keep diffs stable.
func listFromStringSlice(in []string) types.List {
	if len(in) == 0 {
		return types.ListNull(types.StringType)
	}
	l, _ := types.ListValueFrom(context.Background(), types.StringType, in)
	return l
}

// decodePlan decodes a planned value into target, replacing computed values
// that are still unknown (status, metadata.created_by, ...) with empty ones:
// null leaves, and objects whose attributes are all null. The models use
// plain structs for nested attributes, which cannot hold unknown values, and the
// resources always rewrite those computed fields into state after the API call.
func decodePlan(ctx context.Context, plan tfsdk.Plan, target any) diag.Diagnostics {
	raw, err := tftypes.Transform(plan.Raw, func(_ *tftypes.AttributePath, v tftypes.Value) (tftypes.Value, error) {
		if !v.IsKnown() {
			return emptyValue(v.Type()), nil
		}
		return v, nil
	})
	if err != nil {
		var diags diag.Diagnostics
		diags.AddError("Failed to read planned values", err.Error())
		return diags
	}
	return tfsdk.Plan{Raw: raw, Schema: plan.Schema}.Get(ctx, target)
}

// decodePlanKeepingUnknowns is decodePlan for validation: a value that is only known after apply stays
// unknown where the model can hold it (a string, number, bool or map), so a check can skip it instead of
// judging a placeholder. Unknown objects become empty ones and unknown lists become null, as in decodePlan.
func decodePlanKeepingUnknowns(ctx context.Context, plan tfsdk.Plan, target any) diag.Diagnostics {
	raw, err := tftypes.Transform(plan.Raw, func(_ *tftypes.AttributePath, v tftypes.Value) (tftypes.Value, error) {
		if v.IsKnown() {
			return v, nil
		}
		switch v.Type().(type) {
		case tftypes.Object:
			return emptyValue(v.Type()), nil
		case tftypes.List, tftypes.Set, tftypes.Tuple:
			return tftypes.NewValue(v.Type(), nil), nil
		}
		return v, nil
	})
	if err != nil {
		var diags diag.Diagnostics
		diags.AddError("Failed to read planned values", err.Error())
		return diags
	}
	return tfsdk.Plan{Raw: raw, Schema: plan.Schema}.Get(ctx, target)
}

// emptyValue returns a known value of type t with every leaf null. Objects are
// kept non-null so they decode into plain struct fields.
func emptyValue(t tftypes.Type) tftypes.Value {
	obj, ok := t.(tftypes.Object)
	if !ok {
		return tftypes.NewValue(t, nil)
	}
	attrs := make(map[string]tftypes.Value, len(obj.AttributeTypes))
	for name, at := range obj.AttributeTypes {
		attrs[name] = emptyValue(at)
	}
	return tftypes.NewValue(t, attrs)
}

// decodeConfig decodes the raw configuration for validation. Like decodeState it turns a null nested object into one
// whose attributes are all null, and like decodePlanKeepingUnknowns it keeps an unknown string, number, bool or map
// unknown. A wholly unknown object (a spec, timeouts or sharing block built from another resource) becomes an empty
// object and an unknown list becomes null, because the models use plain structs and slices that cannot hold unknown.
func decodeConfig(ctx context.Context, cfg tfsdk.Config, target any) diag.Diagnostics {
	raw, err := tftypes.Transform(cfg.Raw, func(_ *tftypes.AttributePath, v tftypes.Value) (tftypes.Value, error) {
		switch v.Type().(type) {
		case tftypes.Object:
			if v.IsNull() || !v.IsKnown() {
				return emptyValue(v.Type()), nil
			}
		case tftypes.List, tftypes.Set, tftypes.Tuple:
			if !v.IsKnown() {
				return tftypes.NewValue(v.Type(), nil), nil
			}
		}
		return v, nil
	})
	if err != nil {
		var diags diag.Diagnostics
		diags.AddError("Failed to read the configuration", err.Error())
		return diags
	}
	return tfsdk.Config{Raw: raw, Schema: cfg.Schema}.Get(ctx, target)
}

// planCredentialPending reports whether a login credential under `spec` may still become known during apply:
// the whole spec object is unknown, or one of the named attributes is. decodePlan reads an unknown value as null, and
// reading an attribute below an unknown object returns null (not unknown), so the plan has to be asked at both levels.
func planCredentialPending(ctx context.Context, plan tfsdk.Plan, attrs ...string) bool {
	var spec types.Object
	if plan.GetAttribute(ctx, path.Root("spec"), &spec).HasError() || spec.IsUnknown() {
		return true
	}
	for _, name := range attrs {
		var v types.String
		if plan.GetAttribute(ctx, path.Root("spec").AtName(name), &v).HasError() || v.IsUnknown() {
			return true
		}
	}
	return false
}

// planListPending reports whether the list at the given path is unknown in the plan, or sits below an unknown object.
func planListPending(ctx context.Context, plan tfsdk.Plan, parent, name string) bool {
	var obj types.Object
	if plan.GetAttribute(ctx, path.Root(parent), &obj).HasError() || obj.IsUnknown() {
		return true
	}
	var l types.List
	if plan.GetAttribute(ctx, path.Root(parent).AtName(name), &l).HasError() {
		return true
	}
	return l.IsUnknown()
}

// decodeState decodes prior state into target, treating null nested objects (for
// example everything but the identity right after `terraform import`) as objects
// whose attributes are all null, because the models use plain structs for nested
// attributes and those cannot hold null.
//
// The top-level timeouts block is the exception: its model is a pointer, so a null block decodes to nil and stays
// distinguishable from an explicit `timeouts = {}` (an empty struct). Storing the state back then keeps each as the
// configuration had it, with no per-resource repair after a refresh.
func decodeState(ctx context.Context, st tfsdk.State, target any) diag.Diagnostics {
	timeouts := tftypes.NewAttributePath().WithAttributeName("timeouts")
	raw, err := tftypes.Transform(st.Raw, func(p *tftypes.AttributePath, v tftypes.Value) (tftypes.Value, error) {
		if v.IsNull() && !p.Equal(timeouts) {
			if _, ok := v.Type().(tftypes.Object); ok {
				return emptyValue(v.Type()), nil
			}
		}
		return v, nil
	})
	if err != nil {
		var diags diag.Diagnostics
		diags.AddError("Failed to read prior state", err.Error())
		return diags
	}
	return tfsdk.State{Raw: raw, Schema: st.Schema}.Get(ctx, target)
}
