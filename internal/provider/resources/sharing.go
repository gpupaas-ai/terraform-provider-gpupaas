package resources

import (
	"context"
	"sort"

	apiv1 "github.com/gpupaas-ai/gpupaas-go/apis/v1alpha1"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	rschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// SharingModel mirrors the Terraform `sharing` block used by dev resources.
// Workspaces and projects are modeled as sets: the backend does not guarantee
// membership order, so two configs listing the same members must plan clean
// (FR-4).
type SharingModel struct {
	ShareMode  types.String `tfsdk:"share_mode"`
	Workspaces types.Set    `tfsdk:"workspaces"`
	Projects   types.Set    `tfsdk:"projects"`
}

// projectSharingModel mirrors a project entry inside `sharing.projects`.
type projectSharingModel struct {
	Name       types.String `tfsdk:"name"`
	Workspaces types.Set    `tfsdk:"workspaces"`
}

var projectSharingObjectType = types.ObjectType{
	AttrTypes: map[string]attr.Type{
		"name":       types.StringType,
		"workspaces": types.SetType{ElemType: types.StringType},
	},
}

// SharingResourceAttribute is the schema block describing dev-resource sharing.
func SharingResourceAttribute() rschema.SingleNestedAttribute {
	return rschema.SingleNestedAttribute{
		Optional:            true,
		MarkdownDescription: "Resource sharing across workspaces or projects. Membership order is irrelevant (set semantics).",
		Attributes: map[string]rschema.Attribute{
			"share_mode": rschema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Sharing mode (e.g. `ALL_WORKSPACES`, `SPECIFIC_WORKSPACES`, `SPECIFIC_PROJECTS`).",
			},
			"workspaces": rschema.SetAttribute{
				Optional:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "Workspaces shared with when `share_mode = SPECIFIC_WORKSPACES`.",
			},
			"projects": rschema.SetNestedAttribute{
				Optional:            true,
				MarkdownDescription: "Project sharing entries when `share_mode = SPECIFIC_PROJECTS`.",
				NestedObject: rschema.NestedAttributeObject{
					Attributes: map[string]rschema.Attribute{
						"name": rschema.StringAttribute{Required: true},
						"workspaces": rschema.SetAttribute{
							Optional:    true,
							ElementType: types.StringType,
						},
					},
				},
			},
		},
	}
}

// sharingProjectsFromTF flattens the projects set into (name, workspaces) pairs
// sorted by name so wire payloads are deterministic.
func sharingProjectsFromTF(ctx context.Context, projects types.Set, diags *diag.Diagnostics) []projectSharingModel {
	if projects.IsNull() || projects.IsUnknown() {
		return nil
	}
	var out []projectSharingModel
	diags.Append(projects.ElementsAs(ctx, &out, false)...)
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name.ValueString() < out[j].Name.ValueString()
	})
	return out
}

// sharingToSDK converts the Terraform sharing model into the SDK value.
// Returns nil when the model is null/unknown.
func sharingToSDK(ctx context.Context, s *SharingModel, diags *diag.Diagnostics) *apiv1.DevSharingSpec {
	if s == nil {
		return nil
	}
	if s.ShareMode.IsNull() && s.Workspaces.IsNull() && s.Projects.IsNull() {
		return nil
	}
	out := &apiv1.DevSharingSpec{ShareMode: stringOr(s.ShareMode, "")}
	out.Workspaces = sortedStringSliceFromTFSet(ctx, s.Workspaces, diags)
	for _, p := range sharingProjectsFromTF(ctx, s.Projects, diags) {
		out.Projects = append(out.Projects, apiv1.DevProjectSharingSpec{
			Name:       stringOr(p.Name, ""),
			Workspaces: sortedStringSliceFromTFSet(ctx, p.Workspaces, diags),
		})
	}
	return out
}

// vmSharingToSDK converts the Terraform sharing model into the VM SDK value.
func vmSharingToSDK(ctx context.Context, s *SharingModel, diags *diag.Diagnostics) *apiv1.VirtualMachineSharingSpec {
	if s == nil {
		return nil
	}
	if s.ShareMode.IsNull() && s.Workspaces.IsNull() && s.Projects.IsNull() {
		return nil
	}
	out := &apiv1.VirtualMachineSharingSpec{ShareMode: stringOr(s.ShareMode, "")}
	out.Workspaces = sortedStringSliceFromTFSet(ctx, s.Workspaces, diags)
	for _, p := range sharingProjectsFromTF(ctx, s.Projects, diags) {
		out.Projects = append(out.Projects, apiv1.VirtualMachineProjectSharingSpec{
			Name:       stringOr(p.Name, ""),
			Workspaces: sortedStringSliceFromTFSet(ctx, p.Workspaces, diags),
		})
	}
	return out
}

// sharingProjectsSetFromSDK canonicalizes (sort by project name, sorted
// workspaces) and converts project sharing entries into a types.Set.
func sharingProjectsSetFromSDK(names []string, workspaces [][]string, diags *diag.Diagnostics) types.Set {
	if len(names) == 0 {
		return types.SetNull(projectSharingObjectType)
	}
	idx := make([]int, len(names))
	for i := range idx {
		idx[i] = i
	}
	sort.Slice(idx, func(a, b int) bool { return names[idx[a]] < names[idx[b]] })

	objs := make([]attr.Value, 0, len(names))
	for _, i := range idx {
		o, d := types.ObjectValue(projectSharingObjectType.AttrTypes, map[string]attr.Value{
			"name":       types.StringValue(names[i]),
			"workspaces": setFromStringSlice(workspaces[i]),
		})
		diags.Append(d...)
		objs = append(objs, o)
	}
	set, d := types.SetValue(projectSharingObjectType, objs)
	diags.Append(d...)
	return set
}

// vmSharingFromSDK converts a VirtualMachineSharingSpec to the Terraform model.
func vmSharingFromSDK(_ context.Context, s *apiv1.VirtualMachineSharingSpec, diags *diag.Diagnostics) *SharingModel {
	if s == nil {
		return nil
	}
	names := make([]string, len(s.Projects))
	workspaces := make([][]string, len(s.Projects))
	for i, p := range s.Projects {
		names[i] = p.Name
		workspaces[i] = p.Workspaces
	}
	return &SharingModel{
		ShareMode:  nullableString(s.ShareMode),
		Workspaces: setFromStringSlice(s.Workspaces),
		Projects:   sharingProjectsSetFromSDK(names, workspaces, diags),
	}
}

// sharingFromSDK converts an SDK DevSharingSpec into the Terraform model.
// Returns a nil pointer when the SDK value is nil to keep state diffs clean.
func sharingFromSDK(_ context.Context, s *apiv1.DevSharingSpec, diags *diag.Diagnostics) *SharingModel {
	if s == nil {
		return nil
	}
	names := make([]string, len(s.Projects))
	workspaces := make([][]string, len(s.Projects))
	for i, p := range s.Projects {
		names[i] = p.Name
		workspaces[i] = p.Workspaces
	}
	return &SharingModel{
		ShareMode:  nullableString(s.ShareMode),
		Workspaces: setFromStringSlice(s.Workspaces),
		Projects:   sharingProjectsSetFromSDK(names, workspaces, diags),
	}
}
