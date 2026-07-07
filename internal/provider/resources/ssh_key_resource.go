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
	_ resource.Resource                = (*sshKeyResource)(nil)
	_ resource.ResourceWithImportState = (*sshKeyResource)(nil)
	_ resource.ResourceWithConfigure   = (*sshKeyResource)(nil)
)

// NewSshKeyResource constructs the gpupaas_ssh_key resource.
func NewSshKeyResource() resource.Resource { return &sshKeyResource{} }

type sshKeyResource struct{ pd *client.ProviderData }

type sshKeyResourceModel struct {
	ID         types.String  `tfsdk:"id"`
	APIVersion types.String  `tfsdk:"api_version"`
	Kind       types.String  `tfsdk:"kind"`
	Metadata   MetadataModel `tfsdk:"metadata"`
	Spec       sshKeySpec    `tfsdk:"spec"`
	Status     sshKeyStatus  `tfsdk:"status"`
}

type sshKeySpec struct {
	SSHKey    resourceRefModel `tfsdk:"ssh_key"`
	Type      types.String     `tfsdk:"type"`
	Name      types.String     `tfsdk:"name"`
	PublicKey types.String     `tfsdk:"public_key"`
	Sharing   *SharingModel    `tfsdk:"sharing"`
}

type sshKeyStatus struct {
	Status types.String `tfsdk:"status"`
	Reason types.String `tfsdk:"reason"`
	Action types.String `tfsdk:"action"`
}

func (r *sshKeyResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_ssh_key"
}

func (r *sshKeyResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "GPU PaaS SSH key (`apiVersion: " + apiv1.APIVersion + "`, `kind: SshKey`). Supports project- or workspace-scoped placement via metadata.workspace. " +
			"Only `sharing` is mutable Day-2; `public_key`/`type`/metadata are immutable after creation.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"api_version": schema.StringAttribute{Computed: true},
			"kind":        schema.StringAttribute{Computed: true},
			"metadata":    immutableMetadataAttribute(true, true),
			"spec": schema.SingleNestedAttribute{
				Required: true,
				Attributes: map[string]schema.Attribute{
					"ssh_key": immutableResourceRefSchema("SSH key catalog reference."),
					"type": schema.StringAttribute{
						Optional:      true,
						PlanModifiers: []planmodifier.String{immutableString()},
					},
					"name": schema.StringAttribute{
						Optional:            true,
						MarkdownDescription: "Optional logical key name. Falls back to metadata.name.",
						PlanModifiers:       []planmodifier.String{immutableString()},
					},
					"public_key": schema.StringAttribute{
						Optional:            true,
						MarkdownDescription: "OpenSSH public key string.",
						PlanModifiers:       []planmodifier.String{immutableString()},
					},
					// sharing stays mutable Day-2 (D4).
					"sharing": SharingResourceAttribute(),
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

func (r *sshKeyResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.pd = providerData(req.ProviderData, &resp.Diagnostics)
}

func (r *sshKeyResource) client(project, workspace string) typed.SshKeyInterface {
	if workspace == "" {
		return r.pd.Clientset.V1alpha1().SshKeys(project)
	}
	return r.pd.Clientset.V1alpha1().Workspaces(project).SshKeys(workspace)
}

func (r *sshKeyResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	if r.pd == nil {
		resp.Diagnostics.AddError("Provider not configured", "Provider data is nil; check provider configuration.")
		return
	}
	var plan sshKeyResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	project := plan.Metadata.Project.ValueString()
	workspace := plan.Metadata.Workspace.ValueString()
	obj := sshKeyModelToSDK(ctx, plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	out, err := r.client(project, workspace).Create(ctx, obj, gpupaas.CreateOptions{})
	if err != nil {
		handleAPIError(&resp.Diagnostics, err, "Create ssh key", nil)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, sshKeySDKToModel(ctx, out, project, workspace, &resp.Diagnostics))...)
}

func (r *sshKeyResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	if r.pd == nil {
		return
	}
	var state sshKeyResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	project := state.Metadata.Project.ValueString()
	workspace := state.Metadata.Workspace.ValueString()
	out, err := r.client(project, workspace).Get(ctx, state.Metadata.Name.ValueString(), gpupaas.GetOptions{})
	if err != nil {
		handleAPIError(&resp.Diagnostics, err, "Read ssh key", func() { resp.State.RemoveResource(ctx) })
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, sshKeySDKToModel(ctx, out, project, workspace, &resp.Diagnostics))...)
}

func (r *sshKeyResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	if r.pd == nil {
		return
	}
	var plan, state sshKeyResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	// Belt-and-braces: schema-level immutable() modifiers already block these
	// edits at plan time; this is a defensive second gate (plan.md 3.3).
	// sharing is the only Day-2-mutable spec field for SshKey.
	if plan.Spec.SSHKey.Name.ValueString() != state.Spec.SSHKey.Name.ValueString() ||
		stringOr(plan.Spec.Type, "") != stringOr(state.Spec.Type, "") ||
		stringOr(plan.Spec.Name, "") != stringOr(state.Spec.Name, "") ||
		stringOr(plan.Spec.PublicKey, "") != stringOr(state.Spec.PublicKey, "") {
		resp.Diagnostics.AddError(
			"Immutable field changed",
			"Only sharing can be modified on an existing ssh key; destroy and recreate explicitly.",
		)
		return
	}
	project := plan.Metadata.Project.ValueString()
	workspace := plan.Metadata.Workspace.ValueString()
	obj := sshKeyModelToSDK(ctx, plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	out, err := r.client(project, workspace).Create(ctx, obj, gpupaas.CreateOptions{})
	if err != nil {
		handleAPIError(&resp.Diagnostics, err, "Update ssh key", nil)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, sshKeySDKToModel(ctx, out, project, workspace, &resp.Diagnostics))...)
}

func (r *sshKeyResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	if r.pd == nil {
		return
	}
	var state sshKeyResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	project := state.Metadata.Project.ValueString()
	workspace := state.Metadata.Workspace.ValueString()
	err := r.client(project, workspace).Delete(ctx, state.Metadata.Name.ValueString(), gpupaas.DeleteOptions{IgnoreNotFound: true})
	if err != nil && !gpupaas.IsNotFound(err) {
		handleAPIError(&resp.Diagnostics, err, "Delete ssh key", nil)
	}
}

func (r *sshKeyResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
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

func sshKeyModelToSDK(ctx context.Context, m sshKeyResourceModel, diags *diag.Diagnostics) *apiv1.SshKey {
	return &apiv1.SshKey{
		TypeMeta: apiv1.TypeMeta{APIVersion: apiv1.APIVersion, Kind: apiv1.KindSshKey},
		Metadata: metadataToSDK(m.Metadata),
		Spec: apiv1.SshKeySpec{
			SSHKey:    resourceRefToSDK(m.Spec.SSHKey),
			Type:      stringOr(m.Spec.Type, ""),
			Name:      stringOr(m.Spec.Name, ""),
			PublicKey: stringOr(m.Spec.PublicKey, ""),
			Sharing:   sharingToSDK(ctx, m.Spec.Sharing, diags),
		},
	}
}

func sshKeySDKToModel(ctx context.Context, s *apiv1.SshKey, project, workspace string, diags *diag.Diagnostics) sshKeyResourceModel {
	if s == nil {
		return sshKeyResourceModel{}
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
	return sshKeyResourceModel{
		ID:         types.StringValue(id),
		APIVersion: types.StringValue(firstNonEmpty(s.APIVersion, apiv1.APIVersion)),
		Kind:       types.StringValue(firstNonEmpty(s.Kind, apiv1.KindSshKey)),
		Metadata:   metadataFromSDK(s.Metadata),
		Spec: sshKeySpec{
			SSHKey:    resourceRefFromSDK(s.Spec.SSHKey),
			Type:      nullableString(s.Spec.Type),
			Name:      nullableString(s.Spec.Name),
			PublicKey: nullableString(s.Spec.PublicKey),
			Sharing:   sharingFromSDK(ctx, s.Spec.Sharing, diags),
		},
		Status: sshKeyStatus{
			Status: nullableString(s.Status.Status),
			Reason: nullableString(s.Status.Reason),
			Action: nullableString(s.Status.Action),
		},
	}
}
