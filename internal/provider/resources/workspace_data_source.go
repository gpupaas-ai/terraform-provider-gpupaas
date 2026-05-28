package resources

import (
	"context"

	gpupaas "github.com/gpupaas-ai/gpupaas-go"
	apiv1 "github.com/gpupaas-ai/gpupaas-go/apis/v1alpha1"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/gpupaas-ai/terraform-provider-gpupaas/internal/client"
)

var (
	_ datasource.DataSource              = (*workspaceDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*workspaceDataSource)(nil)
)

// NewWorkspaceDataSource is the data source constructor for gpupaas_workspace.
func NewWorkspaceDataSource() datasource.DataSource { return &workspaceDataSource{} }

type workspaceDataSource struct{ pd *client.ProviderData }

type workspaceDataSourceModel struct {
	ID         types.String    `tfsdk:"id"`
	Name       types.String    `tfsdk:"name"`
	Project    types.String    `tfsdk:"project"`
	APIVersion types.String    `tfsdk:"api_version"`
	Kind       types.String    `tfsdk:"kind"`
	Metadata   MetadataModel   `tfsdk:"metadata"`
	Spec       workspaceSpec   `tfsdk:"spec"`
	Status     workspaceStatus `tfsdk:"status"`
}

func (d *workspaceDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_workspace"
}

func (d *workspaceDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Read a GPU PaaS workspace within a project by name.",
		Attributes: map[string]schema.Attribute{
			"id":          schema.StringAttribute{Computed: true},
			"name":        schema.StringAttribute{Required: true},
			"project":     schema.StringAttribute{Required: true},
			"api_version": schema.StringAttribute{Computed: true},
			"kind":        schema.StringAttribute{Computed: true},
			"metadata":    MetadataDataSourceAttribute(),
			"spec": schema.SingleNestedAttribute{
				Computed: true,
				Attributes: map[string]schema.Attribute{
					"display_name": schema.StringAttribute{Computed: true},
					"description":  schema.StringAttribute{Computed: true},
					"icon_url":     schema.StringAttribute{Computed: true},
					"readme":       schema.StringAttribute{Computed: true},
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

func (d *workspaceDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.pd = providerData(req.ProviderData, &resp.Diagnostics)
}

func (d *workspaceDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	if d.pd == nil {
		return
	}
	var cfg workspaceDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	project := cfg.Project.ValueString()
	out, err := d.pd.Clientset.V1alpha1().Workspaces(project).Get(ctx, cfg.Name.ValueString(), gpupaas.GetOptions{})
	if err != nil {
		handleAPIError(&resp.Diagnostics, err, "Read workspace", nil)
		return
	}
	state := workspaceDataSourceModel{
		ID:         types.StringValue(project + "/" + out.Metadata.Name),
		Name:       types.StringValue(out.Metadata.Name),
		Project:    types.StringValue(project),
		APIVersion: types.StringValue(firstNonEmpty(out.APIVersion, apiv1.APIVersion)),
		Kind:       types.StringValue(firstNonEmpty(out.Kind, apiv1.KindWorkspace)),
		Metadata:   metadataFromSDK(out.Metadata),
		Spec: workspaceSpec{
			DisplayName: nullableString(out.Spec.DisplayName),
			Description: nullableString(out.Spec.Description),
			IconURL:     nullableString(out.Spec.IconURL),
			Readme:      nullableString(out.Spec.Readme),
		},
		Status: workspaceStatus{Phase: nullableString(out.Status.Phase)},
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}
