package resources

import (
	"context"

	apiv1 "github.com/gpupaas-ai/gpupaas-go/apis/v1alpha1"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	rschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// SharingModel mirrors the Terraform `sharing` block used by dev resources.
type SharingModel struct {
	ShareMode  types.String `tfsdk:"share_mode"`
	Workspaces types.List   `tfsdk:"workspaces"`
	Projects   types.List   `tfsdk:"projects"`
}

// projectSharingModel mirrors a project entry inside `sharing.projects`.
type projectSharingModel struct {
	Name       types.String `tfsdk:"name"`
	Workspaces types.List   `tfsdk:"workspaces"`
}

var projectSharingObjectType = types.ObjectType{
	AttrTypes: map[string]attr.Type{
		"name":       types.StringType,
		"workspaces": types.ListType{ElemType: types.StringType},
	},
}

// SharingResourceAttribute is the schema block describing dev-resource sharing.
func SharingResourceAttribute() rschema.SingleNestedAttribute {
	return rschema.SingleNestedAttribute{
		Optional:            true,
		MarkdownDescription: "Resource sharing across workspaces or projects.",
		Attributes: map[string]rschema.Attribute{
			"share_mode": rschema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Sharing mode (e.g. `ALL_WORKSPACES`, `SPECIFIC_WORKSPACES`, `SPECIFIC_PROJECTS`).",
			},
			"workspaces": rschema.ListAttribute{
				Optional:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "Workspaces shared with when `share_mode = SPECIFIC_WORKSPACES`.",
			},
			"projects": rschema.ListNestedAttribute{
				Optional:            true,
				MarkdownDescription: "Project sharing entries when `share_mode = SPECIFIC_PROJECTS`.",
				NestedObject: rschema.NestedAttributeObject{
					Attributes: map[string]rschema.Attribute{
						"name": rschema.StringAttribute{Required: true},
						"workspaces": rschema.ListAttribute{
							Optional:    true,
							ElementType: types.StringType,
						},
					},
				},
			},
		},
	}
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
	out.Workspaces = stringSliceFromTF(ctx, s.Workspaces, diags)

	if !s.Projects.IsNull() && !s.Projects.IsUnknown() {
		var projects []projectSharingModel
		diags.Append(s.Projects.ElementsAs(ctx, &projects, false)...)
		for _, p := range projects {
			out.Projects = append(out.Projects, apiv1.DevProjectSharingSpec{
				Name:       stringOr(p.Name, ""),
				Workspaces: stringSliceFromTF(ctx, p.Workspaces, diags),
			})
		}
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
	out.Workspaces = stringSliceFromTF(ctx, s.Workspaces, diags)
	if !s.Projects.IsNull() && !s.Projects.IsUnknown() {
		var projects []projectSharingModel
		diags.Append(s.Projects.ElementsAs(ctx, &projects, false)...)
		for _, p := range projects {
			out.Projects = append(out.Projects, apiv1.VirtualMachineProjectSharingSpec{
				Name:       stringOr(p.Name, ""),
				Workspaces: stringSliceFromTF(ctx, p.Workspaces, diags),
			})
		}
	}
	return out
}

// vmSharingFromSDK converts a VirtualMachineSharingSpec to the Terraform model.
func vmSharingFromSDK(ctx context.Context, s *apiv1.VirtualMachineSharingSpec, diags *diag.Diagnostics) *SharingModel {
	if s == nil {
		return nil
	}
	model := &SharingModel{
		ShareMode:  nullableString(s.ShareMode),
		Workspaces: listFromStringSlice(s.Workspaces),
	}
	if len(s.Projects) == 0 {
		model.Projects = types.ListNull(projectSharingObjectType)
		return model
	}
	objs := make([]attr.Value, 0, len(s.Projects))
	for _, p := range s.Projects {
		o, d := types.ObjectValue(projectSharingObjectType.AttrTypes, map[string]attr.Value{
			"name":       types.StringValue(p.Name),
			"workspaces": listFromStringSlice(p.Workspaces),
		})
		diags.Append(d...)
		objs = append(objs, o)
	}
	list, d := types.ListValue(projectSharingObjectType, objs)
	diags.Append(d...)
	model.Projects = list
	_ = ctx
	return model
}

// sharingFromSDK converts an SDK DevSharingSpec into the Terraform model.
// Returns a nil pointer when the SDK value is nil to keep state diffs clean.
func sharingFromSDK(ctx context.Context, s *apiv1.DevSharingSpec, diags *diag.Diagnostics) *SharingModel {
	if s == nil {
		return nil
	}
	model := &SharingModel{
		ShareMode:  nullableString(s.ShareMode),
		Workspaces: listFromStringSlice(s.Workspaces),
	}
	if len(s.Projects) == 0 {
		model.Projects = types.ListNull(projectSharingObjectType)
		return model
	}
	objs := make([]attr.Value, 0, len(s.Projects))
	for _, p := range s.Projects {
		o, d := types.ObjectValue(projectSharingObjectType.AttrTypes, map[string]attr.Value{
			"name":       types.StringValue(p.Name),
			"workspaces": listFromStringSlice(p.Workspaces),
		})
		diags.Append(d...)
		objs = append(objs, o)
	}
	list, d := types.ListValue(projectSharingObjectType, objs)
	diags.Append(d...)
	model.Projects = list
	_ = ctx
	return model
}
