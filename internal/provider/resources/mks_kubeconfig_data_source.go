package resources

import (
	"context"

	typed "github.com/gpupaas-ai/gpupaas-go/clientset/typed/v1alpha1"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	dsschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/gpupaas-ai/terraform-provider-gpupaas/internal/client"
)

var (
	_ datasource.DataSource              = (*mksKubeconfigDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*mksKubeconfigDataSource)(nil)
)

// NewMKSKubeconfigDataSource constructs the gpupaas_kubernetes_cluster_kubeconfig data source.
func NewMKSKubeconfigDataSource() datasource.DataSource { return &mksKubeconfigDataSource{} }

type mksKubeconfigDataSource struct{ pd *client.ProviderData }

type mksKubeconfigDataSourceModel struct {
	ID         types.String `tfsdk:"id"`
	Name       types.String `tfsdk:"name"`
	Project    types.String `tfsdk:"project"`
	Workspace  types.String `tfsdk:"workspace"`
	Kubeconfig types.String `tfsdk:"kubeconfig"`
}

func (d *mksKubeconfigDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_kubernetes_cluster_kubeconfig"
}

func (d *mksKubeconfigDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = dsschema.Schema{
		MarkdownDescription: "The user kubeconfig of a running first-class managed Kubernetes cluster, the same file the console's " +
			"\"Download Kubeconfig\" gives. It is read each time Terraform refreshes, so it follows the credentials the platform issues. " +
			"The cluster must be registered and running, or the read fails. The platform issues new client credentials with every download, so the value differs between reads: " +
			"Terraform shows it as a changed output, never as a resource change. The platform looks the cluster up by name only, so the read fails if the name matches more than one cluster (for example in two projects) rather than return another cluster's credentials. The kubeconfig is sensitive and is stored in the state.",
		Attributes: map[string]dsschema.Attribute{
			"id":        dsschema.StringAttribute{Computed: true},
			"name":      dsschema.StringAttribute{Required: true, MarkdownDescription: "Name of the cluster."},
			"project":   dsschema.StringAttribute{Required: true},
			"workspace": dsschema.StringAttribute{Optional: true, MarkdownDescription: "Workspace that owns the cluster; omit for a project-scoped cluster."},
			"kubeconfig": dsschema.StringAttribute{
				Computed:            true,
				Sensitive:           true,
				MarkdownDescription: "The kubeconfig file content (YAML).",
			},
		},
	}
}

func (d *mksKubeconfigDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.pd = providerData(req.ProviderData, &resp.Diagnostics)
}

func (d *mksKubeconfigDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	if d.pd == nil {
		return
	}
	var cfg mksKubeconfigDataSourceModel
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("name"), &cfg.Name)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("project"), &cfg.Project)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("workspace"), &cfg.Workspace)...)
	if resp.Diagnostics.HasError() {
		return
	}
	project, workspace, name := cfg.Project.ValueString(), stringOr(cfg.Workspace, ""), cfg.Name.ValueString()
	var clusters typed.MKSClusterInterface
	if workspace == "" {
		clusters = d.pd.Clientset.V1alpha1().MKSClusters(project)
	} else {
		clusters = d.pd.Clientset.V1alpha1().Workspaces(project).MKSClusters(workspace)
	}
	raw, err := clusters.Kubeconfig(ctx, name)
	if err != nil {
		handleAPIError(&resp.Diagnostics, err, "Download kubeconfig", nil)
		return
	}
	cfg.ID = types.StringValue(mksClusterID(project, workspace, name))
	cfg.Kubeconfig = types.StringValue(string(raw))
	resp.Diagnostics.Append(resp.State.Set(ctx, &cfg)...)
}
