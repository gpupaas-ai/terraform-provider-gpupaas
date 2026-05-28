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
	_ datasource.DataSource              = (*projectDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*projectDataSource)(nil)
)

// NewProjectDataSource is the data source constructor for gpupaas_project.
func NewProjectDataSource() datasource.DataSource { return &projectDataSource{} }

type projectDataSource struct{ pd *client.ProviderData }

type projectDataSourceModel struct {
	ID         types.String  `tfsdk:"id"`
	Name       types.String  `tfsdk:"name"`
	APIVersion types.String  `tfsdk:"api_version"`
	Kind       types.String  `tfsdk:"kind"`
	Metadata   MetadataModel `tfsdk:"metadata"`
	Spec       projectSpec   `tfsdk:"spec"`
	Status     projectStatus `tfsdk:"status"`
}

func (d *projectDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_project"
}

func (d *projectDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Read a GPU PaaS project by name.",
		Attributes: map[string]schema.Attribute{
			"id":          schema.StringAttribute{Computed: true},
			"name":        schema.StringAttribute{Required: true, MarkdownDescription: "Project name to look up."},
			"api_version": schema.StringAttribute{Computed: true},
			"kind":        schema.StringAttribute{Computed: true},
			"metadata":    MetadataDataSourceAttribute(),
			"spec": schema.SingleNestedAttribute{
				Computed: true,
				Attributes: map[string]schema.Attribute{
					"display_name": schema.StringAttribute{Computed: true},
					"description":  schema.StringAttribute{Computed: true},
					"default":      schema.BoolAttribute{Computed: true},
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

func (d *projectDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.pd = providerData(req.ProviderData, &resp.Diagnostics)
}

func (d *projectDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	if d.pd == nil {
		return
	}
	var cfg projectDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	out, err := d.pd.Clientset.V1alpha1().Projects().Get(ctx, cfg.Name.ValueString(), gpupaas.GetOptions{})
	if err != nil {
		handleAPIError(&resp.Diagnostics, err, "Read project", nil)
		return
	}
	state := projectDataSourceModel{
		ID:         types.StringValue(out.Metadata.Name),
		Name:       types.StringValue(out.Metadata.Name),
		APIVersion: types.StringValue(firstNonEmpty(out.APIVersion, apiv1.APIVersion)),
		Kind:       types.StringValue(firstNonEmpty(out.Kind, apiv1.KindProject)),
		Metadata:   metadataFromSDK(out.Metadata),
		Spec: projectSpec{
			DisplayName: nullableString(out.Spec.DisplayName),
			Description: nullableString(out.Spec.Description),
			Default:     types.BoolValue(out.Spec.Default),
		},
		Status: projectStatus{Phase: nullableString(out.Status.Phase)},
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}
