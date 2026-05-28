package resources

import (
	"github.com/hashicorp/terraform-plugin-framework/attr"
	dschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	rschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// userMetaOverrideAttrTypes mirrors apiv1.UserMetaOverrideOptions.
var userMetaOverrideAttrTypes = map[string]attr.Type{
	"type":              types.StringType,
	"restricted_values": types.ListType{ElemType: types.StringType},
}

// userMetaOverrideObjectType is the canonical attr.Type for nested override objects.
var userMetaOverrideObjectType = types.ObjectType{AttrTypes: userMetaOverrideAttrTypes}

// userMetaOptionsAttrTypes mirrors apiv1.UserMetaOptions.
var userMetaOptionsAttrTypes = map[string]attr.Type{
	"description": types.StringType,
	"required":    types.BoolType,
	"override":    userMetaOverrideObjectType,
}

// userMetaOptionsObjectType is the canonical attr.Type for nested options objects.
var userMetaOptionsObjectType = types.ObjectType{AttrTypes: userMetaOptionsAttrTypes}

// userMetaAttrTypes mirrors apiv1.UserMeta.
var userMetaAttrTypes = map[string]attr.Type{
	"username":    types.StringType,
	"is_sso_user": types.BoolType,
	"options":     userMetaOptionsObjectType,
}

// userMetaObjectType is the canonical attr.Type for nested user-meta objects.
var userMetaObjectType = types.ObjectType{AttrTypes: userMetaAttrTypes}

// metadataAttrTypes describes the canonical metadata{} nested object attribute
// types used by every resource and data source.
var metadataAttrTypes = map[string]attr.Type{
	"name":         types.StringType,
	"project":      types.StringType,
	"workspace":    types.StringType,
	"display_name": types.StringType,
	"description":  types.StringType,
	"labels":       types.MapType{ElemType: types.StringType},
	"annotations":  types.MapType{ElemType: types.StringType},
	"created_by":   userMetaObjectType,
	"modified_by":  userMetaObjectType,
}

// MetadataModel mirrors the Terraform `metadata` nested object.
type MetadataModel struct {
	Name        types.String `tfsdk:"name"`
	Project     types.String `tfsdk:"project"`
	Workspace   types.String `tfsdk:"workspace"`
	DisplayName types.String `tfsdk:"display_name"`
	Description types.String `tfsdk:"description"`
	Labels      types.Map    `tfsdk:"labels"`
	Annotations types.Map    `tfsdk:"annotations"`
	CreatedBy   types.Object `tfsdk:"created_by"`
	ModifiedBy  types.Object `tfsdk:"modified_by"`
}

// userMetaResourceAttribute returns a fully-Computed UserMeta block for use
// inside the resource metadata schema. createdBy / modifiedBy are populated by
// the backend on reads and must never be writeable.
func userMetaResourceAttribute(description string) rschema.SingleNestedAttribute {
	return rschema.SingleNestedAttribute{
		Computed:            true,
		MarkdownDescription: description,
		Attributes: map[string]rschema.Attribute{
			"username":    rschema.StringAttribute{Computed: true},
			"is_sso_user": rschema.BoolAttribute{Computed: true},
			"options": rschema.SingleNestedAttribute{
				Computed: true,
				Attributes: map[string]rschema.Attribute{
					"description": rschema.StringAttribute{Computed: true},
					"required":    rschema.BoolAttribute{Computed: true},
					"override": rschema.SingleNestedAttribute{
						Computed: true,
						Attributes: map[string]rschema.Attribute{
							"type":              rschema.StringAttribute{Computed: true},
							"restricted_values": rschema.ListAttribute{Computed: true, ElementType: types.StringType},
						},
					},
				},
			},
		},
	}
}

func userMetaDataSourceAttribute(description string) dschema.SingleNestedAttribute {
	return dschema.SingleNestedAttribute{
		Computed:            true,
		MarkdownDescription: description,
		Attributes: map[string]dschema.Attribute{
			"username":    dschema.StringAttribute{Computed: true},
			"is_sso_user": dschema.BoolAttribute{Computed: true},
			"options": dschema.SingleNestedAttribute{
				Computed: true,
				Attributes: map[string]dschema.Attribute{
					"description": dschema.StringAttribute{Computed: true},
					"required":    dschema.BoolAttribute{Computed: true},
					"override": dschema.SingleNestedAttribute{
						Computed: true,
						Attributes: map[string]dschema.Attribute{
							"type":              dschema.StringAttribute{Computed: true},
							"restricted_values": dschema.ListAttribute{Computed: true, ElementType: types.StringType},
						},
					},
				},
			},
		},
	}
}

// MetadataResourceAttribute returns the metadata{} nested attribute for a
// resource schema. `includeWorkspace` controls whether the optional
// `workspace` field is exposed (workspace-scoped resources opt in).
func MetadataResourceAttribute(includeWorkspace bool, requireProject bool) rschema.SingleNestedAttribute {
	attrs := map[string]rschema.Attribute{
		"name": rschema.StringAttribute{
			Required:            true,
			MarkdownDescription: "Name of the resource. Combined with project/workspace to form the unique key.",
		},
		"display_name": rschema.StringAttribute{
			Optional:            true,
			Computed:            true,
			MarkdownDescription: "Optional human-friendly name. Maps to backend `metadata.displayName`.",
		},
		"description": rschema.StringAttribute{
			Optional:            true,
			Computed:            true,
			MarkdownDescription: "Optional free-form description. Maps to backend `metadata.description`.",
		},
		"labels": rschema.MapAttribute{
			Optional:            true,
			Computed:            true,
			ElementType:         types.StringType,
			MarkdownDescription: "Key/value labels propagated to the backend `metadata.labels` field.",
		},
		"annotations": rschema.MapAttribute{
			Optional:            true,
			Computed:            true,
			ElementType:         types.StringType,
			MarkdownDescription: "Key/value annotations propagated to the backend `metadata.annotations` field.",
		},
		"created_by":  userMetaResourceAttribute("Backend-observed user that created the resource. Populated on reads; ignored on writes."),
		"modified_by": userMetaResourceAttribute("Backend-observed user that last modified the resource. Populated on reads; ignored on writes."),
	}
	attrs["project"] = rschema.StringAttribute{
		Optional:            !requireProject,
		Required:            requireProject,
		MarkdownDescription: "Project name that owns this resource. Maps to backend `metadata.project`.",
	}
	if includeWorkspace {
		attrs["workspace"] = rschema.StringAttribute{
			Optional:            true,
			MarkdownDescription: "Workspace name that owns this resource. Maps to backend `metadata.workspace`.",
		}
	} else {
		attrs["workspace"] = rschema.StringAttribute{
			Computed:            true,
			MarkdownDescription: "Reserved. Always empty for project-scoped resources.",
		}
	}

	return rschema.SingleNestedAttribute{
		Required:            true,
		Attributes:          attrs,
		MarkdownDescription: "Standard metadata block (name, project, workspace, display_name, description, labels, annotations, created_by, modified_by).",
	}
}

// MetadataDataSourceAttribute returns the metadata{} nested attribute for a
// data source schema. All fields are Computed since data sources are read-only.
func MetadataDataSourceAttribute() dschema.SingleNestedAttribute {
	return dschema.SingleNestedAttribute{
		Computed: true,
		Attributes: map[string]dschema.Attribute{
			"name":         dschema.StringAttribute{Computed: true},
			"project":      dschema.StringAttribute{Computed: true},
			"workspace":    dschema.StringAttribute{Computed: true},
			"display_name": dschema.StringAttribute{Computed: true},
			"description":  dschema.StringAttribute{Computed: true},
			"labels":       dschema.MapAttribute{Computed: true, ElementType: types.StringType},
			"annotations":  dschema.MapAttribute{Computed: true, ElementType: types.StringType},
			"created_by":   userMetaDataSourceAttribute("Backend-observed user that created the resource."),
			"modified_by":  userMetaDataSourceAttribute("Backend-observed user that last modified the resource."),
		},
	}
}
