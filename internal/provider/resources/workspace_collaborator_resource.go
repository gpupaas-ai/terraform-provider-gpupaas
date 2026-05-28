package resources

import (
	"context"

	gpupaas "github.com/gpupaas-ai/gpupaas-go"
	apiv1 "github.com/gpupaas-ai/gpupaas-go/apis/v1alpha1"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/gpupaas-ai/terraform-provider-gpupaas/internal/client"
)

var (
	_ resource.Resource                = (*workspaceCollaboratorResource)(nil)
	_ resource.ResourceWithImportState = (*workspaceCollaboratorResource)(nil)
	_ resource.ResourceWithConfigure   = (*workspaceCollaboratorResource)(nil)
)

// NewWorkspaceCollaboratorResource constructs the gpupaas_workspace_collaborator resource.
func NewWorkspaceCollaboratorResource() resource.Resource { return &workspaceCollaboratorResource{} }

type workspaceCollaboratorResource struct{ pd *client.ProviderData }

type workspaceCollaboratorModel struct {
	ID         types.String                `tfsdk:"id"`
	APIVersion types.String                `tfsdk:"api_version"`
	Kind       types.String                `tfsdk:"kind"`
	Metadata   MetadataModel               `tfsdk:"metadata"`
	Spec       workspaceCollaboratorSpec   `tfsdk:"spec"`
	Status     workspaceCollaboratorStatus `tfsdk:"status"`
}

type workspaceCollaboratorSpec struct {
	Username  types.String `tfsdk:"username"`
	Email     types.String `tfsdk:"email"`
	FirstName types.String `tfsdk:"first_name"`
	LastName  types.String `tfsdk:"last_name"`
	Role      types.String `tfsdk:"role"`
	UserType  types.String `tfsdk:"user_type"`
	IsSSOUser types.Bool   `tfsdk:"is_sso_user"`
}

type workspaceCollaboratorStatus struct {
	Phase types.String `tfsdk:"phase"`
	Role  types.String `tfsdk:"role"`
}

func (r *workspaceCollaboratorResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_workspace_collaborator"
}

func (r *workspaceCollaboratorResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "GPU PaaS workspace collaborator (`apiVersion: " + apiv1.APIVersion + "`, `kind: WorkspaceCollaborator`). Assigns an existing Rafay user (set `username` or use `metadata.name`) or invites a new user (set `email`). Valid roles: `PAAS_WORKSPACE_COLLABORATOR`, `PAAS_WORKSPACE_COLLABORATOR_READ_ONLY`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"api_version": schema.StringAttribute{Computed: true},
			"kind":        schema.StringAttribute{Computed: true},
			"metadata":    MetadataResourceAttribute(true, true),
			"spec": schema.SingleNestedAttribute{
				Required: true,
				Attributes: map[string]schema.Attribute{
					"username":    schema.StringAttribute{Optional: true, MarkdownDescription: "Existing Rafay username. Optional if set via metadata.name."},
					"email":       schema.StringAttribute{Optional: true, MarkdownDescription: "Email for inviting a new external user."},
					"first_name":  schema.StringAttribute{Optional: true},
					"last_name":   schema.StringAttribute{Optional: true},
					"role":        schema.StringAttribute{Required: true, MarkdownDescription: "One of `PAAS_WORKSPACE_COLLABORATOR`, `PAAS_WORKSPACE_COLLABORATOR_READ_ONLY`."},
					"user_type":   schema.StringAttribute{Optional: true},
					"is_sso_user": schema.BoolAttribute{Optional: true, MarkdownDescription: "Treat the user as an SSO user (controls the `ssoUsers=true` query param)."},
				},
			},
			"status": schema.SingleNestedAttribute{
				Computed: true,
				Attributes: map[string]schema.Attribute{
					"phase": schema.StringAttribute{Computed: true},
					"role":  schema.StringAttribute{Computed: true},
				},
			},
		},
	}
}

func (r *workspaceCollaboratorResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.pd = providerData(req.ProviderData, &resp.Diagnostics)
}

func (r *workspaceCollaboratorResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	if r.pd == nil {
		resp.Diagnostics.AddError("Provider not configured", "Provider data is nil; check provider configuration.")
		return
	}
	var plan workspaceCollaboratorModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	project := plan.Metadata.Project.ValueString()
	workspace := plan.Metadata.Workspace.ValueString()
	out, err := r.pd.Clientset.V1alpha1().Workspaces(project).Collaborators(workspace).Create(ctx, collabModelToSDK(plan), gpupaas.CreateOptions{})
	if err != nil {
		handleAPIError(&resp.Diagnostics, err, "Create workspace collaborator", nil)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, collabSDKToModel(out, project, workspace, plan))...)
}

func (r *workspaceCollaboratorResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	if r.pd == nil {
		return
	}
	var state workspaceCollaboratorModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	project := state.Metadata.Project.ValueString()
	workspace := state.Metadata.Workspace.ValueString()
	out, err := r.pd.Clientset.V1alpha1().Workspaces(project).Collaborators(workspace).Get(ctx, state.Metadata.Name.ValueString(), gpupaas.GetOptions{})
	if err != nil {
		handleAPIError(&resp.Diagnostics, err, "Read workspace collaborator", func() { resp.State.RemoveResource(ctx) })
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, collabSDKToModel(out, project, workspace, state))...)
}

func (r *workspaceCollaboratorResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	// The backend does not expose update; re-assign via create which is
	// idempotent for existing Rafay users.
	if r.pd == nil {
		return
	}
	var plan workspaceCollaboratorModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	project := plan.Metadata.Project.ValueString()
	workspace := plan.Metadata.Workspace.ValueString()
	out, err := r.pd.Clientset.V1alpha1().Workspaces(project).Collaborators(workspace).Create(ctx, collabModelToSDK(plan), gpupaas.CreateOptions{})
	if err != nil {
		handleAPIError(&resp.Diagnostics, err, "Update workspace collaborator", nil)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, collabSDKToModel(out, project, workspace, plan))...)
}

func (r *workspaceCollaboratorResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	if r.pd == nil {
		return
	}
	var state workspaceCollaboratorModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	project := state.Metadata.Project.ValueString()
	workspace := state.Metadata.Workspace.ValueString()
	opts := gpupaas.DeleteOptions{IgnoreNotFound: true}
	if !state.Spec.IsSSOUser.IsNull() && !state.Spec.IsSSOUser.IsUnknown() {
		v := state.Spec.IsSSOUser.ValueBool()
		opts.SSOUser = &v
	}
	err := r.pd.Clientset.V1alpha1().Workspaces(project).Collaborators(workspace).Delete(ctx, state.Metadata.Name.ValueString(), opts)
	if err != nil && !gpupaas.IsNotFound(err) {
		handleAPIError(&resp.Diagnostics, err, "Delete workspace collaborator", nil)
	}
}

func (r *workspaceCollaboratorResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	project, workspace, name, err := ParseWorkspaceScopedImportID(req.ID)
	if err != nil {
		resp.Diagnostics.AddError("Invalid import ID", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("metadata").AtName("project"), project)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("metadata").AtName("workspace"), workspace)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("metadata").AtName("name"), name)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), project+"/"+workspace+"/"+name)...)
}

// ---- converters ---------------------------------------------------------

func collabModelToSDK(m workspaceCollaboratorModel) *apiv1.WorkspaceCollaborator {
	return &apiv1.WorkspaceCollaborator{
		TypeMeta: apiv1.TypeMeta{APIVersion: apiv1.APIVersion, Kind: apiv1.KindWorkspaceCollaborator},
		Metadata: metadataToSDK(m.Metadata),
		Spec: apiv1.WorkspaceCollaboratorSpec{
			Username:  stringOr(m.Spec.Username, ""),
			Email:     stringOr(m.Spec.Email, ""),
			FirstName: stringOr(m.Spec.FirstName, ""),
			LastName:  stringOr(m.Spec.LastName, ""),
			Role:      stringOr(m.Spec.Role, ""),
			UserType:  stringOr(m.Spec.UserType, ""),
			IsSSOUser: m.Spec.IsSSOUser.ValueBool(),
		},
	}
}

func collabSDKToModel(c *apiv1.WorkspaceCollaborator, project, workspace string, prior workspaceCollaboratorModel) workspaceCollaboratorModel {
	if c == nil {
		return prior
	}
	if c.Metadata.Project == "" {
		c.Metadata.Project = project
	}
	if c.Metadata.Workspace == "" {
		c.Metadata.Workspace = workspace
	}
	name := c.Metadata.Name
	if name == "" {
		name = c.CollaboratorUsername()
	}
	return workspaceCollaboratorModel{
		ID:         types.StringValue(project + "/" + workspace + "/" + name),
		APIVersion: types.StringValue(firstNonEmpty(c.APIVersion, apiv1.APIVersion)),
		Kind:       types.StringValue(firstNonEmpty(c.Kind, apiv1.KindWorkspaceCollaborator)),
		Metadata:   metadataFromSDK(c.Metadata),
		Spec: workspaceCollaboratorSpec{
			Username:  nullableString(c.Spec.Username),
			Email:     nullableString(c.Spec.Email),
			FirstName: nullableString(c.Spec.FirstName),
			LastName:  nullableString(c.Spec.LastName),
			Role:      types.StringValue(c.Spec.Role),
			UserType:  nullableString(c.Spec.UserType),
			IsSSOUser: types.BoolValue(c.Spec.IsSSOUser),
		},
		Status: workspaceCollaboratorStatus{
			Phase: nullableString(c.Status.Phase),
			Role:  nullableString(c.Status.Role),
		},
	}
}
