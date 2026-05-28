package resources

import (
	"context"

	gpupaas "github.com/gpupaas-ai/gpupaas-go"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	dsschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/gpupaas-ai/terraform-provider-gpupaas/internal/client"
)

var (
	_ datasource.DataSource              = (*virtualMachineStatusDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*virtualMachineStatusDataSource)(nil)
)

// NewVirtualMachineStatusDataSource constructs the gpupaas_virtual_machine_status
// data source. It calls the SDK's GetStatus sub-route (POST not used; GET) to
// surface the live provisioning state of a virtual machine without holding a
// full resource definition in state.
func NewVirtualMachineStatusDataSource() datasource.DataSource {
	return &virtualMachineStatusDataSource{}
}

type virtualMachineStatusDataSource struct{ pd *client.ProviderData }

type virtualMachineStatusDataSourceModel struct {
	ID        types.String         `tfsdk:"id"`
	Name      types.String         `tfsdk:"name"`
	Project   types.String         `tfsdk:"project"`
	Workspace types.String         `tfsdk:"workspace"`
	Status    virtualMachineStatus `tfsdk:"status"`
}

func (d *virtualMachineStatusDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_virtual_machine_status"
}

func (d *virtualMachineStatusDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = dsschema.Schema{
		MarkdownDescription: "Live provisioning status of a GPU PaaS virtual machine. " +
			"Backed by `GET .../virtualmachines/{name}/status`. Returns the same " +
			"`status` block exposed by `gpupaas_virtual_machine`.",
		Attributes: map[string]dsschema.Attribute{
			"id":        dsschema.StringAttribute{Computed: true},
			"name":      dsschema.StringAttribute{Required: true, MarkdownDescription: "Virtual machine name to query."},
			"project":   dsschema.StringAttribute{Required: true, MarkdownDescription: "Project that owns the virtual machine."},
			"workspace": dsschema.StringAttribute{Optional: true, MarkdownDescription: "Workspace that owns the virtual machine; omit for project-scoped VMs."},
			"status":    virtualMachineStatusDataSourceAttribute(),
		},
	}
}

func (d *virtualMachineStatusDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.pd = providerData(req.ProviderData, &resp.Diagnostics)
}

func (d *virtualMachineStatusDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	if d.pd == nil {
		resp.Diagnostics.AddError("Provider not configured", "Provider data is nil")
		return
	}
	var cfg virtualMachineStatusDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	project := cfg.Project.ValueString()
	workspace := stringOr(cfg.Workspace, "")
	var vmClient = d.pd.Clientset.V1alpha1().VirtualMachines(project)
	if workspace != "" {
		vmClient = d.pd.Clientset.V1alpha1().Workspaces(project).VirtualMachines(workspace)
	}
	out, err := vmClient.GetStatus(ctx, cfg.Name.ValueString(), gpupaas.GetOptions{})
	if err != nil {
		handleAPIError(&resp.Diagnostics, err, "Read virtual machine status", nil)
		return
	}
	// Reuse the resource-side projection to get the status block. We discard
	// the spec/metadata portions because the status data source's contract is
	// purely status-focused.
	full := virtualMachineSDKToModel(ctx, out, project, workspace, &resp.Diagnostics)
	cfg.ID = full.ID
	cfg.Status = full.Status
	resp.Diagnostics.Append(resp.State.Set(ctx, &cfg)...)
}
