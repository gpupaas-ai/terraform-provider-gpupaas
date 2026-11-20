package resources

import (
	"context"

	gpupaas "github.com/gpupaas-ai/gpupaas-go"
	apiv1 "github.com/gpupaas-ai/gpupaas-go/apis/v1alpha1"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	dsschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/gpupaas-ai/terraform-provider-gpupaas/internal/client"
)

var (
	_ datasource.DataSource              = (*mksClusterDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*mksClusterDataSource)(nil)
	_ datasource.DataSource              = (*mksClusterProfileDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*mksClusterProfileDataSource)(nil)
)

// NewMKSClusterDataSource constructs the gpupaas_kubernetes_cluster data source.
func NewMKSClusterDataSource() datasource.DataSource { return &mksClusterDataSource{} }

type mksClusterDataSource struct{ pd *client.ProviderData }

type mksClusterDataSourceModel struct {
	ID        types.String   `tfsdk:"id"`
	Name      types.String   `tfsdk:"name"`
	Project   types.String   `tfsdk:"project"`
	Workspace types.String   `tfsdk:"workspace"`
	Metadata  MetadataModel  `tfsdk:"metadata"`
	Spec      mksSpecModel   `tfsdk:"spec"`
	Status    mksStatusModel `tfsdk:"status"`
}

func (d *mksClusterDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_kubernetes_cluster"
}

func mksDSGroupAttrs(controlPlane bool) map[string]dsschema.Attribute {
	attrs := map[string]dsschema.Attribute{
		"name":         dsschema.StringAttribute{Computed: true},
		"datacenter":   dsschema.StringAttribute{Computed: true},
		"os_image":     dsschema.StringAttribute{Computed: true},
		"sku_name":     dsschema.StringAttribute{Computed: true},
		"sku_type":     dsschema.StringAttribute{Computed: true},
		"ssh_key":      dsschema.StringAttribute{Computed: true},
		"node_count":   dsschema.Int64Attribute{Computed: true},
		"scaling_mode": dsschema.StringAttribute{Computed: true},
	}
	attrs["network"] = dsschema.SingleNestedAttribute{Computed: true, Attributes: map[string]dsschema.Attribute{
		"vpc": dsschema.StringAttribute{Computed: true}, "subnet": dsschema.StringAttribute{Computed: true}, "security_group": dsschema.StringAttribute{Computed: true},
	}}
	attrs["boot_disk_size_gb"] = dsschema.Int64Attribute{Computed: true}
	attrs["assign_public_ip"] = dsschema.BoolAttribute{Computed: true}
	attrs["user_password"] = dsschema.StringAttribute{Computed: true, Sensitive: true, MarkdownDescription: "Always null: the platform never returns a node password."}
	if controlPlane {
		attrs["compute_type"] = dsschema.StringAttribute{Computed: true}
	} else {
		attrs["min_nodes"] = dsschema.Int64Attribute{Computed: true}
		attrs["max_nodes"] = dsschema.Int64Attribute{Computed: true}
		attrs["labels"] = dsschema.MapAttribute{Computed: true, ElementType: types.StringType}
		attrs["annotations"] = dsschema.MapAttribute{Computed: true, ElementType: types.StringType}
		attrs["taints"] = dsschema.ListNestedAttribute{Computed: true, NestedObject: dsschema.NestedAttributeObject{Attributes: map[string]dsschema.Attribute{
			"key": dsschema.StringAttribute{Computed: true}, "value": dsschema.StringAttribute{Computed: true}, "effect": dsschema.StringAttribute{Computed: true},
		}}}
	}
	return attrs
}

func (d *mksClusterDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = dsschema.Schema{
		MarkdownDescription: "Look up a first-class managed Kubernetes cluster by name within a project, optionally scoped to a workspace.",
		Attributes: map[string]dsschema.Attribute{
			"id":        dsschema.StringAttribute{Computed: true},
			"name":      dsschema.StringAttribute{Required: true},
			"project":   dsschema.StringAttribute{Required: true},
			"workspace": dsschema.StringAttribute{Optional: true, MarkdownDescription: "Workspace that owns the cluster; omit for a project-scoped cluster."},
			"metadata":  MetadataDataSourceAttribute(),
			"spec": dsschema.SingleNestedAttribute{
				Computed: true,
				Attributes: map[string]dsschema.Attribute{
					"profile_name":       dsschema.StringAttribute{Computed: true},
					"kubernetes_version": dsschema.StringAttribute{Computed: true},
					"location":           dsschema.StringAttribute{Computed: true},
					"blueprint": dsschema.SingleNestedAttribute{Computed: true, Attributes: map[string]dsschema.Attribute{
						"name": dsschema.StringAttribute{Computed: true}, "version": dsschema.StringAttribute{Computed: true},
					}},
					"networking": dsschema.SingleNestedAttribute{Computed: true, Attributes: map[string]dsschema.Attribute{
						"vpc": dsschema.StringAttribute{Computed: true}, "subnet": dsschema.StringAttribute{Computed: true},
						"pod_subnet": dsschema.StringAttribute{Computed: true}, "service_subnet": dsschema.StringAttribute{Computed: true},
						"cni_name": dsschema.StringAttribute{Computed: true}, "cni_version": dsschema.StringAttribute{Computed: true},
					}},
					"control_plane": dsschema.SingleNestedAttribute{Computed: true, Attributes: mksDSGroupAttrs(true)},
					"worker_pools":  dsschema.ListNestedAttribute{Computed: true, NestedObject: dsschema.NestedAttributeObject{Attributes: mksDSGroupAttrs(false)}},
					"tags":          dsschema.MapAttribute{Computed: true, ElementType: types.StringType},
					"contract_term": dsschema.StringAttribute{Computed: true},
				},
			},
			"status": dsschema.SingleNestedAttribute{
				Computed: true,
				Attributes: map[string]dsschema.Attribute{
					"phase":                         dsschema.StringAttribute{Computed: true},
					"status":                        dsschema.StringAttribute{Computed: true},
					"reason":                        dsschema.StringAttribute{Computed: true},
					"action":                        dsschema.StringAttribute{Computed: true},
					"cluster_id":                    dsschema.StringAttribute{Computed: true},
					"kubernetes_version":            dsschema.StringAttribute{Computed: true},
					"node_count":                    dsschema.Int64Attribute{Computed: true},
					"available_kubernetes_versions": dsschema.ListAttribute{Computed: true, ElementType: types.StringType},
					"nodes": dsschema.ListNestedAttribute{Computed: true, NestedObject: dsschema.NestedAttributeObject{Attributes: map[string]dsschema.Attribute{
						"hostname": dsschema.StringAttribute{Computed: true}, "node_pool": dsschema.StringAttribute{Computed: true}, "kubernetes_version": dsschema.StringAttribute{Computed: true},
						"private_ip": dsschema.StringAttribute{Computed: true}, "roles": dsschema.ListAttribute{Computed: true, ElementType: types.StringType},
						"operating_system": dsschema.StringAttribute{Computed: true}, "arch": dsschema.StringAttribute{Computed: true}, "status": dsschema.StringAttribute{Computed: true},
					}}},
				},
			},
		},
	}
}

func (d *mksClusterDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.pd = providerData(req.ProviderData, &resp.Diagnostics)
}

func (d *mksClusterDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	if d.pd == nil {
		return
	}
	var cfg mksClusterDataSourceModel
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("name"), &cfg.Name)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("project"), &cfg.Project)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("workspace"), &cfg.Workspace)...)
	if resp.Diagnostics.HasError() {
		return
	}
	project, workspace, name := cfg.Project.ValueString(), stringOr(cfg.Workspace, ""), cfg.Name.ValueString()
	var clusters interface {
		Get(context.Context, string, gpupaas.GetOptions) (*apiv1.MKSCluster, error)
	}
	if workspace == "" {
		clusters = d.pd.Clientset.V1alpha1().MKSClusters(project)
	} else {
		clusters = d.pd.Clientset.V1alpha1().Workspaces(project).MKSClusters(workspace)
	}
	srv, err := clusters.Get(ctx, name, gpupaas.GetOptions{})
	if err != nil {
		handleAPIError(&resp.Diagnostics, err, "Read mks cluster", nil)
		return
	}
	nodes, _ := d.pd.Clientset.V1alpha1().MKSNodes(project).ListForCluster(ctx, name)
	// An empty spec marks the read as an import, so every field takes the server value.
	m := mksClusterModelFromServer(mksResourceModel{Metadata: MetadataModel{Name: cfg.Name, Project: cfg.Project, Workspace: cfg.Workspace}}, srv, nodes, false)
	cfg.ID = types.StringValue(mksClusterID(project, workspace, name))
	cfg.Metadata = m.Metadata
	cfg.Spec = m.Spec
	cfg.Status = m.Status
	resp.Diagnostics.Append(resp.State.Set(ctx, &cfg)...)
}

// --- cluster profile -------------------------------------------------------

// NewMKSClusterProfileDataSource constructs the gpupaas_kubernetes_sku data source.
func NewMKSClusterProfileDataSource() datasource.DataSource { return &mksClusterProfileDataSource{} }

type mksClusterProfileDataSource struct{ pd *client.ProviderData }

type mksProfileGroupModel struct {
	ComputeTypes   types.List   `tfsdk:"compute_types"`
	DefaultImage   types.String `tfsdk:"default_image"`
	DefaultSKUName types.String `tfsdk:"default_sku_name"`
	Images         types.List   `tfsdk:"images"`
	SKUNames       types.List   `tfsdk:"sku_names"`
	SKUTypes       types.List   `tfsdk:"sku_types"`
}

type mksProfileCNIModel struct {
	Name    types.String `tfsdk:"name"`
	Version types.String `tfsdk:"version"`
}

type mksClusterProfileDataSourceModel struct {
	ID                          types.String          `tfsdk:"id"`
	Name                        types.String          `tfsdk:"name"`
	Project                     types.String          `tfsdk:"project"`
	SupportedKubernetesVersions types.List            `tfsdk:"supported_kubernetes_versions"`
	DefaultKubernetesVersion    types.String          `tfsdk:"default_kubernetes_version"`
	Datacenters                 types.List            `tfsdk:"datacenters"`
	SupportedCNIs               []mksProfileCNIModel  `tfsdk:"supported_cnis"`
	DefaultCNI                  *mksProfileCNIModel   `tfsdk:"default_cni"`
	DefaultBlueprint            *mksBlueprintModel    `tfsdk:"default_blueprint"`
	ControlPlane                *mksProfileGroupModel `tfsdk:"control_plane"`
	Workers                     *mksProfileGroupModel `tfsdk:"workers"`
	DefaultPodSubnet            types.String          `tfsdk:"default_pod_subnet"`
	DefaultServiceSubnet        types.String          `tfsdk:"default_service_subnet"`
	DefaultSSHKey               types.String          `tfsdk:"default_ssh_key"`
	ExposeSSHKeyChoice          types.Bool            `tfsdk:"expose_ssh_key_choice"`
	HasDefaultUserPassword      types.Bool            `tfsdk:"has_default_user_password"`
	ContractTermsMonths         types.List            `tfsdk:"contract_terms_months"`
	Version                     types.String          `tfsdk:"version"`
}

func (d *mksClusterProfileDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_kubernetes_sku"
}

func mksProfileGroupAttrs() map[string]dsschema.Attribute {
	list := func() dsschema.Attribute {
		return dsschema.ListAttribute{Computed: true, ElementType: types.StringType}
	}
	return map[string]dsschema.Attribute{
		"compute_types":    list(),
		"default_image":    dsschema.StringAttribute{Computed: true},
		"default_sku_name": dsschema.StringAttribute{Computed: true},
		"images":           list(),
		"sku_names":        list(),
		"sku_types":        list(),
	}
}

func (d *mksClusterProfileDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	cni := map[string]dsschema.Attribute{"name": dsschema.StringAttribute{Computed: true}, "version": dsschema.StringAttribute{Computed: true}}
	resp.Schema = dsschema.Schema{
		MarkdownDescription: "A cluster profile (Kubernetes SKU): the Kubernetes versions, CNIs, SKUs, images and datacenters a `gpupaas_kubernetes_cluster` created from it may use.",
		Attributes: map[string]dsschema.Attribute{
			"id":                            dsschema.StringAttribute{Computed: true},
			"name":                          dsschema.StringAttribute{Required: true},
			"project":                       dsschema.StringAttribute{Required: true, MarkdownDescription: "Project that holds the profile, usually `system-catalog`."},
			"supported_kubernetes_versions": dsschema.ListAttribute{Computed: true, ElementType: types.StringType},
			"default_kubernetes_version":    dsschema.StringAttribute{Computed: true},
			"datacenters":                   dsschema.ListAttribute{Computed: true, ElementType: types.StringType},
			"supported_cnis":                dsschema.ListNestedAttribute{Computed: true, NestedObject: dsschema.NestedAttributeObject{Attributes: cni}},
			"default_cni":                   dsschema.SingleNestedAttribute{Computed: true, Attributes: cni},
			"default_blueprint": dsschema.SingleNestedAttribute{Computed: true, Attributes: map[string]dsschema.Attribute{
				"name": dsschema.StringAttribute{Computed: true}, "version": dsschema.StringAttribute{Computed: true},
			}},
			"control_plane":             dsschema.SingleNestedAttribute{Computed: true, Attributes: mksProfileGroupAttrs(), MarkdownDescription: "What the profile allows for control-plane nodes."},
			"workers":                   dsschema.SingleNestedAttribute{Computed: true, Attributes: mksProfileGroupAttrs(), MarkdownDescription: "What the profile allows for worker pools."},
			"default_pod_subnet":        dsschema.StringAttribute{Computed: true},
			"default_service_subnet":    dsschema.StringAttribute{Computed: true},
			"default_ssh_key":           dsschema.StringAttribute{Computed: true},
			"expose_ssh_key_choice":     dsschema.BoolAttribute{Computed: true, MarkdownDescription: "When true, a new cluster needs a node password or an SSH key (on the control plane or on a pool), unless the SKU has a default."},
			"has_default_user_password": dsschema.BoolAttribute{Computed: true, MarkdownDescription: "True when the SKU holds a default node password (the platform never shows it)."},
			"contract_terms_months":     dsschema.ListAttribute{Computed: true, ElementType: types.Int64Type, MarkdownDescription: "Billing contract terms in months a cluster may pick."},
			"version":                   dsschema.StringAttribute{Computed: true},
		},
	}
}

func (d *mksClusterProfileDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.pd = providerData(req.ProviderData, &resp.Diagnostics)
}

func profileGroupModel(g *apiv1.MKSGroupProfile) *mksProfileGroupModel {
	if g == nil {
		return nil
	}
	return &mksProfileGroupModel{
		ComputeTypes:   listFromStringSlice(g.ComputeTypes),
		DefaultImage:   nullableString(g.DefaultImage),
		DefaultSKUName: nullableString(g.DefaultSKUName),
		Images:         listFromStringSlice(g.Images),
		SKUNames:       listFromStringSlice(g.SKUNames),
		SKUTypes:       listFromStringSlice(g.SKUTypes),
	}
}

func (d *mksClusterProfileDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	if d.pd == nil {
		return
	}
	var cfg mksClusterProfileDataSourceModel
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("name"), &cfg.Name)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("project"), &cfg.Project)...)
	if resp.Diagnostics.HasError() {
		return
	}
	project, name := cfg.Project.ValueString(), cfg.Name.ValueString()
	p, err := d.pd.Clientset.V1alpha1().MKSClusterProfiles(project).Get(ctx, name, gpupaas.GetOptions{})
	if err != nil {
		handleAPIError(&resp.Diagnostics, err, "Read mks cluster profile", nil)
		return
	}
	s := p.Spec
	cfg.ID = types.StringValue(project + "/" + name)
	cfg.SupportedKubernetesVersions = listFromStringSlice(s.SupportedKubernetesVersions)
	cfg.DefaultKubernetesVersion = nullableString(s.DefaultKubernetesVersion)
	cfg.Datacenters = listFromStringSlice(s.Datacenters)
	cfg.SupportedCNIs = nil
	for _, c := range s.SupportedCNIs {
		cfg.SupportedCNIs = append(cfg.SupportedCNIs, mksProfileCNIModel{Name: nullableString(c.Name), Version: nullableString(c.Version)})
	}
	cfg.DefaultCNI = nil
	if s.DefaultCNI != nil {
		cfg.DefaultCNI = &mksProfileCNIModel{Name: nullableString(s.DefaultCNI.Name), Version: nullableString(s.DefaultCNI.Version)}
	}
	cfg.DefaultBlueprint = nil
	if s.DefaultBlueprint != nil {
		cfg.DefaultBlueprint = &mksBlueprintModel{Name: nullableString(s.DefaultBlueprint.Name), Version: nullableString(s.DefaultBlueprint.Version)}
	}
	cfg.ControlPlane = profileGroupModel(s.ControlPlane)
	cfg.Workers = profileGroupModel(s.Workers)
	cfg.DefaultPodSubnet = nullableString(s.DefaultPodSubnet)
	cfg.DefaultServiceSubnet = nullableString(s.DefaultServiceSubnet)
	cfg.DefaultSSHKey = nullableString(s.DefaultSSHKey)
	cfg.ExposeSSHKeyChoice = types.BoolValue(s.ExposeSSHKeyChoice)
	cfg.HasDefaultUserPassword = types.BoolValue(s.DefaultUserPassword != "")
	terms := make([]int64, len(s.ContractTermsMonths))
	for i, t := range s.ContractTermsMonths {
		terms[i] = int64(t)
	}
	cfg.ContractTermsMonths = int64ListOrNull(terms)
	cfg.Version = nullableString(s.Version)
	resp.Diagnostics.Append(resp.State.Set(ctx, &cfg)...)
}

// int64ListOrNull returns a list of the values, or a null list when there are none.
func int64ListOrNull(values []int64) types.List {
	if len(values) == 0 {
		return types.ListNull(types.Int64Type)
	}
	elems := make([]attr.Value, len(values))
	for i, v := range values {
		elems[i] = types.Int64Value(v)
	}
	l, _ := types.ListValue(types.Int64Type, elems)
	return l
}
