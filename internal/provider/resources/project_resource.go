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
	_ resource.Resource                = (*projectResource)(nil)
	_ resource.ResourceWithImportState = (*projectResource)(nil)
	_ resource.ResourceWithConfigure   = (*projectResource)(nil)
)

// NewProjectResource is the resource constructor referenced by provider.Resources.
func NewProjectResource() resource.Resource { return &projectResource{} }

type projectResource struct {
	pd *client.ProviderData
}

type projectResourceModel struct {
	ID         types.String  `tfsdk:"id"`
	APIVersion types.String  `tfsdk:"api_version"`
	Kind       types.String  `tfsdk:"kind"`
	Metadata   MetadataModel `tfsdk:"metadata"`
	Spec       projectSpec   `tfsdk:"spec"`
	Status     projectStatus `tfsdk:"status"`
}

type projectSpec struct {
	DisplayName types.String `tfsdk:"display_name"`
	Description types.String `tfsdk:"description"`
	Default     types.Bool   `tfsdk:"default"`
}

type projectStatus struct {
	Phase types.String `tfsdk:"phase"`
}

func (r *projectResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_project"
}

func (r *projectResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "GPU PaaS top-level project (`apiVersion: " + apiv1.APIVersion + "`, `kind: Project`).",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Terraform-internal ID equal to the project name.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"api_version": schema.StringAttribute{Computed: true},
			"kind":        schema.StringAttribute{Computed: true},
			"metadata":    MetadataResourceAttribute(false, false),
			"spec": schema.SingleNestedAttribute{
				Required: true,
				Attributes: map[string]schema.Attribute{
					"display_name": schema.StringAttribute{Optional: true},
					"description":  schema.StringAttribute{Optional: true},
					"default":      schema.BoolAttribute{Optional: true},
				},
				MarkdownDescription: "Desired project state.",
			},
			"status": schema.SingleNestedAttribute{
				Computed: true,
				Attributes: map[string]schema.Attribute{
					"phase": schema.StringAttribute{Computed: true},
				},
				MarkdownDescription: "Observed project state.",
			},
		},
	}
}

func (r *projectResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.pd = providerData(req.ProviderData, &resp.Diagnostics)
}

func (r *projectResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	if r.pd == nil {
		resp.Diagnostics.AddError("Provider not configured", "Provider data is nil; check provider configuration.")
		return
	}
	var plan projectResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	obj := projectModelToSDK(plan)
	out, err := r.pd.Clientset.V1alpha1().Projects().Create(ctx, obj, gpupaas.CreateOptions{})
	if err != nil {
		handleAPIError(&resp.Diagnostics, err, "Create project", nil)
		return
	}
	state := projectSDKToModel(out)
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *projectResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	if r.pd == nil {
		return
	}
	var state projectResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	out, err := r.pd.Clientset.V1alpha1().Projects().Get(ctx, state.Metadata.Name.ValueString(), gpupaas.GetOptions{})
	if err != nil {
		handleAPIError(&resp.Diagnostics, err, "Read project", func() { resp.State.RemoveResource(ctx) })
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, projectSDKToModel(out))...)
}

func (r *projectResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	if r.pd == nil {
		return
	}
	var plan projectResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	out, err := r.pd.Clientset.V1alpha1().Projects().Update(ctx, projectModelToSDK(plan), gpupaas.UpdateOptions{})
	if err != nil {
		handleAPIError(&resp.Diagnostics, err, "Update project", nil)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, projectSDKToModel(out))...)
}

func (r *projectResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	if r.pd == nil {
		return
	}
	var state projectResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.pd.Clientset.V1alpha1().Projects().Delete(ctx, state.Metadata.Name.ValueString(), gpupaas.DeleteOptions{IgnoreNotFound: true})
	if err != nil && !gpupaas.IsNotFound(err) {
		handleAPIError(&resp.Diagnostics, err, "Delete project", nil)
	}
}

func (r *projectResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	name, err := ParseClusterScopedImportID(req.ID)
	if err != nil {
		resp.Diagnostics.AddError("Invalid import ID", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("metadata").AtName("name"), name)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), name)...)
}

// ---- converters ----------------------------------------------------------

func projectModelToSDK(m projectResourceModel) *apiv1.Project {
	return &apiv1.Project{
		TypeMeta: apiv1.TypeMeta{APIVersion: apiv1.APIVersion, Kind: apiv1.KindProject},
		Metadata: metadataToSDK(m.Metadata),
		Spec: apiv1.ProjectSpec{
			DisplayName: stringOr(m.Spec.DisplayName, ""),
			Description: stringOr(m.Spec.Description, ""),
			Default:     m.Spec.Default.ValueBool(),
		},
	}
}

func projectSDKToModel(p *apiv1.Project) projectResourceModel {
	if p == nil {
		return projectResourceModel{}
	}
	return projectResourceModel{
		ID:         types.StringValue(p.Metadata.Name),
		APIVersion: types.StringValue(firstNonEmpty(p.APIVersion, apiv1.APIVersion)),
		Kind:       types.StringValue(firstNonEmpty(p.Kind, apiv1.KindProject)),
		Metadata:   metadataFromSDK(p.Metadata),
		Spec: projectSpec{
			DisplayName: nullableString(p.Spec.DisplayName),
			Description: nullableString(p.Spec.Description),
			Default:     types.BoolValue(p.Spec.Default),
		},
		Status: projectStatus{Phase: nullableString(p.Status.Phase)},
	}
}
