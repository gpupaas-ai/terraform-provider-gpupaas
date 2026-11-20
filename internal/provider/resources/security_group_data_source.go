package resources

import (
	"context"

	gpupaas "github.com/gpupaas-ai/gpupaas-go"
	apiv1 "github.com/gpupaas-ai/gpupaas-go/apis/v1alpha1"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	dsschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/gpupaas-ai/terraform-provider-gpupaas/internal/client"
)

var (
	_ datasource.DataSource              = (*securityGroupDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*securityGroupDataSource)(nil)
)

// NewSecurityGroupDataSource constructs the gpupaas_security_group data source.
func NewSecurityGroupDataSource() datasource.DataSource { return &securityGroupDataSource{} }

type securityGroupDataSource struct{ pd *client.ProviderData }

type securityGroupDataSpecModel struct {
	Rules []securityRuleModel `tfsdk:"rules"`
}

type securityGroupDataSourceModel struct {
	ID        types.String               `tfsdk:"id"`
	Name      types.String               `tfsdk:"name"`
	Project   types.String               `tfsdk:"project"`
	Workspace types.String               `tfsdk:"workspace"`
	Metadata  MetadataModel              `tfsdk:"metadata"`
	Spec      securityGroupDataSpecModel `tfsdk:"spec"`
	Status    sshKeyStatusModel          `tfsdk:"status"`
}

func (d *securityGroupDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_security_group"
}

func (d *securityGroupDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	str := func() dsschema.Attribute { return dsschema.StringAttribute{Computed: true} }
	resp.Schema = dsschema.Schema{
		MarkdownDescription: "Look up a security group by name within a project, optionally scoped to a workspace.",
		Attributes: map[string]dsschema.Attribute{
			"id":        dsschema.StringAttribute{Computed: true},
			"name":      dsschema.StringAttribute{Required: true},
			"project":   dsschema.StringAttribute{Required: true},
			"workspace": dsschema.StringAttribute{Optional: true, MarkdownDescription: "Workspace that owns the group; omit for a project-scoped group."},
			"metadata":  MetadataDataSourceAttribute(),
			"spec": dsschema.SingleNestedAttribute{
				Computed: true,
				Attributes: map[string]dsschema.Attribute{
					"rules": dsschema.ListNestedAttribute{
						Computed: true,
						NestedObject: dsschema.NestedAttributeObject{
							Attributes: map[string]dsschema.Attribute{
								"source_cidr": str(), "application": str(), "application_port": str(), "protocol": str(), "action": str(),
							},
						},
					},
				},
			},
			"status": dsschema.SingleNestedAttribute{
				Computed:   true,
				Attributes: map[string]dsschema.Attribute{"status": str(), "reason": str()},
			},
		},
	}
}

func (d *securityGroupDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.pd = providerData(req.ProviderData, &resp.Diagnostics)
}

func (d *securityGroupDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	if d.pd == nil {
		return
	}
	var cfg securityGroupDataSourceModel
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("name"), &cfg.Name)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("project"), &cfg.Project)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("workspace"), &cfg.Workspace)...)
	if resp.Diagnostics.HasError() {
		return
	}
	project, workspace, name := cfg.Project.ValueString(), stringOr(cfg.Workspace, ""), cfg.Name.ValueString()
	var groups interface {
		Get(context.Context, string, gpupaas.GetOptions) (*apiv1.SecurityGroup, error)
	}
	if workspace == "" {
		groups = d.pd.Clientset.V1alpha1().SecurityGroups(project)
	} else {
		groups = d.pd.Clientset.V1alpha1().Workspaces(project).SecurityGroups(workspace)
	}
	srv, err := groups.Get(ctx, name, gpupaas.GetOptions{})
	if err != nil {
		handleAPIError(&resp.Diagnostics, err, "Read security group", nil)
		return
	}
	cfg.ID = types.StringValue(scopedID(project, workspace, name))
	cfg.Metadata = metadataFromSDK(srv.Metadata)
	rules := make([]securityRuleModel, 0, len(srv.Spec.Rules))
	for _, r := range srv.Spec.Rules {
		rules = append(rules, ruleModelFromSDK(r))
	}
	cfg.Spec = securityGroupDataSpecModel{Rules: rules}
	cfg.Status = sshKeyStatusModel{Status: nullableString(srv.Status.Status), Reason: nullableString(srv.Status.Reason)}
	resp.Diagnostics.Append(resp.State.Set(ctx, &cfg)...)
}
