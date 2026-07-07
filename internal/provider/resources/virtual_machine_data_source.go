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
	_ datasource.DataSource              = (*virtualMachineDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*virtualMachineDataSource)(nil)
)

// NewVirtualMachineDataSource constructs the gpupaas_virtual_machine data source.
func NewVirtualMachineDataSource() datasource.DataSource { return &virtualMachineDataSource{} }

type virtualMachineDataSource struct{ pd *client.ProviderData }

type virtualMachineDataSourceModel struct {
	ID         types.String         `tfsdk:"id"`
	Name       types.String         `tfsdk:"name"`
	Project    types.String         `tfsdk:"project"`
	Workspace  types.String         `tfsdk:"workspace"`
	APIVersion types.String         `tfsdk:"api_version"`
	Kind       types.String         `tfsdk:"kind"`
	Metadata   MetadataModel        `tfsdk:"metadata"`
	Spec       virtualMachineSpec   `tfsdk:"spec"`
	Status     virtualMachineStatus `tfsdk:"status"`
}

func (d *virtualMachineDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_virtual_machine"
}

func (d *virtualMachineDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = dsschema.Schema{
		MarkdownDescription: "Lookup a GPU PaaS virtual machine by name within a project, optionally scoped to a workspace.",
		Attributes: map[string]dsschema.Attribute{
			"id":          dsschema.StringAttribute{Computed: true},
			"name":        dsschema.StringAttribute{Required: true},
			"project":     dsschema.StringAttribute{Required: true},
			"workspace":   dsschema.StringAttribute{Optional: true},
			"api_version": dsschema.StringAttribute{Computed: true},
			"kind":        dsschema.StringAttribute{Computed: true},
			"metadata":    MetadataDataSourceAttribute(),
			"spec":        virtualMachineSpecDataSourceAttribute(),
			"status":      virtualMachineStatusDataSourceAttribute(),
		},
	}
}

func (d *virtualMachineDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.pd = providerData(req.ProviderData, &resp.Diagnostics)
}

func (d *virtualMachineDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	if d.pd == nil {
		resp.Diagnostics.AddError("Provider not configured", "Provider data is nil")
		return
	}
	var cfg virtualMachineDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	project := cfg.Project.ValueString()
	workspace := stringOr(cfg.Workspace, "")
	var client = d.pd.Clientset.V1alpha1().VirtualMachines(project)
	if workspace != "" {
		client = d.pd.Clientset.V1alpha1().Workspaces(project).VirtualMachines(workspace)
	}
	out, err := client.Get(ctx, cfg.Name.ValueString(), gpupaas.GetOptions{})
	if err != nil {
		handleAPIError(&resp.Diagnostics, err, "Read virtual machine", nil)
		return
	}
	model := virtualMachineSDKToModel(ctx, out, project, workspace, &resp.Diagnostics)
	cfg.ID = model.ID
	cfg.APIVersion = model.APIVersion
	cfg.Kind = model.Kind
	cfg.Metadata = model.Metadata
	cfg.Spec = model.Spec
	cfg.Status = model.Status
	resp.Diagnostics.Append(resp.State.Set(ctx, &cfg)...)
}

// virtualMachineSpecDataSourceAttribute mirrors the resource spec as a
// computed-only datasource attribute set.
func virtualMachineSpecDataSourceAttribute() dsschema.SingleNestedAttribute {
	return dsschema.SingleNestedAttribute{
		Computed: true,
		Attributes: map[string]dsschema.Attribute{
			"virtual_machine": dsschema.SingleNestedAttribute{
				Computed: true,
				Attributes: map[string]dsschema.Attribute{
					"name":           dsschema.StringAttribute{Computed: true},
					"system_catalog": dsschema.BoolAttribute{Computed: true},
				},
			},
			"vm_id":                   dsschema.StringAttribute{Computed: true, MarkdownDescription: "Inventory device ID of the virtual machine."},
			"type":                    dsschema.StringAttribute{Computed: true},
			"name":                    dsschema.StringAttribute{Computed: true},
			"cpu_count":               dsschema.StringAttribute{Computed: true},
			"memory":                  dsschema.StringAttribute{Computed: true},
			"security_group":          dsschema.StringAttribute{Computed: true},
			"ssh_key":                 dsschema.StringAttribute{Computed: true},
			"vpc":                     dsschema.StringAttribute{Computed: true},
			"subnet":                  dsschema.StringAttribute{Computed: true},
			"assign_public_ip":        dsschema.BoolAttribute{Computed: true},
			"sharing":                 sharingDataSourceAttribute(),
			"datacenter":              dsschema.StringAttribute{Computed: true},
			"guest_password":          dsschema.StringAttribute{Computed: true, Sensitive: true},
			"dns_servers":             dsschema.SetAttribute{Computed: true, ElementType: types.StringType},
			"user_data":               dsschema.StringAttribute{Computed: true},
			"timezone":                dsschema.StringAttribute{Computed: true},
			"shared_storage":          dsschema.StringAttribute{Computed: true},
			"block_storage_type":      dsschema.StringAttribute{Computed: true},
			"image":                   dsschema.StringAttribute{Computed: true},
			"boot_disk_size":          dsschema.Int64Attribute{Computed: true},
			"create_additional_block": dsschema.BoolAttribute{Computed: true},
			"additional_block_size":   dsschema.Int64Attribute{Computed: true},
		},
	}
}

func virtualMachineStatusDataSourceAttribute() dsschema.SingleNestedAttribute {
	return dsschema.SingleNestedAttribute{
		Computed: true,
		Attributes: map[string]dsschema.Attribute{
			"status":            dsschema.StringAttribute{Computed: true},
			"reason":            dsschema.StringAttribute{Computed: true},
			"action":            dsschema.StringAttribute{Computed: true},
			"provisioned_at":    dsschema.StringAttribute{Computed: true},
			"last_connected_at": dsschema.StringAttribute{Computed: true},
			"output": dsschema.SingleNestedAttribute{
				Computed: true,
				Attributes: map[string]dsschema.Attribute{
					"host_name":       dsschema.StringAttribute{Computed: true},
					"os_name":         dsschema.StringAttribute{Computed: true},
					"private_ip":      dsschema.StringAttribute{Computed: true},
					"public_ip":       dsschema.StringAttribute{Computed: true},
					"server_host":     dsschema.StringAttribute{Computed: true},
					"user_name":       dsschema.StringAttribute{Computed: true},
					"disk_mount_path": dsschema.StringAttribute{Computed: true},
				},
			},
		},
	}
}

// sharingDataSourceAttribute returns a computed-only sharing block for data sources.
func sharingDataSourceAttribute() dsschema.SingleNestedAttribute {
	return dsschema.SingleNestedAttribute{
		Computed: true,
		Attributes: map[string]dsschema.Attribute{
			"share_mode": dsschema.StringAttribute{Computed: true},
			"workspaces": dsschema.SetAttribute{Computed: true, ElementType: types.StringType},
			"projects": dsschema.SetNestedAttribute{
				Computed: true,
				NestedObject: dsschema.NestedAttributeObject{
					Attributes: map[string]dsschema.Attribute{
						"name":       dsschema.StringAttribute{Computed: true},
						"workspaces": dsschema.SetAttribute{Computed: true, ElementType: types.StringType},
					},
				},
			},
		},
	}
}
