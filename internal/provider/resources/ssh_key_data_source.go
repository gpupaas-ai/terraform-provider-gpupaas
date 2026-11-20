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
	_ datasource.DataSource              = (*sshKeyDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*sshKeyDataSource)(nil)
)

// NewSshKeyDataSource constructs the gpupaas_ssh_key data source.
func NewSshKeyDataSource() datasource.DataSource { return &sshKeyDataSource{} }

type sshKeyDataSource struct{ pd *client.ProviderData }

type sshKeyDataSpecModel struct {
	PublicKey types.String `tfsdk:"public_key"`
}

type sshKeyDataSourceModel struct {
	ID        types.String        `tfsdk:"id"`
	Name      types.String        `tfsdk:"name"`
	Project   types.String        `tfsdk:"project"`
	Workspace types.String        `tfsdk:"workspace"`
	Metadata  MetadataModel       `tfsdk:"metadata"`
	Spec      sshKeyDataSpecModel `tfsdk:"spec"`
	Status    sshKeyStatusModel   `tfsdk:"status"`
}

func (d *sshKeyDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_ssh_key"
}

func (d *sshKeyDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = dsschema.Schema{
		MarkdownDescription: "Look up an SSH key by name within a project, optionally scoped to a workspace.",
		Attributes: map[string]dsschema.Attribute{
			"id":        dsschema.StringAttribute{Computed: true},
			"name":      dsschema.StringAttribute{Required: true},
			"project":   dsschema.StringAttribute{Required: true},
			"workspace": dsschema.StringAttribute{Optional: true, MarkdownDescription: "Workspace that owns the key; omit for a project-scoped key."},
			"metadata":  MetadataDataSourceAttribute(),
			"spec": dsschema.SingleNestedAttribute{
				Computed:   true,
				Attributes: map[string]dsschema.Attribute{"public_key": dsschema.StringAttribute{Computed: true}},
			},
			"status": dsschema.SingleNestedAttribute{
				Computed: true,
				Attributes: map[string]dsschema.Attribute{
					"status": dsschema.StringAttribute{Computed: true},
					"reason": dsschema.StringAttribute{Computed: true},
				},
			},
		},
	}
}

func (d *sshKeyDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.pd = providerData(req.ProviderData, &resp.Diagnostics)
}

func (d *sshKeyDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	if d.pd == nil {
		return
	}
	var cfg sshKeyDataSourceModel
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("name"), &cfg.Name)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("project"), &cfg.Project)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("workspace"), &cfg.Workspace)...)
	if resp.Diagnostics.HasError() {
		return
	}
	project, workspace, name := cfg.Project.ValueString(), stringOr(cfg.Workspace, ""), cfg.Name.ValueString()
	var keys interface {
		Get(context.Context, string, gpupaas.GetOptions) (*apiv1.SshKey, error)
	}
	if workspace == "" {
		keys = d.pd.Clientset.V1alpha1().SshKeys(project)
	} else {
		keys = d.pd.Clientset.V1alpha1().Workspaces(project).SshKeys(workspace)
	}
	srv, err := keys.Get(ctx, name, gpupaas.GetOptions{})
	if err != nil {
		handleAPIError(&resp.Diagnostics, err, "Read SSH key", nil)
		return
	}
	cfg.ID = types.StringValue(scopedID(project, workspace, name))
	cfg.Metadata = metadataFromSDK(srv.Metadata)
	cfg.Spec = sshKeyDataSpecModel{PublicKey: nullableString(srv.Spec.PublicKey)}
	cfg.Status = sshKeyStatusModel{Status: nullableString(srv.Status.Status), Reason: nullableString(srv.Status.Reason)}
	resp.Diagnostics.Append(resp.State.Set(ctx, &cfg)...)
}
