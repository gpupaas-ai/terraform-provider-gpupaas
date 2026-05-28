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
	_ datasource.DataSource              = (*mksClusterDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*mksClusterDataSource)(nil)
)

// NewMKSClusterDataSource constructs the gpupaas_mks_cluster data source.
func NewMKSClusterDataSource() datasource.DataSource { return &mksClusterDataSource{} }

type mksClusterDataSource struct{ pd *client.ProviderData }

type mksClusterDataSourceModel struct {
	ID         types.String          `tfsdk:"id"`
	Name       types.String          `tfsdk:"name"`
	Project    types.String          `tfsdk:"project"`
	APIVersion types.String          `tfsdk:"api_version"`
	Kind       types.String          `tfsdk:"kind"`
	Metadata   MetadataModel         `tfsdk:"metadata"`
	Spec       mksClusterSpecModel   `tfsdk:"spec"`
	Status     mksClusterStatusModel `tfsdk:"status"`
}

func (d *mksClusterDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_mks_cluster"
}

func (d *mksClusterDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = dsschema.Schema{
		MarkdownDescription: "Lookup a GPU PaaS managed Kubernetes cluster by name within a project.",
		Attributes: map[string]dsschema.Attribute{
			"id":          dsschema.StringAttribute{Computed: true},
			"name":        dsschema.StringAttribute{Required: true},
			"project":     dsschema.StringAttribute{Required: true},
			"api_version": dsschema.StringAttribute{Computed: true},
			"kind":        dsschema.StringAttribute{Computed: true},
			"metadata":    MetadataDataSourceAttribute(),
			"spec":        mksClusterSpecDataSourceAttribute(),
			"status":      mksClusterStatusDataSourceAttribute(),
		},
	}
}

func (d *mksClusterDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.pd = providerData(req.ProviderData, &resp.Diagnostics)
}

func (d *mksClusterDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	if d.pd == nil {
		resp.Diagnostics.AddError("Provider not configured", "Provider data is nil")
		return
	}
	var cfg mksClusterDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	project := cfg.Project.ValueString()
	out, err := d.pd.Clientset.V1alpha1().MKSClusters(project).Get(ctx, cfg.Name.ValueString(), gpupaas.GetOptions{})
	if err != nil {
		handleAPIError(&resp.Diagnostics, err, "Read MKS cluster", nil)
		return
	}
	model := mksClusterSDKToModel(out, project)
	cfg.ID = model.ID
	cfg.APIVersion = model.APIVersion
	cfg.Kind = model.Kind
	cfg.Metadata = model.Metadata
	cfg.Spec = model.Spec
	cfg.Status = model.Status
	resp.Diagnostics.Append(resp.State.Set(ctx, &cfg)...)
}

// mksClusterSpecDataSourceAttribute mirrors the resource spec as a
// computed-only datasource attribute set.
func mksClusterSpecDataSourceAttribute() dsschema.SingleNestedAttribute {
	return dsschema.SingleNestedAttribute{
		Computed: true,
		Attributes: map[string]dsschema.Attribute{
			"kubernetes_version":      dsschema.StringAttribute{Computed: true},
			"platform_version":        dsschema.StringAttribute{Computed: true},
			"cni":                     dsschema.StringAttribute{Computed: true},
			"cni_version":             dsschema.StringAttribute{Computed: true},
			"os":                      dsschema.StringAttribute{Computed: true},
			"ha_enabled":              dsschema.BoolAttribute{Computed: true},
			"dedicated_control_plane": dsschema.BoolAttribute{Computed: true},
			"location":                dsschema.StringAttribute{Computed: true},
			"blueprint":               mksBlueprintDataSourceAttribute(),
			"networking":              mksNetworkingDataSourceAttribute(),
			"proxy":                   mksProxyDataSourceAttribute(),
			"storage":                 mksStorageDataSourceAttribute(),
			"tags":                    dsschema.MapAttribute{Computed: true, ElementType: types.StringType},
			"control_plane_node_group": dsschema.SingleNestedAttribute{
				Computed:   true,
				Attributes: mksNodeGroupDataSourceAttributes(),
			},
			"worker_node_groups": dsschema.ListNestedAttribute{
				Computed:     true,
				NestedObject: dsschema.NestedAttributeObject{Attributes: mksNodeGroupDataSourceAttributes()},
			},
			"nodes": dsschema.ListNestedAttribute{
				Computed:     true,
				NestedObject: dsschema.NestedAttributeObject{Attributes: mksNodeSpecDataSourceAttributes()},
			},
		},
	}
}

func mksBlueprintDataSourceAttribute() dsschema.SingleNestedAttribute {
	return dsschema.SingleNestedAttribute{
		Computed: true,
		Attributes: map[string]dsschema.Attribute{
			"name":    dsschema.StringAttribute{Computed: true},
			"version": dsschema.StringAttribute{Computed: true},
		},
	}
}

func mksNetworkingDataSourceAttribute() dsschema.SingleNestedAttribute {
	return dsschema.SingleNestedAttribute{
		Computed: true,
		Attributes: map[string]dsschema.Attribute{
			"vpc":             dsschema.StringAttribute{Computed: true},
			"subnet":          dsschema.StringAttribute{Computed: true},
			"pod_cidr":        dsschema.StringAttribute{Computed: true},
			"service_cidr":    dsschema.StringAttribute{Computed: true},
			"ip_family":       dsschema.StringAttribute{Computed: true},
			"pod_cidr_v6":     dsschema.StringAttribute{Computed: true},
			"service_cidr_v6": dsschema.StringAttribute{Computed: true},
			"security_groups": dsschema.ListAttribute{Computed: true, ElementType: types.StringType},
		},
	}
}

func mksProxyDataSourceAttribute() dsschema.SingleNestedAttribute {
	return dsschema.SingleNestedAttribute{
		Computed: true,
		Attributes: map[string]dsschema.Attribute{
			"http_proxy":    dsschema.StringAttribute{Computed: true},
			"https_proxy":   dsschema.StringAttribute{Computed: true},
			"no_proxy":      dsschema.StringAttribute{Computed: true},
			"proxy_root_ca": dsschema.StringAttribute{Computed: true},
			"tls_terminate": dsschema.BoolAttribute{Computed: true},
		},
	}
}

func mksStorageBackendDataSourceAttribute() dsschema.SingleNestedAttribute {
	return dsschema.SingleNestedAttribute{
		Computed: true,
		Attributes: map[string]dsschema.Attribute{
			"type":           dsschema.StringAttribute{Computed: true},
			"access_mode":    dsschema.StringAttribute{Computed: true},
			"reclaim_policy": dsschema.StringAttribute{Computed: true},
			"config":         dsschema.MapAttribute{Computed: true, ElementType: types.StringType},
		},
	}
}

func mksStorageDataSourceAttribute() dsschema.SingleNestedAttribute {
	return dsschema.SingleNestedAttribute{
		Computed: true,
		Attributes: map[string]dsschema.Attribute{
			"block":                 mksStorageBackendDataSourceAttribute(),
			"shared_fs":             mksStorageBackendDataSourceAttribute(),
			"object":                mksStorageBackendDataSourceAttribute(),
			"high_speed":            mksStorageBackendDataSourceAttribute(),
			"default_storage_class": dsschema.StringAttribute{Computed: true},
		},
	}
}

func mksKubeletConfigDataSourceAttribute() dsschema.SingleNestedAttribute {
	return dsschema.SingleNestedAttribute{
		Computed: true,
		Attributes: map[string]dsschema.Attribute{
			"key_value": dsschema.MapAttribute{Computed: true, ElementType: types.StringType},
			"yaml":      dsschema.StringAttribute{Computed: true},
		},
	}
}

func mksNodeGroupDataSourceAttributes() map[string]dsschema.Attribute {
	return map[string]dsschema.Attribute{
		"id":               dsschema.StringAttribute{Computed: true},
		"sku":              dsschema.StringAttribute{Computed: true},
		"scaling_mode":     dsschema.StringAttribute{Computed: true},
		"node_count":       dsschema.Int64Attribute{Computed: true},
		"min_nodes":        dsschema.Int64Attribute{Computed: true},
		"max_nodes":        dsschema.Int64Attribute{Computed: true},
		"desired_nodes":    dsschema.Int64Attribute{Computed: true},
		"public_ip":        dsschema.BoolAttribute{Computed: true},
		"ssh_key":          dsschema.StringAttribute{Computed: true},
		"user_data":        dsschema.StringAttribute{Computed: true},
		"node_labels":      dsschema.MapAttribute{Computed: true, ElementType: types.StringType},
		"node_annotations": dsschema.MapAttribute{Computed: true, ElementType: types.StringType},
		"kubelet_config":   mksKubeletConfigDataSourceAttribute(),
	}
}

func mksNodeSpecDataSourceAttributes() map[string]dsschema.Attribute {
	return map[string]dsschema.Attribute{
		"hostname":         dsschema.StringAttribute{Computed: true},
		"inventory_source": dsschema.StringAttribute{Computed: true},
		"roles":            dsschema.ListAttribute{Computed: true, ElementType: types.StringType},
		"ssh_key":          dsschema.StringAttribute{Computed: true},
		"ssh_user_name":    dsschema.StringAttribute{Computed: true},
		"ssh_port":         dsschema.Int64Attribute{Computed: true},
		"private_ip":       dsschema.StringAttribute{Computed: true},
		"arch":             dsschema.StringAttribute{Computed: true},
		"operating_system": dsschema.StringAttribute{Computed: true},
		"interface":        dsschema.StringAttribute{Computed: true},
		"node_labels":      dsschema.MapAttribute{Computed: true, ElementType: types.StringType},
		"node_annotations": dsschema.MapAttribute{Computed: true, ElementType: types.StringType},
		"node_taints":      dsschema.MapAttribute{Computed: true, ElementType: types.StringType},
		"user_data":        dsschema.StringAttribute{Computed: true},
		"kubelet_config":   mksKubeletConfigDataSourceAttribute(),
		"node_pool":        dsschema.StringAttribute{Computed: true},
		"sku":              dsschema.StringAttribute{Computed: true},
		"public_ip":        dsschema.BoolAttribute{Computed: true},
	}
}

func mksClusterStatusDataSourceAttribute() dsschema.SingleNestedAttribute {
	return dsschema.SingleNestedAttribute{
		Computed: true,
		Attributes: map[string]dsschema.Attribute{
			"condition":        dsschema.StringAttribute{Computed: true},
			"condition_reason": dsschema.StringAttribute{Computed: true},
			"action":           dsschema.StringAttribute{Computed: true},
			"output": dsschema.SingleNestedAttribute{
				Computed: true,
				Attributes: map[string]dsschema.Attribute{
					"api_server_endpoint": dsschema.StringAttribute{Computed: true},
					"cluster_id_edgesrv":  dsschema.StringAttribute{Computed: true},
				},
			},
		},
	}
}
