package resources

import (
	"context"

	gpupaas "github.com/gpupaas-ai/gpupaas-go"
	apiv1 "github.com/gpupaas-ai/gpupaas-go/apis/v1alpha1"
	typed "github.com/gpupaas-ai/gpupaas-go/clientset/typed/v1alpha1"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/gpupaas-ai/terraform-provider-gpupaas/internal/client"
)

var (
	_ resource.Resource                = (*storageResource)(nil)
	_ resource.ResourceWithImportState = (*storageResource)(nil)
	_ resource.ResourceWithConfigure   = (*storageResource)(nil)
)

// NewStorageResource constructs the gpupaas_storage resource.
func NewStorageResource() resource.Resource { return &storageResource{} }

type storageResource struct{ pd *client.ProviderData }

type storageResourceModel struct {
	ID         types.String  `tfsdk:"id"`
	APIVersion types.String  `tfsdk:"api_version"`
	Kind       types.String  `tfsdk:"kind"`
	Metadata   MetadataModel `tfsdk:"metadata"`
	Spec       storageSpec   `tfsdk:"spec"`
	Status     storageStatus `tfsdk:"status"`
}

type storageSpec struct {
	Storage                   resourceRefModel `tfsdk:"storage"`
	Type                      types.String     `tfsdk:"type"`
	Size                      types.String     `tfsdk:"size"`
	Datacenter                types.String     `tfsdk:"datacenter"`
	AccessPolicy              types.String     `tfsdk:"access_policy"`
	ContractTerm              types.String     `tfsdk:"contract_term"`
	EnableEncryptionAtRest    types.String     `tfsdk:"enable_encryption_at_rest"`
	EnableEncryptionInTransit types.String     `tfsdk:"enable_encryption_in_transit"`
	StorageType               types.String     `tfsdk:"storage_type"`
	Sharing                   *SharingModel    `tfsdk:"sharing"`
}

type storageStatus struct {
	Status types.String `tfsdk:"status"`
	Reason types.String `tfsdk:"reason"`
	Action types.String `tfsdk:"action"`
}

type resourceRefModel struct {
	Name          types.String `tfsdk:"name"`
	SystemCatalog types.Bool   `tfsdk:"system_catalog"`
}

func resourceRefSchema(desc string) schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{
		Required:            true,
		MarkdownDescription: desc,
		Attributes: map[string]schema.Attribute{
			"name":           schema.StringAttribute{Required: true},
			"system_catalog": schema.BoolAttribute{Optional: true},
		},
	}
}

func (r *storageResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_storage"
}

func (r *storageResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "GPU PaaS storage resource (`apiVersion: " + apiv1.APIVersion + "`, `kind: Storage`). Supports project- or workspace-scoped placement via metadata.workspace.",
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
					"storage":                      resourceRefSchema("Storage catalog reference (`name`, optional `system_catalog`)."),
					"type":                         schema.StringAttribute{Optional: true},
					"size":                         schema.StringAttribute{Optional: true},
					"datacenter":                   schema.StringAttribute{Optional: true},
					"access_policy":                schema.StringAttribute{Optional: true},
					"contract_term":                schema.StringAttribute{Optional: true},
					"enable_encryption_at_rest":    schema.StringAttribute{Optional: true},
					"enable_encryption_in_transit": schema.StringAttribute{Optional: true},
					"storage_type":                 schema.StringAttribute{Optional: true},
					"sharing":                      SharingResourceAttribute(),
				},
			},
			"status": schema.SingleNestedAttribute{
				Computed: true,
				Attributes: map[string]schema.Attribute{
					"status": schema.StringAttribute{Computed: true},
					"reason": schema.StringAttribute{Computed: true},
					"action": schema.StringAttribute{Computed: true},
				},
			},
		},
	}
}

func (r *storageResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.pd = providerData(req.ProviderData, &resp.Diagnostics)
}

func (r *storageResource) client(project, workspace string) typed.StorageInterface {
	if workspace == "" {
		return r.pd.Clientset.V1alpha1().Storages(project)
	}
	return r.pd.Clientset.V1alpha1().Workspaces(project).Storages(workspace)
}

func (r *storageResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	if r.pd == nil {
		resp.Diagnostics.AddError("Provider not configured", "Provider data is nil; check provider configuration.")
		return
	}
	var plan storageResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	project := plan.Metadata.Project.ValueString()
	workspace := plan.Metadata.Workspace.ValueString()
	obj := storageModelToSDK(ctx, plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	out, err := r.client(project, workspace).Create(ctx, obj, gpupaas.CreateOptions{})
	if err != nil {
		handleAPIError(&resp.Diagnostics, err, "Create storage", nil)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, storageSDKToModel(ctx, out, project, workspace, &resp.Diagnostics))...)
}

func (r *storageResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	if r.pd == nil {
		return
	}
	var state storageResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	project := state.Metadata.Project.ValueString()
	workspace := state.Metadata.Workspace.ValueString()
	out, err := r.client(project, workspace).Get(ctx, state.Metadata.Name.ValueString(), gpupaas.GetOptions{})
	if err != nil {
		handleAPIError(&resp.Diagnostics, err, "Read storage", func() { resp.State.RemoveResource(ctx) })
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, storageSDKToModel(ctx, out, project, workspace, &resp.Diagnostics))...)
}

func (r *storageResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	if r.pd == nil {
		return
	}
	var plan storageResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	project := plan.Metadata.Project.ValueString()
	workspace := plan.Metadata.Workspace.ValueString()
	obj := storageModelToSDK(ctx, plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	out, err := r.client(project, workspace).Create(ctx, obj, gpupaas.CreateOptions{})
	if err != nil {
		handleAPIError(&resp.Diagnostics, err, "Update storage", nil)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, storageSDKToModel(ctx, out, project, workspace, &resp.Diagnostics))...)
}

func (r *storageResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	if r.pd == nil {
		return
	}
	var state storageResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	project := state.Metadata.Project.ValueString()
	workspace := state.Metadata.Workspace.ValueString()
	err := r.client(project, workspace).Delete(ctx, state.Metadata.Name.ValueString(), gpupaas.DeleteOptions{IgnoreNotFound: true})
	if err != nil && !gpupaas.IsNotFound(err) {
		handleAPIError(&resp.Diagnostics, err, "Delete storage", nil)
	}
}

func (r *storageResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	project, workspace, name, err := ParseFlexibleScopedImportID(req.ID)
	if err != nil {
		resp.Diagnostics.AddError("Invalid import ID", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("metadata").AtName("project"), project)...)
	if workspace != "" {
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("metadata").AtName("workspace"), workspace)...)
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("metadata").AtName("name"), name)...)
	id := project + "/" + name
	if workspace != "" {
		id = project + "/" + workspace + "/" + name
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), id)...)
}

// ---- converters ---------------------------------------------------------

func resourceRefToSDK(r resourceRefModel) apiv1.ResourceRef {
	return apiv1.ResourceRef{
		Name:          stringOr(r.Name, ""),
		SystemCatalog: r.SystemCatalog.ValueBool(),
	}
}

func resourceRefFromSDK(r apiv1.ResourceRef) resourceRefModel {
	return resourceRefModel{
		Name:          types.StringValue(r.Name),
		SystemCatalog: types.BoolValue(r.SystemCatalog),
	}
}

func storageModelToSDK(ctx context.Context, m storageResourceModel, diags *diag.Diagnostics) *apiv1.Storage {
	return &apiv1.Storage{
		TypeMeta: apiv1.TypeMeta{APIVersion: apiv1.APIVersion, Kind: apiv1.KindStorage},
		Metadata: metadataToSDK(m.Metadata),
		Spec: apiv1.StorageSpec{
			Storage:                   resourceRefToSDK(m.Spec.Storage),
			Type:                      stringOr(m.Spec.Type, ""),
			Size:                      stringOr(m.Spec.Size, ""),
			Datacenter:                stringOr(m.Spec.Datacenter, ""),
			AccessPolicy:              stringOr(m.Spec.AccessPolicy, ""),
			ContractTerm:              stringOr(m.Spec.ContractTerm, ""),
			EnableEncryptionAtRest:    stringOr(m.Spec.EnableEncryptionAtRest, ""),
			EnableEncryptionInTransit: stringOr(m.Spec.EnableEncryptionInTransit, ""),
			StorageType:               stringOr(m.Spec.StorageType, ""),
			Sharing:                   sharingToSDK(ctx, m.Spec.Sharing, diags),
		},
	}
}

func storageSDKToModel(ctx context.Context, s *apiv1.Storage, project, workspace string, diags *diag.Diagnostics) storageResourceModel {
	if s == nil {
		return storageResourceModel{}
	}
	if s.Metadata.Project == "" {
		s.Metadata.Project = project
	}
	if s.Metadata.Workspace == "" {
		s.Metadata.Workspace = workspace
	}
	id := s.Metadata.Project + "/" + s.Metadata.Name
	if s.Metadata.Workspace != "" {
		id = s.Metadata.Project + "/" + s.Metadata.Workspace + "/" + s.Metadata.Name
	}
	return storageResourceModel{
		ID:         types.StringValue(id),
		APIVersion: types.StringValue(firstNonEmpty(s.APIVersion, apiv1.APIVersion)),
		Kind:       types.StringValue(firstNonEmpty(s.Kind, apiv1.KindStorage)),
		Metadata:   metadataFromSDK(s.Metadata),
		Spec: storageSpec{
			Storage:                   resourceRefFromSDK(s.Spec.Storage),
			Type:                      nullableString(s.Spec.Type),
			Size:                      nullableString(s.Spec.Size),
			Datacenter:                nullableString(s.Spec.Datacenter),
			AccessPolicy:              nullableString(s.Spec.AccessPolicy),
			ContractTerm:              nullableString(s.Spec.ContractTerm),
			EnableEncryptionAtRest:    nullableString(s.Spec.EnableEncryptionAtRest),
			EnableEncryptionInTransit: nullableString(s.Spec.EnableEncryptionInTransit),
			StorageType:               nullableString(s.Spec.StorageType),
			Sharing:                   sharingFromSDK(ctx, s.Spec.Sharing, diags),
		},
		Status: storageStatus{
			Status: nullableString(s.Status.Status),
			Reason: nullableString(s.Status.Reason),
			Action: nullableString(s.Status.Action),
		},
	}
}
