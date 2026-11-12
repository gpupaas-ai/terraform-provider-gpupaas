package resources

import (
	"context"

	gpupaas "github.com/gpupaas-ai/gpupaas-go"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	dsschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/gpupaas-ai/terraform-provider-gpupaas/internal/client"
)

var (
	_ datasource.DataSource              = (*workspaceDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*workspaceDataSource)(nil)
)

// NewWorkspaceDataSource constructs the gpupaas_workspace data source.
func NewWorkspaceDataSource() datasource.DataSource { return &workspaceDataSource{} }

type workspaceDataSource struct{ pd *client.ProviderData }

type workspaceDataSourceModel struct {
	ID       types.String  `tfsdk:"id"`
	Name     types.String  `tfsdk:"name"`
	Project  types.String  `tfsdk:"project"`
	Metadata MetadataModel `tfsdk:"metadata"`
}

func (d *workspaceDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_workspace"
}

func (d *workspaceDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = dsschema.Schema{
		MarkdownDescription: "Look up a workspace by name within a project.",
		Attributes: map[string]dsschema.Attribute{
			"id":       dsschema.StringAttribute{Computed: true},
			"name":     dsschema.StringAttribute{Required: true},
			"project":  dsschema.StringAttribute{Required: true},
			"metadata": MetadataDataSourceAttribute(),
		},
	}
}

func (d *workspaceDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.pd = providerData(req.ProviderData, &resp.Diagnostics)
}

func (d *workspaceDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	if d.pd == nil {
		return
	}
	var cfg workspaceDataSourceModel
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("name"), &cfg.Name)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("project"), &cfg.Project)...)
	if resp.Diagnostics.HasError() {
		return
	}
	project, name := cfg.Project.ValueString(), cfg.Name.ValueString()
	srv, err := d.pd.Clientset.V1alpha1().Workspaces(project).Get(ctx, name, gpupaas.GetOptions{})
	if err != nil {
		handleAPIError(&resp.Diagnostics, err, "Read workspace", nil)
		return
	}
	cfg.ID = types.StringValue(workspaceID(project, name))
	cfg.Metadata = metadataFromSDK(srv.Metadata)
	resp.Diagnostics.Append(resp.State.Set(ctx, &cfg)...)
}
