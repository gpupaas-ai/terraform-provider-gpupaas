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
	_ resource.Resource                = (*workspaceResource)(nil)
	_ resource.ResourceWithImportState = (*workspaceResource)(nil)
	_ resource.ResourceWithConfigure   = (*workspaceResource)(nil)
)

// NewWorkspaceResource is the resource constructor for gpupaas_workspace.
func NewWorkspaceResource() resource.Resource { return &workspaceResource{} }

type workspaceResource struct{ pd *client.ProviderData }

type workspaceResourceModel struct {
	ID         types.String    `tfsdk:"id"`
	APIVersion types.String    `tfsdk:"api_version"`
	Kind       types.String    `tfsdk:"kind"`
	Metadata   MetadataModel   `tfsdk:"metadata"`
	Spec       workspaceSpec   `tfsdk:"spec"`
	Status     workspaceStatus `tfsdk:"status"`
}

type workspaceSpec struct {
	DisplayName types.String `tfsdk:"display_name"`
	Description types.String `tfsdk:"description"`
	IconURL     types.String `tfsdk:"icon_url"`
	Readme      types.String `tfsdk:"readme"`
}

type workspaceStatus struct {
	Phase types.String `tfsdk:"phase"`
}

func (r *workspaceResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_workspace"
}

func (r *workspaceResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "GPU PaaS workspace within a project (`apiVersion: " + apiv1.APIVersion + "`, `kind: Workspace`).",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"api_version": schema.StringAttribute{Computed: true},
			"kind":        schema.StringAttribute{Computed: true},
			"metadata":    MetadataResourceAttribute(false, true),
			"spec": schema.SingleNestedAttribute{
				Required: true,
				Attributes: map[string]schema.Attribute{
					"display_name": schema.StringAttribute{Optional: true},
					"description":  schema.StringAttribute{Optional: true},
					"icon_url":     schema.StringAttribute{Optional: true},
					"readme":       schema.StringAttribute{Optional: true},
				},
			},
			"status": schema.SingleNestedAttribute{
				Computed: true,
				Attributes: map[string]schema.Attribute{
					"phase": schema.StringAttribute{Computed: true},
				},
			},
		},
	}
}

func (r *workspaceResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.pd = providerData(req.ProviderData, &resp.Diagnostics)
}

func (r *workspaceResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	if r.pd == nil {
		resp.Diagnostics.AddError("Provider not configured", "Provider data is nil; check provider configuration.")
		return
	}
	var plan workspaceResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	project := plan.Metadata.Project.ValueString()
	out, err := r.pd.Clientset.V1alpha1().Workspaces(project).Create(ctx, workspaceModelToSDK(plan), gpupaas.CreateOptions{})
	if err != nil {
		handleAPIError(&resp.Diagnostics, err, "Create workspace", nil)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, workspaceSDKToModel(out, project))...)
}

func (r *workspaceResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	if r.pd == nil {
		return
	}
	var state workspaceResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	project := state.Metadata.Project.ValueString()
	out, err := r.pd.Clientset.V1alpha1().Workspaces(project).Get(ctx, state.Metadata.Name.ValueString(), gpupaas.GetOptions{})
	if err != nil {
		handleAPIError(&resp.Diagnostics, err, "Read workspace", func() { resp.State.RemoveResource(ctx) })
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, workspaceSDKToModel(out, project))...)
}

func (r *workspaceResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	if r.pd == nil {
		return
	}
	var plan workspaceResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	project := plan.Metadata.Project.ValueString()
	out, err := r.pd.Clientset.V1alpha1().Workspaces(project).Update(ctx, workspaceModelToSDK(plan), gpupaas.UpdateOptions{})
	if err != nil {
		handleAPIError(&resp.Diagnostics, err, "Update workspace", nil)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, workspaceSDKToModel(out, project))...)
}

func (r *workspaceResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	if r.pd == nil {
		return
	}
	var state workspaceResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	project := state.Metadata.Project.ValueString()
	err := r.pd.Clientset.V1alpha1().Workspaces(project).Delete(ctx, state.Metadata.Name.ValueString(), gpupaas.DeleteOptions{IgnoreNotFound: true})
	if err != nil && !gpupaas.IsNotFound(err) {
		handleAPIError(&resp.Diagnostics, err, "Delete workspace", nil)
	}
}

func (r *workspaceResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	project, name, err := ParseProjectScopedImportID(req.ID)
	if err != nil {
		resp.Diagnostics.AddError("Invalid import ID", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("metadata").AtName("name"), name)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("metadata").AtName("project"), project)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), project+"/"+name)...)
}

// ---- converters ---------------------------------------------------------

func workspaceModelToSDK(m workspaceResourceModel) *apiv1.Workspace {
	return &apiv1.Workspace{
		TypeMeta: apiv1.TypeMeta{APIVersion: apiv1.APIVersion, Kind: apiv1.KindWorkspace},
		Metadata: metadataToSDK(m.Metadata),
		Spec: apiv1.WorkspaceSpec{
			DisplayName: stringOr(m.Spec.DisplayName, ""),
			Description: stringOr(m.Spec.Description, ""),
			IconURL:     stringOr(m.Spec.IconURL, ""),
			Readme:      stringOr(m.Spec.Readme, ""),
		},
	}
}

func workspaceSDKToModel(w *apiv1.Workspace, project string) workspaceResourceModel {
	if w == nil {
		return workspaceResourceModel{}
	}
	if w.Metadata.Project == "" {
		w.Metadata.Project = project
	}
	return workspaceResourceModel{
		ID:         types.StringValue(w.Metadata.Project + "/" + w.Metadata.Name),
		APIVersion: types.StringValue(firstNonEmpty(w.APIVersion, apiv1.APIVersion)),
		Kind:       types.StringValue(firstNonEmpty(w.Kind, apiv1.KindWorkspace)),
		Metadata:   metadataFromSDK(w.Metadata),
		Spec: workspaceSpec{
			DisplayName: nullableString(w.Spec.DisplayName),
			Description: nullableString(w.Spec.Description),
			IconURL:     nullableString(w.Spec.IconURL),
			Readme:      nullableString(w.Spec.Readme),
		},
		Status: workspaceStatus{Phase: nullableString(w.Status.Phase)},
	}
}
