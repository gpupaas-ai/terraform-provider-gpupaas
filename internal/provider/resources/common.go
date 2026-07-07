// Package resources contains the Terraform Plugin Framework resource and
// data source implementations for the GPU PaaS provider.
package resources

import (
	"context"
	"fmt"
	"sort"
	"strings"

	gpupaas "github.com/gpupaas-ai/gpupaas-go"
	apiv1 "github.com/gpupaas-ai/gpupaas-go/apis/v1alpha1"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"

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

// sortedStringSliceFromTFSet converts a Terraform set of strings to a sorted
// []string so wire payloads built from set values are deterministic.
func sortedStringSliceFromTFSet(ctx context.Context, s types.Set, diags *diag.Diagnostics) []string {
	if s.IsNull() || s.IsUnknown() {
		return nil
	}
	out := []string{}
	diags.Append(s.ElementsAs(ctx, &out, false)...)
	if len(out) == 0 {
		return nil
	}
	sort.Strings(out)
	return out
}

// setFromStringSlice converts a Go []string to a canonical (sorted) Terraform
// set value. Returns a null set when in is empty/nil to keep diffs stable.
func setFromStringSlice(in []string) types.Set {
	if len(in) == 0 {
		return types.SetNull(types.StringType)
	}
	sorted := append([]string(nil), in...)
	sort.Strings(sorted)
	s, _ := types.SetValueFrom(context.Background(), types.StringType, sorted)
	return s
}

// ---- Imperative action trigger helpers ------------------------------------
//
// Shared by the resources that still model imperative lifecycle actions via a
// `desired_action` trigger field (BaremetalMachine, MKSCluster). VirtualMachine
// has moved to the declarative `power_state` model (plan.md 3.4) and no
// longer uses these.

// normalizeDesiredAction returns the value to persist in state. Null/unset
// flattens to "none" so the next plan stays stable.
func normalizeDesiredAction(in types.String) types.String {
	if in.IsNull() || in.IsUnknown() || in.ValueString() == "" {
		return types.StringValue("none")
	}
	return in
}

// normalizedActionValue returns the lowercase comparable form of a
// desired_action value: "" for null/unknown, otherwise the literal string.
// "none" stays as "none" since it has explicit "do-nothing" semantics.
func normalizedActionValue(in types.String) string {
	if in.IsNull() || in.IsUnknown() {
		return ""
	}
	return in.ValueString()
}
