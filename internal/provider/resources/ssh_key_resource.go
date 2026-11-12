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
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/objectplanmodifier"
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

type sshKeySpecModel struct {
	PublicKey types.String  `tfsdk:"public_key"`
	Sharing   *SharingModel `tfsdk:"sharing"`
}

type sshKeyStatusModel struct {
	Status types.String `tfsdk:"status"`
	Reason types.String `tfsdk:"reason"`
}

type sshKeyResourceModel struct {
	ID       types.String        `tfsdk:"id"`
	Metadata scopedMetadataModel `tfsdk:"metadata"`
	Spec     sshKeySpecModel     `tfsdk:"spec"`
	Status   sshKeyStatusModel   `tfsdk:"status"`
}

func (r *sshKeyResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_ssh_key"
}

func (r *sshKeyResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	sharing := SharingResourceAttribute()
	sharing.PlanModifiers = []planmodifier.Object{objectplanmodifier.RequiresReplace()}
	resp.Schema = schema.Schema{
		MarkdownDescription: "An SSH public key (`dev.envmgmt.io/v1` `SshKey`), at project or workspace scope. Instances (VMs, bare metal, clusters) name a key of their workspace to install it. " +
			"The platform has no update for a key: changing any argument deletes the key and creates it again.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "`<project>/<workspace>/<name>`, or `<project>/<name>` for a project-scoped key.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"metadata": scopedMetadataAttribute("SSH key"),
			"spec": schema.SingleNestedAttribute{
				Required: true,
				Attributes: map[string]schema.Attribute{
					"public_key": schema.StringAttribute{
						Required:            true,
						MarkdownDescription: "The OpenSSH public key, for example the contents of `id_ed25519.pub`. Changing it replaces the key.",
						PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
					},
					"sharing": sharing,
				},
			},
			"status": schema.SingleNestedAttribute{
				Computed: true,
				Attributes: map[string]schema.Attribute{
					"status": schema.StringAttribute{Computed: true, MarkdownDescription: "Platform status, `success` once the key is stored."},
					"reason": schema.StringAttribute{Computed: true},
				},
			},
		},
	}
}

func (r *sshKeyResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.pd = providerData(req.ProviderData, &resp.Diagnostics)
}

func (r *sshKeyResource) keys(project, workspace string) typed.SshKeyInterface {
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
	resp.Diagnostics.Append(decodePlan(ctx, req.Plan, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	project, workspace := plan.Metadata.Project.ValueString(), stringOr(plan.Metadata.Workspace, "")
	obj := &apiv1.SshKey{
		TypeMeta: apiv1.TypeMeta{APIVersion: apiv1.APIVersion, Kind: apiv1.KindSshKey},
		Metadata: apiv1.ObjectMeta{Name: plan.Metadata.Name.ValueString(), Project: project, Workspace: workspace},
		Spec:     apiv1.SshKeySpec{PublicKey: plan.Spec.PublicKey.ValueString(), Sharing: sharingToSDK(ctx, plan.Spec.Sharing, &resp.Diagnostics)},
	}
	if resp.Diagnostics.HasError() {
		return
	}
	out, err := r.keys(project, workspace).Create(ctx, obj, gpupaas.CreateOptions{})
	if err != nil {
		handleAPIError(&resp.Diagnostics, err, "Create SSH key", nil)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, sshKeyModelFromServer(ctx, plan, out, &resp.Diagnostics))...)
}

func (r *sshKeyResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	if r.pd == nil {
		return
	}
	var state sshKeyResourceModel
	resp.Diagnostics.Append(decodeState(ctx, req.State, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	project, workspace := state.Metadata.Project.ValueString(), stringOr(state.Metadata.Workspace, "")
	out, err := r.keys(project, workspace).Get(ctx, state.Metadata.Name.ValueString(), gpupaas.GetOptions{})
	if err != nil {
		handleAPIError(&resp.Diagnostics, err, "Read SSH key", func() { resp.State.RemoveResource(ctx) })
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, sshKeyModelFromServer(ctx, state, out, &resp.Diagnostics))...)
}

// Update is never reached with a change: every argument forces a replacement.
func (r *sshKeyResource) Update(_ context.Context, _ resource.UpdateRequest, resp *resource.UpdateResponse) {
	resp.Diagnostics.AddError("SSH keys cannot be updated", "The platform has no update for an SSH key; change it by replacing it. This is a provider bug: the change should have planned a replacement.")
}

func (r *sshKeyResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	if r.pd == nil {
		return
	}
	var state sshKeyResourceModel
	resp.Diagnostics.Append(decodeState(ctx, req.State, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	project, workspace := state.Metadata.Project.ValueString(), stringOr(state.Metadata.Workspace, "")
	if err := r.keys(project, workspace).Delete(ctx, state.Metadata.Name.ValueString(), gpupaas.DeleteOptions{IgnoreNotFound: true}); err != nil {
		handleAPIError(&resp.Diagnostics, err, "Delete SSH key", nil)
	}
}

func (r *sshKeyResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	project, workspace, name, err := ParseFlexibleScopedImportID(req.ID)
	if err != nil {
		resp.Diagnostics.AddError("Invalid import ID", "Use <project>/<workspace>/<name> or <project>/<name>: "+err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("metadata").AtName("project"), project)...)
	if workspace != "" {
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("metadata").AtName("workspace"), workspace)...)
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("metadata").AtName("name"), name)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), scopedID(project, workspace, name))...)
}

// sshKeyModelFromServer builds the state from what the platform returned. base holds the configured
// values (plan or prior state): a key that is the same as the configured one keeps the configured text, so a
// platform that trims the comment does not look like drift.
func sshKeyModelFromServer(ctx context.Context, base sshKeyResourceModel, srv *apiv1.SshKey, diags *diag.Diagnostics) sshKeyResourceModel {
	out := base
	project, workspace := base.Metadata.Project.ValueString(), stringOr(base.Metadata.Workspace, "")
	out.ID = types.StringValue(scopedID(project, workspace, srv.Metadata.Name))
	out.Metadata.Name = types.StringValue(srv.Metadata.Name)
	if srv.Metadata.Name == "" {
		out.Metadata.Name = base.Metadata.Name
	}
	if !set(base.Spec.PublicKey) || !samePublicKey(base.Spec.PublicKey.ValueString(), srv.Spec.PublicKey) {
		out.Spec.PublicKey = nullableString(srv.Spec.PublicKey)
	}
	if isDefaultSharing(srv.Spec.Sharing) && !sharingConfigured(base.Spec.Sharing) {
		out.Spec.Sharing = nil
	} else {
		out.Spec.Sharing = sharingFromSDK(ctx, srv.Spec.Sharing, base.Spec.Sharing, diags)
	}
	out.Status = sshKeyStatusModel{Status: nullableString(srv.Status.Status), Reason: nullableString(srv.Status.Reason)}
	return out
}
