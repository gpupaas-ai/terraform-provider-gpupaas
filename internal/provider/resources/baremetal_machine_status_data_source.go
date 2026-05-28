package resources

import (
	"context"
	"encoding/json"

	gpupaas "github.com/gpupaas-ai/gpupaas-go"
	apiv1 "github.com/gpupaas-ai/gpupaas-go/apis/v1alpha1"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	dsschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/gpupaas-ai/terraform-provider-gpupaas/internal/client"
)

var (
	_ datasource.DataSource              = (*baremetalMachineStatusDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*baremetalMachineStatusDataSource)(nil)
)

// NewBaremetalMachineStatusDataSource constructs the
// gpupaas_baremetal_machine_status data source. It calls the SDK's
// GetStatusInfo sub-route (GET .../baremetalmachines/{name}/status) to surface
// the free-form runtime info (hardware, network, ...) for a machine. This is
// distinct from the resource's embedded `status.conditions`.
func NewBaremetalMachineStatusDataSource() datasource.DataSource {
	return &baremetalMachineStatusDataSource{}
}

type baremetalMachineStatusDataSource struct{ pd *client.ProviderData }

type baremetalMachineStatusDataSourceModel struct {
	ID       types.String `tfsdk:"id"`
	Name     types.String `tfsdk:"name"`
	Project  types.String `tfsdk:"project"`
	DataJSON types.String `tfsdk:"data_json"`
}

func (d *baremetalMachineStatusDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_baremetal_machine_status"
}

func (d *baremetalMachineStatusDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = dsschema.Schema{
		MarkdownDescription: "Live runtime info for a GPU PaaS baremetal machine. " +
			"Backed by `GET .../baremetalmachines/{name}/status`. The payload is " +
			"free-form (hardware, network, ...) and is surfaced as a JSON-encoded " +
			"string in `data_json`. Decode with Terraform's `jsondecode()`.",
		Attributes: map[string]dsschema.Attribute{
			"id":      dsschema.StringAttribute{Computed: true},
			"name":    dsschema.StringAttribute{Required: true, MarkdownDescription: "Baremetal machine name to query."},
			"project": dsschema.StringAttribute{Required: true, MarkdownDescription: "Project that owns the machine."},
			"data_json": dsschema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "JSON-encoded free-form runtime info returned by the platform.",
			},
		},
	}
}

func (d *baremetalMachineStatusDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.pd = providerData(req.ProviderData, &resp.Diagnostics)
}

func (d *baremetalMachineStatusDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	if d.pd == nil {
		resp.Diagnostics.AddError("Provider not configured", "Provider data is nil")
		return
	}
	var cfg baremetalMachineStatusDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	project := cfg.Project.ValueString()
	out, err := d.pd.Clientset.V1alpha1().BaremetalMachines(project).GetStatusInfo(ctx, cfg.Name.ValueString(), gpupaas.GetOptions{})
	if err != nil {
		handleAPIError(&resp.Diagnostics, err, "Read baremetal machine status", nil)
		return
	}
	cfg.ID = types.StringValue(project + "/" + cfg.Name.ValueString())
	cfg.DataJSON = baremetalStatusDataJSON(out)
	resp.Diagnostics.Append(resp.State.Set(ctx, &cfg)...)
}

// baremetalStatusDataJSON marshals the free-form status fields to a JSON
// string. Returns a null string when there is nothing to surface.
func baremetalStatusDataJSON(info *apiv1.BaremetalMachineInfo) types.String {
	if info == nil || len(info.Data.Fields) == 0 {
		return types.StringNull()
	}
	b, err := json.Marshal(info.Data.Fields)
	if err != nil {
		return types.StringNull()
	}
	return types.StringValue(string(b))
}
