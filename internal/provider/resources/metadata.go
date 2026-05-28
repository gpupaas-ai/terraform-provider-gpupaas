package resources

import (
	"github.com/hashicorp/terraform-plugin-framework/attr"
	dschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	rschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// metadataAttrTypes describes the canonical metadata{} nested object attribute
// types used by every resource and data source.
var metadataAttrTypes = map[string]attr.Type{
	"name":        types.StringType,
	"project":     types.StringType,
	"workspace":   types.StringType,
	"labels":      types.MapType{ElemType: types.StringType},
	"annotations": types.MapType{ElemType: types.StringType},
}

// MetadataModel mirrors the Terraform `metadata` nested object.
type MetadataModel struct {
	Name        types.String `tfsdk:"name"`
	Project     types.String `tfsdk:"project"`
	Workspace   types.String `tfsdk:"workspace"`
	Labels      types.Map    `tfsdk:"labels"`
	Annotations types.Map    `tfsdk:"annotations"`
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
		MarkdownDescription: "Standard metadata block (name, project, workspace, labels, annotations).",
	}
}

// MetadataDataSourceAttribute returns the metadata{} nested attribute for a
// data source schema. All fields are Computed since data sources are read-only.
func MetadataDataSourceAttribute() dschema.SingleNestedAttribute {
	return dschema.SingleNestedAttribute{
		Computed: true,
		Attributes: map[string]dschema.Attribute{
			"name":        dschema.StringAttribute{Computed: true},
			"project":     dschema.StringAttribute{Computed: true},
			"workspace":   dschema.StringAttribute{Computed: true},
			"labels":      dschema.MapAttribute{Computed: true, ElementType: types.StringType},
			"annotations": dschema.MapAttribute{Computed: true, ElementType: types.StringType},
		},
	}
}
