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
	_ datasource.DataSource              = (*baremetalMachineDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*baremetalMachineDataSource)(nil)
)

// NewBaremetalMachineDataSource constructs the gpupaas_baremetal_machine data source.
func NewBaremetalMachineDataSource() datasource.DataSource { return &baremetalMachineDataSource{} }

type baremetalMachineDataSource struct{ pd *client.ProviderData }

type baremetalMachineDataSourceModel struct {
	ID         types.String           `tfsdk:"id"`
	Name       types.String           `tfsdk:"name"`
	Project    types.String           `tfsdk:"project"`
	APIVersion types.String           `tfsdk:"api_version"`
	Kind       types.String           `tfsdk:"kind"`
	Metadata   MetadataModel          `tfsdk:"metadata"`
	Spec       baremetalMachineSpec   `tfsdk:"spec"`
	Status     baremetalMachineStatus `tfsdk:"status"`
}

func (d *baremetalMachineDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_baremetal_machine"
}

func (d *baremetalMachineDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = dsschema.Schema{
		MarkdownDescription: "Lookup a GPU PaaS baremetal machine by name within a project.",
		Attributes: map[string]dsschema.Attribute{
			"id":          dsschema.StringAttribute{Computed: true},
			"name":        dsschema.StringAttribute{Required: true},
			"project":     dsschema.StringAttribute{Required: true},
			"api_version": dsschema.StringAttribute{Computed: true},
			"kind":        dsschema.StringAttribute{Computed: true},
			"metadata":    MetadataDataSourceAttribute(),
			"spec":        baremetalMachineSpecDataSourceAttribute(),
			"status":      baremetalMachineStatusDataSourceAttribute(),
		},
	}
}

func (d *baremetalMachineDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.pd = providerData(req.ProviderData, &resp.Diagnostics)
}

func (d *baremetalMachineDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	if d.pd == nil {
		resp.Diagnostics.AddError("Provider not configured", "Provider data is nil")
		return
	}
	var cfg baremetalMachineDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	project := cfg.Project.ValueString()
	out, err := d.pd.Clientset.V1alpha1().BaremetalMachines(project).Get(ctx, cfg.Name.ValueString(), gpupaas.GetOptions{})
	if err != nil {
		handleAPIError(&resp.Diagnostics, err, "Read baremetal machine", nil)
		return
	}
	model := baremetalMachineSDKToModel(ctx, out, project, &resp.Diagnostics)
	cfg.ID = model.ID
	cfg.APIVersion = model.APIVersion
	cfg.Kind = model.Kind
	cfg.Metadata = model.Metadata
	cfg.Spec = model.Spec
	cfg.Status = model.Status
	resp.Diagnostics.Append(resp.State.Set(ctx, &cfg)...)
}

// baremetalMachineSpecDataSourceAttribute mirrors the resource spec as a
// computed-only datasource attribute set.
func baremetalMachineSpecDataSourceAttribute() dsschema.SingleNestedAttribute {
	return dsschema.SingleNestedAttribute{
		Computed: true,
		Attributes: map[string]dsschema.Attribute{
			"architecture":               dsschema.StringAttribute{Computed: true},
			"automated_cleaning_mode":    dsschema.StringAttribute{Computed: true},
			"baremetal_provisioner_name": dsschema.StringAttribute{Computed: true},
			"boot_mode":                  dsschema.StringAttribute{Computed: true},
			"datacenter":                 dsschema.StringAttribute{Computed: true},
			"device_id":                  dsschema.StringAttribute{Computed: true},
			"hostname":                   dsschema.StringAttribute{Computed: true},
			"image":                      baremetalImageDataSourceAttribute(),
			"mac_address":                dsschema.StringAttribute{Computed: true},
			"online":                     dsschema.BoolAttribute{Computed: true},
			"raid":                       baremetalRaidDataSourceAttribute(),
			"root_device_hints":          baremetalRootDeviceHintsDataSourceAttribute(),
			"ssh_key":                    dsschema.StringAttribute{Computed: true},
			"system_user_data":           dsschema.StringAttribute{Computed: true},
			"user_data":                  dsschema.StringAttribute{Computed: true},
		},
	}
}

func baremetalImageDataSourceAttribute() dsschema.SingleNestedAttribute {
	return dsschema.SingleNestedAttribute{
		Computed: true,
		Attributes: map[string]dsschema.Attribute{
			"checksum":      dsschema.StringAttribute{Computed: true},
			"checksum_type": dsschema.StringAttribute{Computed: true},
			"format":        dsschema.StringAttribute{Computed: true},
			"url":           dsschema.StringAttribute{Computed: true},
		},
	}
}

func baremetalRootDeviceHintsDataSourceAttribute() dsschema.SingleNestedAttribute {
	return dsschema.SingleNestedAttribute{
		Computed:   true,
		Attributes: baremetalRootDeviceHintsDataSourceAttributes(),
	}
}

func baremetalRootDeviceHintsDataSourceAttributes() map[string]dsschema.Attribute {
	return map[string]dsschema.Attribute{
		"device_name":          dsschema.StringAttribute{Computed: true},
		"hctl":                 dsschema.StringAttribute{Computed: true},
		"min_size_gigabytes":   dsschema.Int64Attribute{Computed: true},
		"model":                dsschema.StringAttribute{Computed: true},
		"rotational":           dsschema.BoolAttribute{Computed: true},
		"serial_number":        dsschema.StringAttribute{Computed: true},
		"vendor":               dsschema.StringAttribute{Computed: true},
		"wwn":                  dsschema.StringAttribute{Computed: true},
		"wwn_vendor_extension": dsschema.StringAttribute{Computed: true},
		"wwn_with_extension":   dsschema.StringAttribute{Computed: true},
	}
}

func baremetalRaidDataSourceAttribute() dsschema.SingleNestedAttribute {
	return dsschema.SingleNestedAttribute{
		Computed: true,
		Attributes: map[string]dsschema.Attribute{
			"hardware_raid_volumes": dsschema.ListNestedAttribute{
				Computed: true,
				NestedObject: dsschema.NestedAttributeObject{
					Attributes: map[string]dsschema.Attribute{
						"controller":               dsschema.StringAttribute{Computed: true},
						"level":                    dsschema.StringAttribute{Computed: true},
						"name":                     dsschema.StringAttribute{Computed: true},
						"number_of_physical_disks": dsschema.Int64Attribute{Computed: true},
						"physical_disks":           dsschema.ListAttribute{Computed: true, ElementType: types.StringType},
						"rotational":               dsschema.BoolAttribute{Computed: true},
						"size_gibibytes":           dsschema.Int64Attribute{Computed: true},
					},
				},
			},
			"software_raid_volumes": dsschema.ListNestedAttribute{
				Computed: true,
				NestedObject: dsschema.NestedAttributeObject{
					Attributes: map[string]dsschema.Attribute{
						"level":          dsschema.StringAttribute{Computed: true},
						"size_gibibytes": dsschema.Int64Attribute{Computed: true},
						"physical_disks": dsschema.ListNestedAttribute{
							Computed: true,
							NestedObject: dsschema.NestedAttributeObject{
								Attributes: baremetalRootDeviceHintsDataSourceAttributes(),
							},
						},
					},
				},
			},
		},
	}
}

func baremetalMachineStatusDataSourceAttribute() dsschema.SingleNestedAttribute {
	return dsschema.SingleNestedAttribute{
		Computed: true,
		Attributes: map[string]dsschema.Attribute{
			"conditions": dsschema.ListNestedAttribute{
				Computed: true,
				NestedObject: dsschema.NestedAttributeObject{
					Attributes: map[string]dsschema.Attribute{
						"type":         dsschema.StringAttribute{Computed: true},
						"status":       dsschema.StringAttribute{Computed: true},
						"reason":       dsschema.StringAttribute{Computed: true},
						"last_updated": dsschema.StringAttribute{Computed: true},
					},
				},
			},
		},
	}
}
