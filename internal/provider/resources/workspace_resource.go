package resources

import (
	"context"
	"time"

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

// workspaceDeleteTimeout bounds the wait for a deleted workspace to disappear.
const workspaceDeleteTimeout = 2 * time.Minute

// NewWorkspaceResource constructs the gpupaas_workspace resource.
func NewWorkspaceResource() resource.Resource { return &workspaceResource{} }

type workspaceResource struct{ pd *client.ProviderData }

type workspaceResourceModel struct {
	ID       types.String  `tfsdk:"id"`
	Metadata MetadataModel `tfsdk:"metadata"`
}

func (r *workspaceResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_workspace"
}

func (r *workspaceResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	md := MetadataResourceAttribute(false, true)
	for _, k := range []string{"name", "project"} {
		sa := md.Attributes[k].(schema.StringAttribute)
		sa.PlanModifiers = append(sa.PlanModifiers, stringplanmodifier.RequiresReplace())
		md.Attributes[k] = sa
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: "A workspace (`paas.envmgmt.io/v1` `Workspace`): a partition of a project that holds instances, SSH keys and security groups. " +
			"Create it first and put the instances, SSH keys and security groups in it with `metadata.workspace`. " +
			"A workspace that still holds instances (virtual machines, bare metal machines, clusters, volumes) cannot be deleted: delete them first. Its SSH keys and security groups are removed with it. " +
			"It has metadata only: the platform accepts a readme and an icon but does not store them, so the provider does not offer them.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "`<project>/<name>`.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"metadata": md,
		},
	}
}

func (r *workspaceResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.pd = providerData(req.ProviderData, &resp.Diagnostics)
}

func workspaceID(project, name string) string { return project + "/" + name }

func workspaceToSDK(m workspaceResourceModel) *apiv1.Workspace {
	return &apiv1.Workspace{
		TypeMeta: apiv1.TypeMeta{APIVersion: apiv1.APIVersion, Kind: apiv1.KindWorkspace},
		Metadata: metadataToSDK(m.Metadata),
	}
}

// workspaceFromServer builds the state from what the platform returned. base holds the configured values
// (plan or prior state); fromRead says the server is the truth, as in a refresh or an import.
func workspaceFromServer(base workspaceResourceModel, srv *apiv1.Workspace, fromRead bool) workspaceResourceModel {
	out := base
	out.Metadata = metadataFromServer(base.Metadata, srv.Metadata, fromRead)
	out.Metadata.Project = types.StringValue(firstNonEmpty(srv.Metadata.Project, base.Metadata.Project.ValueString()))
	out.Metadata.Workspace = types.StringNull()
	out.ID = types.StringValue(workspaceID(out.Metadata.Project.ValueString(), out.Metadata.Name.ValueString()))
	return out
}

func (r *workspaceResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	if r.pd == nil {
		resp.Diagnostics.AddError("Provider not configured", "Provider data is nil; check provider configuration.")
		return
	}
	var plan workspaceResourceModel
	resp.Diagnostics.Append(decodePlan(ctx, req.Plan, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	project := plan.Metadata.Project.ValueString()
	out, err := r.pd.Clientset.V1alpha1().Workspaces(project).Create(ctx, workspaceToSDK(plan), gpupaas.CreateOptions{})
	if err != nil {
		handleAPIError(&resp.Diagnostics, err, "Create workspace", nil)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, workspaceFromServer(plan, out, false))...)
}

func (r *workspaceResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	if r.pd == nil {
		return
	}
	var state workspaceResourceModel
	resp.Diagnostics.Append(decodeState(ctx, req.State, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	project := state.Metadata.Project.ValueString()
	out, err := r.pd.Clientset.V1alpha1().Workspaces(project).Get(ctx, state.Metadata.Name.ValueString(), gpupaas.GetOptions{})
	if err != nil {
		handleAPIError(&resp.Diagnostics, err, "Read workspace", func() { resp.State.RemoveResource(ctx) })
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, workspaceFromServer(state, out, true))...)
}

func (r *workspaceResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	if r.pd == nil {
		return
	}
	var plan workspaceResourceModel
	resp.Diagnostics.Append(decodePlan(ctx, req.Plan, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	project := plan.Metadata.Project.ValueString()
	out, err := r.pd.Clientset.V1alpha1().Workspaces(project).Update(ctx, workspaceToSDK(plan), gpupaas.UpdateOptions{})
	if err != nil {
		handleAPIError(&resp.Diagnostics, err, "Update workspace", nil)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, workspaceFromServer(plan, out, false))...)
}

func (r *workspaceResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	if r.pd == nil {
		return
	}
	var state workspaceResourceModel
	resp.Diagnostics.Append(decodeState(ctx, req.State, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	project, name := state.Metadata.Project.ValueString(), state.Metadata.Name.ValueString()
	workspaces := r.pd.Clientset.V1alpha1().Workspaces(project)
	if err := workspaces.Delete(ctx, name, gpupaas.DeleteOptions{IgnoreNotFound: true}); err != nil {
		handleAPIError(&resp.Diagnostics, err, "Delete workspace", nil)
		return
	}
	// Resources created in the workspace next (a second apply, another project) need it gone, not going.
	ctx, cancel := context.WithTimeout(ctx, workspaceDeleteTimeout)
	defer cancel()
	for {
		if _, err := workspaces.Get(ctx, name, gpupaas.GetOptions{}); gpupaas.IsNotFound(err) {
			return
		} else if err != nil && ctx.Err() == nil {
			handleAPIError(&resp.Diagnostics, err, "Wait for workspace deletion", nil)
			return
		}
		select {
		case <-ctx.Done():
			resp.Diagnostics.AddError("Workspace still exists", "The platform accepted the delete but workspace "+name+" is still listed after "+workspaceDeleteTimeout.String()+".")
			return
		case <-time.After(pollInterval / 5):
		}
	}
}

func (r *workspaceResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	project, name, err := ParseProjectScopedImportID(req.ID)
	if err != nil {
		resp.Diagnostics.AddError("Invalid import ID", "Use <project>/<name>: "+err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("metadata").AtName("project"), project)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("metadata").AtName("name"), name)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), workspaceID(project, name))...)
}
