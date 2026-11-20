package resources

import (
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// scopedMetadataModel is the metadata block of the resources the platform can only create and delete
// (SSH keys, security groups): where the object lives, and nothing else, so no setting can drift.
// Every attribute forces a replacement.
type scopedMetadataModel struct {
	Name      types.String `tfsdk:"name"`
	Project   types.String `tfsdk:"project"`
	Workspace types.String `tfsdk:"workspace"`
}

func scopedMetadataAttribute(kind string) schema.SingleNestedAttribute {
	replace := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	return schema.SingleNestedAttribute{
		Required:            true,
		MarkdownDescription: "Where the " + kind + " lives. Changing any of these replaces it.",
		Attributes: map[string]schema.Attribute{
			"name":      schema.StringAttribute{Required: true, MarkdownDescription: "Name of the " + kind + ".", PlanModifiers: replace},
			"project":   schema.StringAttribute{Required: true, MarkdownDescription: "Project that owns the " + kind + ".", PlanModifiers: replace},
			"workspace": schema.StringAttribute{Optional: true, MarkdownDescription: "Workspace that owns the " + kind + "; omit for a project-scoped one. It must already exist.", PlanModifiers: replace},
		},
	}
}

// scopedID is `<project>/<workspace>/<name>`, or `<project>/<name>` without a workspace.
func scopedID(project, workspace, name string) string {
	if workspace == "" {
		return project + "/" + name
	}
	return project + "/" + workspace + "/" + name
}

// samePublicKey compares two OpenSSH public keys by type and key material: the comment is not part of
// the key, and a platform that trims or drops it must not look like drift.
func samePublicKey(a, b string) bool {
	fa, fb := strings.Fields(a), strings.Fields(b)
	if len(fa) < 2 || len(fb) < 2 {
		return strings.TrimSpace(a) == strings.TrimSpace(b)
	}
	return fa[0] == fb[0] && fa[1] == fb[1]
}
