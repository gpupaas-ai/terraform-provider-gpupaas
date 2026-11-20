package resources

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gpupaas-ai/gpupaas-go"
	apiv1 "github.com/gpupaas-ai/gpupaas-go/apis/v1alpha1"
	typed "github.com/gpupaas-ai/gpupaas-go/clientset/typed/v1alpha1"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	dsschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/gpupaas-ai/terraform-provider-gpupaas/internal/client"
)

// Read-only data sources over the discovery, status and audit routes of the managed Kubernetes API:
// the Kubernetes versions a controller offers, what a Kubernetes SKU allows and has left, the status
// document of a cluster and its audit trail.

var (
	_ datasource.DataSourceWithConfigure = (*mksVersionsDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*mksSKUCreateOptionsDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*mksSKUCapacityDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*mksAuditEventsDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*mksClusterStatusDataSource)(nil)
)

func strList() dsschema.ListAttribute {
	return dsschema.ListAttribute{Computed: true, ElementType: types.StringType}
}

// ---------------------------------------------------------------------------
// gpupaas_kubernetes_versions
// ---------------------------------------------------------------------------

// NewMKSVersionsDataSource constructs the gpupaas_kubernetes_versions data source.
func NewMKSVersionsDataSource() datasource.DataSource { return &mksVersionsDataSource{} }

type mksVersionsDataSource struct{ pd *client.ProviderData }

type mksVersionModel struct {
	Minor       types.String `tfsdk:"minor"`
	LatestPatch types.String `tfsdk:"latest_patch"`
	IsDefault   types.Bool   `tfsdk:"is_default"`
}

type mksVersionsModel struct {
	ID             types.String      `tfsdk:"id"`
	Project        types.String      `tfsdk:"project"`
	Minors         types.List        `tfsdk:"minors"`
	DefaultVersion types.String      `tfsdk:"default_version"`
	Versions       []mksVersionModel `tfsdk:"versions"`
}

func (d *mksVersionsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_kubernetes_versions"
}

func (d *mksVersionsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = dsschema.Schema{
		MarkdownDescription: "The Kubernetes versions the controller offers for managed clusters, newest first. A cluster takes a minor such as `1.36` (the platform resolves it to the latest patch). " +
			"This is the controller's catalogue: a Kubernetes SKU can allow fewer minors (for example the catalogue offers `1.37` while a SKU allows `1.34` to `1.36`). " +
			"The minors a particular Kubernetes SKU allows are in `supported_kubernetes_versions` of `gpupaas_kubernetes_sku`.",
		Attributes: map[string]dsschema.Attribute{
			"id":              dsschema.StringAttribute{Computed: true},
			"project":         dsschema.StringAttribute{Required: true, MarkdownDescription: "Project to ask in, usually `system-catalog`."},
			"minors":          dsschema.ListAttribute{Computed: true, ElementType: types.StringType, MarkdownDescription: "The offered minor versions, newest first, for example `1.36`."},
			"default_version": dsschema.StringAttribute{Computed: true, MarkdownDescription: "The minor version the controller preselects."},
			"versions": dsschema.ListNestedAttribute{Computed: true, NestedObject: dsschema.NestedAttributeObject{Attributes: map[string]dsschema.Attribute{
				"minor":        dsschema.StringAttribute{Computed: true},
				"latest_patch": dsschema.StringAttribute{Computed: true, MarkdownDescription: "The version a cluster gets when it asks for the minor, for example `v1.36.4`."},
				"is_default":   dsschema.BoolAttribute{Computed: true},
			}}},
		},
	}
}

func (d *mksVersionsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.pd = providerData(req.ProviderData, &resp.Diagnostics)
}

func (d *mksVersionsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	if d.pd == nil {
		return
	}
	var project types.String
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("project"), &project)...)
	if resp.Diagnostics.HasError() {
		return
	}
	got, err := d.pd.Clientset.V1alpha1().MKSMatchmaker(project.ValueString()).KubernetesVersions(ctx)
	if err != nil {
		handleAPIError(&resp.Diagnostics, err, "Read Kubernetes versions", nil)
		return
	}
	m := mksVersionsModel{ID: types.StringValue(project.ValueString() + "/kubernetes-versions"), Project: project, Minors: listFromStringSlice(got.Minors), DefaultVersion: types.StringNull()}
	for _, v := range got.Versions {
		m.Versions = append(m.Versions, mksVersionModel{Minor: nullableString(v.Minor), LatestPatch: nullableString(v.LatestPatch), IsDefault: types.BoolValue(v.IsDefault)})
		if v.IsDefault {
			m.DefaultVersion = nullableString(v.Minor)
		}
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

// ---------------------------------------------------------------------------
// gpupaas_kubernetes_sku_create_options
// ---------------------------------------------------------------------------

// NewMKSSKUCreateOptionsDataSource constructs the gpupaas_kubernetes_sku_create_options data source.
func NewMKSSKUCreateOptionsDataSource() datasource.DataSource {
	return &mksSKUCreateOptionsDataSource{}
}

type mksSKUCreateOptionsDataSource struct{ pd *client.ProviderData }

type mksOSVersionModel struct {
	Version   types.String `tfsdk:"version"`
	ImageName types.String `tfsdk:"image_name"`
}

type mksOSImageModel struct {
	Name     types.String        `tfsdk:"name"`
	Versions []mksOSVersionModel `tfsdk:"versions"`
}

type mksShapeModel struct {
	CPU         types.Int64  `tfsdk:"cpu"`
	RAMGb       types.Int64  `tfsdk:"ram_gb"`
	StorageGb   types.Int64  `tfsdk:"storage_gb"`
	GPUType     types.String `tfsdk:"gpu_type"`
	GPUCount    types.Int64  `tfsdk:"gpu_count"`
	GPUMemoryGb types.Int64  `tfsdk:"gpu_memory_gb"`
	DisplayText types.String `tfsdk:"display_text"`
}

type mksInstanceTypeModel struct {
	Name         types.String    `tfsdk:"name"`
	Kind         types.String    `tfsdk:"kind"`
	ComputeType  types.String    `tfsdk:"compute_type"`
	DisplayLabel types.String    `tfsdk:"display_label"`
	Images       types.List      `tfsdk:"images"`
	Datacenters  types.List      `tfsdk:"datacenters"`
	Shapes       []mksShapeModel `tfsdk:"shapes"`
}

type mksPoolOptionsModel struct {
	Name          types.String           `tfsdk:"name"`
	DisplayName   types.String           `tfsdk:"display_name"`
	Description   types.String           `tfsdk:"description"`
	SKUKinds      types.List             `tfsdk:"sku_kinds"`
	ComputeTypes  types.List             `tfsdk:"compute_types"`
	Datacenters   types.List             `tfsdk:"datacenters"`
	SKUNames      types.List             `tfsdk:"sku_names"`
	Images        types.List             `tfsdk:"images"`
	OSImages      []mksOSImageModel      `tfsdk:"os_images"`
	InstanceTypes []mksInstanceTypeModel `tfsdk:"instance_types"`
}

type mksSKUCreateOptionsModel struct {
	ID                          types.String         `tfsdk:"id"`
	Name                        types.String         `tfsdk:"name"`
	Project                     types.String         `tfsdk:"project"`
	DataCenterRegions           types.List           `tfsdk:"datacenters"`
	DefaultKubernetesVersion    types.String         `tfsdk:"default_kubernetes_version"`
	SupportedKubernetesVersions types.List           `tfsdk:"supported_kubernetes_versions"`
	EnableOIDC                  types.Bool           `tfsdk:"enable_oidc"`
	ControlPlane                *mksPoolOptionsModel `tfsdk:"control_plane"`
	Workers                     *mksPoolOptionsModel `tfsdk:"workers"`
}

func (d *mksSKUCreateOptionsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_kubernetes_sku_create_options"
}

func mksPoolOptionsAttrs() map[string]dsschema.Attribute {
	return map[string]dsschema.Attribute{
		"name":          dsschema.StringAttribute{Computed: true},
		"display_name":  dsschema.StringAttribute{Computed: true},
		"description":   dsschema.StringAttribute{Computed: true},
		"sku_kinds":     dsschema.ListAttribute{Computed: true, ElementType: types.StringType, MarkdownDescription: "`bmaas` and/or `vmaas`."},
		"compute_types": dsschema.ListAttribute{Computed: true, ElementType: types.StringType, MarkdownDescription: "`cpu`, `gpu` and/or `custom`."},
		"datacenters":   strList(),
		"sku_names":     dsschema.ListAttribute{Computed: true, ElementType: types.StringType, MarkdownDescription: "The compute SKUs a pool may use."},
		"images":        dsschema.ListAttribute{Computed: true, ElementType: types.StringType, MarkdownDescription: "The OS images a pool may use."},
		"os_images": dsschema.ListNestedAttribute{Computed: true, NestedObject: dsschema.NestedAttributeObject{Attributes: map[string]dsschema.Attribute{
			"name": dsschema.StringAttribute{Computed: true, MarkdownDescription: "Operating system, for example `Ubuntu`."},
			"versions": dsschema.ListNestedAttribute{Computed: true, NestedObject: dsschema.NestedAttributeObject{Attributes: map[string]dsschema.Attribute{
				"version":    dsschema.StringAttribute{Computed: true},
				"image_name": dsschema.StringAttribute{Computed: true, MarkdownDescription: "The value of a pool's `os_image`."},
			}}},
		}}},
		"instance_types": dsschema.ListNestedAttribute{Computed: true, NestedObject: dsschema.NestedAttributeObject{Attributes: map[string]dsschema.Attribute{
			"name":          dsschema.StringAttribute{Computed: true, MarkdownDescription: "The compute SKU: the value of a pool's `sku_name`."},
			"kind":          dsschema.StringAttribute{Computed: true},
			"compute_type":  dsschema.StringAttribute{Computed: true},
			"display_label": dsschema.StringAttribute{Computed: true},
			"images":        strList(),
			"datacenters":   strList(),
			"shapes": dsschema.ListNestedAttribute{Computed: true, NestedObject: dsschema.NestedAttributeObject{Attributes: map[string]dsschema.Attribute{
				"cpu":           dsschema.Int64Attribute{Computed: true},
				"ram_gb":        dsschema.Int64Attribute{Computed: true},
				"storage_gb":    dsschema.Int64Attribute{Computed: true},
				"gpu_type":      dsschema.StringAttribute{Computed: true},
				"gpu_count":     dsschema.Int64Attribute{Computed: true},
				"gpu_memory_gb": dsschema.Int64Attribute{Computed: true},
				"display_text":  dsschema.StringAttribute{Computed: true},
			}}},
		}}},
	}
}

func (d *mksSKUCreateOptionsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = dsschema.Schema{
		MarkdownDescription: "What a Kubernetes SKU allows when a cluster is created from it: for the control plane and for the workers the compute SKUs, operating system images, shapes and datacenters, " +
			"plus the datacenters and Kubernetes versions of the SKU. It is what the console offers in its create form. `gpupaas_kubernetes_sku` shows the SKU's own settings.",
		Attributes: map[string]dsschema.Attribute{
			"id":                            dsschema.StringAttribute{Computed: true},
			"name":                          dsschema.StringAttribute{Required: true, MarkdownDescription: "Name of the Kubernetes SKU."},
			"project":                       dsschema.StringAttribute{Required: true, MarkdownDescription: "Project that holds the SKU, usually `system-catalog`."},
			"datacenters":                   strList(),
			"default_kubernetes_version":    dsschema.StringAttribute{Computed: true},
			"supported_kubernetes_versions": strList(),
			"enable_oidc":                   dsschema.BoolAttribute{Computed: true},
			"control_plane":                 dsschema.SingleNestedAttribute{Computed: true, Attributes: mksPoolOptionsAttrs()},
			"workers":                       dsschema.SingleNestedAttribute{Computed: true, Attributes: mksPoolOptionsAttrs()},
		},
	}
}

func (d *mksSKUCreateOptionsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.pd = providerData(req.ProviderData, &resp.Diagnostics)
}

func poolOptionsModel(o *apiv1.MKSPoolOptions) *mksPoolOptionsModel {
	if o == nil {
		return nil
	}
	m := &mksPoolOptionsModel{
		Name: nullableString(o.Name), DisplayName: nullableString(o.DisplayName), Description: nullableString(o.Description),
		SKUKinds: listFromStringSlice(o.SKUKind), ComputeTypes: listFromStringSlice(o.ComputeType), Datacenters: listFromStringSlice(o.Datacenters),
		SKUNames: listFromStringSlice(o.SKUNames), Images: listFromStringSlice(o.Images),
	}
	for _, g := range o.OSImages {
		img := mksOSImageModel{Name: nullableString(g.Name)}
		for _, v := range g.Versions {
			img.Versions = append(img.Versions, mksOSVersionModel{Version: nullableString(v.Version), ImageName: nullableString(v.ImageName)})
		}
		m.OSImages = append(m.OSImages, img)
	}
	for _, it := range o.InstanceTypes {
		t := mksInstanceTypeModel{Name: nullableString(it.Name), Kind: nullableString(it.Kind), ComputeType: nullableString(it.ComputeType), DisplayLabel: nullableString(it.DisplayLabel),
			Images: listFromStringSlice(it.Images), Datacenters: listFromStringSlice(it.Datacenters)}
		for _, sh := range it.Shapes {
			t.Shapes = append(t.Shapes, mksShapeModel{CPU: int64Or(sh.CPU), RAMGb: int64Or(sh.RAMGb), StorageGb: int64Or(sh.StorageGb), GPUType: nullableString(sh.GPUType),
				GPUCount: int64Or(sh.GPUCount), GPUMemoryGb: int64Or(sh.GPUMemoryGb), DisplayText: nullableString(sh.DisplayText)})
		}
		m.InstanceTypes = append(m.InstanceTypes, t)
	}
	return m
}

// int64Or returns the number, or null when it is zero (the platform leaves out a value it does not have).
func int64Or(v int32) types.Int64 {
	if v == 0 {
		return types.Int64Null()
	}
	return types.Int64Value(int64(v))
}

func (d *mksSKUCreateOptionsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	if d.pd == nil {
		return
	}
	var cfg mksSKUCreateOptionsModel
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("name"), &cfg.Name)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("project"), &cfg.Project)...)
	if resp.Diagnostics.HasError() {
		return
	}
	project, name := cfg.Project.ValueString(), cfg.Name.ValueString()
	o, err := d.pd.Clientset.V1alpha1().MKSClusterProfiles(project).CreateOptions(ctx, name)
	if err != nil {
		handleAPIError(&resp.Diagnostics, err, "Read Kubernetes SKU create options", nil)
		return
	}
	cfg.ID = types.StringValue(project + "/" + name)
	cfg.DataCenterRegions = listFromStringSlice(o.DataCenterRegions)
	cfg.DefaultKubernetesVersion = nullableString(o.DefaultKubernetesVersion)
	cfg.SupportedKubernetesVersions = listFromStringSlice(o.SupportedKubernetesVersion)
	cfg.EnableOIDC = types.BoolValue(o.EnableOIDC)
	cfg.ControlPlane = poolOptionsModel(o.ControlPlane)
	cfg.Workers = poolOptionsModel(o.WorkerNode)
	resp.Diagnostics.Append(resp.State.Set(ctx, &cfg)...)
}

// ---------------------------------------------------------------------------
// gpupaas_kubernetes_sku_capacity
// ---------------------------------------------------------------------------

// NewMKSSKUCapacityDataSource constructs the gpupaas_kubernetes_sku_capacity data source.
func NewMKSSKUCapacityDataSource() datasource.DataSource { return &mksSKUCapacityDataSource{} }

type mksSKUCapacityDataSource struct{ pd *client.ProviderData }

type mksSKUCapacityModel struct {
	ID      types.String `tfsdk:"id"`
	Name    types.String `tfsdk:"name"`
	Project types.String `tfsdk:"project"`
	JSON    types.String `tfsdk:"json"`
	// Supported is false when the platform has no capacity answer for the SKU's compute type.
	Supported types.Bool `tfsdk:"supported"`
}

func (d *mksSKUCapacityDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_kubernetes_sku_capacity"
}

func (d *mksSKUCapacityDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = dsschema.Schema{
		MarkdownDescription: "The capacity left for a Kubernetes SKU. The API reference does not describe the answer, so it is returned as a JSON document in `json`; read it with `jsondecode`.",
		Attributes: map[string]dsschema.Attribute{
			"id":        dsschema.StringAttribute{Computed: true},
			"name":      dsschema.StringAttribute{Required: true, MarkdownDescription: "Name of the Kubernetes SKU."},
			"project":   dsschema.StringAttribute{Required: true, MarkdownDescription: "Project that holds the SKU, usually `system-catalog`."},
			"json":      dsschema.StringAttribute{Computed: true, MarkdownDescription: "The platform's answer, as JSON. Null when `supported` is false."},
			"supported": dsschema.BoolAttribute{Computed: true, MarkdownDescription: "False when the platform does not report capacity for the compute type of this SKU (for example bare metal): `json` is then null and a warning is shown. Any other refusal is an error."},
		},
	}
}

func (d *mksSKUCapacityDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.pd = providerData(req.ProviderData, &resp.Diagnostics)
}

func (d *mksSKUCapacityDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	if d.pd == nil {
		return
	}
	var cfg mksSKUCapacityModel
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("name"), &cfg.Name)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("project"), &cfg.Project)...)
	if resp.Diagnostics.HasError() {
		return
	}
	project, name := cfg.Project.ValueString(), cfg.Name.ValueString()
	cfg.ID = types.StringValue(project + "/" + name)
	c, err := d.pd.Clientset.V1alpha1().MKSClusterProfiles(project).AvailableCapacity(ctx, name)
	if err != nil {
		var apiErr *gpupaas.APIError
		if !capacityNotSupported(err, &apiErr) {
			handleAPIError(&resp.Diagnostics, err, "Read Kubernetes SKU capacity", nil)
			return
		}
		resp.Diagnostics.AddWarning("Capacity is not reported for this SKU",
			"The platform does not report capacity for the compute type of Kubernetes SKU "+name+": "+apiErr.Message)
		cfg.JSON = types.StringNull()
		cfg.Supported = types.BoolValue(false)
		resp.Diagnostics.Append(resp.State.Set(ctx, &cfg)...)
		return
	}
	cfg.JSON = nullableString(string(c.Raw))
	cfg.Supported = types.BoolValue(true)
	resp.Diagnostics.Append(resp.State.Set(ctx, &cfg)...)
}

// capacityNotSupported reports the platform's answer for a profile whose compute type has no capacity figure
// (400 "available-capacity is not supported for this compute type"). Any other refusal is a real error.
func capacityNotSupported(err error, into **gpupaas.APIError) bool {
	var apiErr *gpupaas.APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusBadRequest {
		return false
	}
	if !strings.Contains(strings.ToLower(apiErr.Message+" "+apiErr.Body), "not supported") {
		return false
	}
	*into = apiErr
	return true
}

// ---------------------------------------------------------------------------
// gpupaas_kubernetes_cluster_audit_events
// ---------------------------------------------------------------------------

// NewMKSAuditEventsDataSource constructs the gpupaas_kubernetes_cluster_audit_events data source.
func NewMKSAuditEventsDataSource() datasource.DataSource { return &mksAuditEventsDataSource{} }

type mksAuditEventsDataSource struct{ pd *client.ProviderData }

type mksAuditEventModel struct {
	ID           types.String `tfsdk:"id"`
	ResourceType types.String `tfsdk:"resource_type"`
	ResourceName types.String `tfsdk:"resource_name"`
	Action       types.String `tfsdk:"action"`
	Actor        types.String `tfsdk:"actor"`
	Message      types.String `tfsdk:"message"`
	Timestamp    types.String `tfsdk:"timestamp"`
	Result       types.String `tfsdk:"result"`
	OperationID  types.String `tfsdk:"operation_id"`
}

type mksAuditEventsModel struct {
	ID           types.String         `tfsdk:"id"`
	Cluster      types.String         `tfsdk:"cluster"`
	Project      types.String         `tfsdk:"project"`
	Action       types.String         `tfsdk:"action"`
	Result       types.String         `tfsdk:"result"`
	ResourceType types.String         `tfsdk:"resource_type"`
	Actor        types.String         `tfsdk:"actor"`
	From         types.String         `tfsdk:"from"`
	To           types.String         `tfsdk:"to"`
	Limit        types.Int64          `tfsdk:"limit"`
	Total        types.Int64          `tfsdk:"total"`
	Events       []mksAuditEventModel `tfsdk:"events"`
}

func (d *mksAuditEventsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_kubernetes_cluster_audit_events"
}

func (d *mksAuditEventsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = dsschema.Schema{
		MarkdownDescription: "The audit trail of a managed Kubernetes cluster, newest first: who asked for what (create, upgrade, pool changes, node actions) and how it ended. " +
			"Use it to find out why a cluster changed, or who changed it.",
		Attributes: map[string]dsschema.Attribute{
			"id":            dsschema.StringAttribute{Computed: true},
			"cluster":       dsschema.StringAttribute{Required: true, MarkdownDescription: "Name of the cluster."},
			"project":       dsschema.StringAttribute{Required: true},
			"action":        dsschema.StringAttribute{Optional: true, MarkdownDescription: "Only events of this action, for example `cluster.upgrade`, `cluster.nodepool.create`, `cluster.metadata.update` or `node.drain`."},
			"result":        dsschema.StringAttribute{Optional: true, MarkdownDescription: "Only events with this result: `accepted`, `rejected`, `completed` or `failed`."},
			"resource_type": dsschema.StringAttribute{Optional: true, MarkdownDescription: "Only events about this kind of resource: `cluster`, `controlPlane`, `nodePool`, `node` or `sku`."},
			"actor":         dsschema.StringAttribute{Optional: true, MarkdownDescription: "Only events of this user."},
			"from":          dsschema.StringAttribute{Optional: true, MarkdownDescription: "Only events at or after this time (RFC 3339)."},
			"to":            dsschema.StringAttribute{Optional: true, MarkdownDescription: "Only events at or before this time (RFC 3339)."},
			"limit":         dsschema.Int64Attribute{Optional: true, MarkdownDescription: "Most events to return. Defaults to 100; the platform caps it at 500."},
			"total":         dsschema.Int64Attribute{Computed: true, MarkdownDescription: "How many events match the filters, which can be more than `limit`."},
			"events": dsschema.ListNestedAttribute{Computed: true, NestedObject: dsschema.NestedAttributeObject{Attributes: map[string]dsschema.Attribute{
				"id":            dsschema.StringAttribute{Computed: true},
				"resource_type": dsschema.StringAttribute{Computed: true},
				"resource_name": dsschema.StringAttribute{Computed: true},
				"action":        dsschema.StringAttribute{Computed: true},
				"actor":         dsschema.StringAttribute{Computed: true},
				"message":       dsschema.StringAttribute{Computed: true},
				"timestamp":     dsschema.StringAttribute{Computed: true, MarkdownDescription: "When the event happened (RFC 3339, UTC)."},
				"result":        dsschema.StringAttribute{Computed: true},
				"operation_id":  dsschema.StringAttribute{Computed: true, MarkdownDescription: "The day-2 operation the event belongs to, if any."},
			}}},
		},
	}
}

func (d *mksAuditEventsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.pd = providerData(req.ProviderData, &resp.Diagnostics)
}

func (d *mksAuditEventsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	if d.pd == nil {
		return
	}
	var cfg mksAuditEventsModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	filter := typed.MKSAuditEventFilter{Action: cfg.Action.ValueString(), Result: cfg.Result.ValueString(), ResourceType: cfg.ResourceType.ValueString(), Actor: cfg.Actor.ValueString(), Limit: 100}
	if !cfg.Limit.IsNull() && !cfg.Limit.IsUnknown() {
		filter.Limit = int(cfg.Limit.ValueInt64())
	}
	for _, t := range []struct {
		name string
		v    types.String
		dst  *time.Time
	}{{"from", cfg.From, &filter.From}, {"to", cfg.To, &filter.To}} {
		if !set(t.v) {
			continue
		}
		parsed, err := time.Parse(time.RFC3339, t.v.ValueString())
		if err != nil {
			resp.Diagnostics.AddAttributeError(path.Root(t.name), "Invalid time", "Use an RFC 3339 time such as 2026-11-02T09:30:00Z: "+err.Error())
			return
		}
		*t.dst = parsed
	}
	project, cluster := cfg.Project.ValueString(), cfg.Cluster.ValueString()
	list, err := d.pd.Clientset.V1alpha1().MKSClusters(project).AuditEvents(ctx, cluster, filter)
	if err != nil {
		handleAPIError(&resp.Diagnostics, err, "Read cluster audit events", nil)
		return
	}
	cfg.ID = types.StringValue(project + "/" + cluster + "/audit-events")
	cfg.Total = types.Int64Value(list.Count)
	// An empty list, not null: length(...events) must work when nothing matches.
	cfg.Events = []mksAuditEventModel{}
	for _, e := range list.Items {
		ts := types.StringNull()
		if e.Timestamp != 0 {
			ts = types.StringValue(time.Unix(int64(e.Timestamp), 0).UTC().Format(time.RFC3339))
		}
		cfg.Events = append(cfg.Events, mksAuditEventModel{
			ID: nullableString(e.ID), ResourceType: nullableString(e.ResourceType), ResourceName: nullableString(e.ResourceName), Action: nullableString(e.Action),
			Actor: nullableString(e.Actor), Message: nullableString(e.Message), Timestamp: ts, Result: nullableString(e.Result), OperationID: nullableString(e.OperationID),
		})
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &cfg)...)
}

// ---------------------------------------------------------------------------
// gpupaas_kubernetes_cluster_status
// ---------------------------------------------------------------------------

// NewMKSClusterStatusDataSource constructs the gpupaas_kubernetes_cluster_status data source.
func NewMKSClusterStatusDataSource() datasource.DataSource { return &mksClusterStatusDataSource{} }

type mksClusterStatusDataSource struct{ pd *client.ProviderData }

type mksStatusNodeModel struct {
	Hostname    types.String `tfsdk:"hostname"`
	PrivateIP   types.String `tfsdk:"private_ip"`
	KubeVersion types.String `tfsdk:"kubernetes_version"`
	OS          types.String `tfsdk:"os"`
	Arch        types.String `tfsdk:"arch"`
	Roles       types.List   `tfsdk:"roles"`
	Status      types.String `tfsdk:"status"`
	State       types.Map    `tfsdk:"state"`
	Labels      types.Map    `tfsdk:"labels"`
}

type mksStatusPlatformModel struct {
	Version    types.String `tfsdk:"version"`
	Components types.Map    `tfsdk:"components"`
}

type mksStatusPoolModel struct {
	Name        types.String            `tfsdk:"name"`
	SKUName     types.String            `tfsdk:"sku_name"`
	SKUType     types.String            `tfsdk:"sku_type"`
	ComputeType types.String            `tfsdk:"compute_type"`
	OSImage     types.String            `tfsdk:"os_image"`
	Datacenter  types.String            `tfsdk:"datacenter"`
	Status      types.String            `tfsdk:"status"`
	Health      types.String            `tfsdk:"health"`
	Nodes       []mksStatusNodeModel    `tfsdk:"nodes"`
	Platform    *mksStatusPlatformModel `tfsdk:"platform"`
}

type mksClusterStatusModel struct {
	ID                          types.String         `tfsdk:"id"`
	Cluster                     types.String         `tfsdk:"cluster"`
	Project                     types.String         `tfsdk:"project"`
	Phase                       types.String         `tfsdk:"phase"`
	Status                      types.String         `tfsdk:"status"`
	Reason                      types.String         `tfsdk:"reason"`
	Action                      types.String         `tfsdk:"action"`
	KubernetesVersion           types.String         `tfsdk:"kubernetes_version"`
	Location                    types.String         `tfsdk:"location"`
	AvailableKubernetesVersions types.List           `tfsdk:"available_kubernetes_versions"`
	Health                      types.String         `tfsdk:"health"`
	ControlPlane                *mksStatusPoolModel  `tfsdk:"control_plane"`
	WorkerPools                 []mksStatusPoolModel `tfsdk:"worker_pools"`
}

func (d *mksClusterStatusDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_kubernetes_cluster_status"
}

func mksStatusPoolAttrs() map[string]dsschema.Attribute {
	return map[string]dsschema.Attribute{
		"name":         dsschema.StringAttribute{Computed: true, MarkdownDescription: "Empty for the control plane."},
		"sku_name":     dsschema.StringAttribute{Computed: true},
		"sku_type":     dsschema.StringAttribute{Computed: true},
		"compute_type": dsschema.StringAttribute{Computed: true},
		"os_image":     dsschema.StringAttribute{Computed: true},
		"datacenter":   dsschema.StringAttribute{Computed: true},
		"status":       dsschema.StringAttribute{Computed: true, MarkdownDescription: "The pool's lifecycle: `CREATING`, `ACTIVE`, `UPDATING`, `DELETING`, `CREATE_FAILED` or `DELETE_FAILED`. It is not machine health."},
		"health":       dsschema.StringAttribute{Computed: true, MarkdownDescription: "Health of the pool: `healthy`, `unhealthy`, `degraded` or `unknown`."},
		"nodes": dsschema.ListNestedAttribute{Computed: true, NestedObject: dsschema.NestedAttributeObject{Attributes: map[string]dsschema.Attribute{
			"hostname":           dsschema.StringAttribute{Computed: true},
			"private_ip":         dsschema.StringAttribute{Computed: true},
			"kubernetes_version": dsschema.StringAttribute{Computed: true},
			"os":                 dsschema.StringAttribute{Computed: true},
			"arch":               dsschema.StringAttribute{Computed: true},
			"roles":              dsschema.ListAttribute{Computed: true, ElementType: types.StringType, MarkdownDescription: "`Master` and/or `Worker`."},
			"status":             dsschema.StringAttribute{Computed: true, MarkdownDescription: "The platform's own status word for the node, for example `READY`."},
			"state":              dsschema.MapAttribute{Computed: true, ElementType: types.StringType, MarkdownDescription: "Kubernetes conditions (`DiskPressure`, `MemoryPressure`, `PIDPressure`, `NetworkUnavailable`, `ClusterNodeReady`) as `true`, `false` or `unknown`."},
			"labels":             dsschema.MapAttribute{Computed: true, ElementType: types.StringType},
		}}},
		"platform": dsschema.SingleNestedAttribute{Computed: true, Attributes: map[string]dsschema.Attribute{
			"version":    dsschema.StringAttribute{Computed: true, MarkdownDescription: "The platform build the pool's nodes run."},
			"components": dsschema.MapAttribute{Computed: true, ElementType: types.StringType, MarkdownDescription: "Installed component versions by display name."},
		}},
	}
}

func (d *mksClusterStatusDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = dsschema.Schema{
		MarkdownDescription: "The live status of a managed Kubernetes cluster: its lifecycle, health, the upgrade targets and, for the control plane and each worker pool, the nodes the platform reports with their addresses and Kubernetes conditions. " +
			"Unlike `gpupaas_kubernetes_cluster` it reads the platform's status document, which joins the declared pools with what runs behind them.",
		Attributes: map[string]dsschema.Attribute{
			"id":                            dsschema.StringAttribute{Computed: true},
			"cluster":                       dsschema.StringAttribute{Required: true, MarkdownDescription: "Name of the cluster."},
			"project":                       dsschema.StringAttribute{Required: true},
			"phase":                         dsschema.StringAttribute{Computed: true, MarkdownDescription: "Normalized status: `provisioning`, `running`, `updating`, `upgrading`, `degraded`, `error`, `destroying`, ..."},
			"status":                        dsschema.StringAttribute{Computed: true, MarkdownDescription: "Raw status, for example `MKS_CLUSTER_STATUS_RUNNING`."},
			"reason":                        dsschema.StringAttribute{Computed: true},
			"action":                        dsschema.StringAttribute{Computed: true},
			"kubernetes_version":            dsschema.StringAttribute{Computed: true},
			"location":                      dsschema.StringAttribute{Computed: true},
			"available_kubernetes_versions": dsschema.ListAttribute{Computed: true, ElementType: types.StringType, MarkdownDescription: "Versions the cluster can be upgraded to."},
			"health":                        dsschema.StringAttribute{Computed: true, MarkdownDescription: "Overall health: `healthy`, `unhealthy`, `degraded` or `unknown`."},
			"control_plane":                 dsschema.SingleNestedAttribute{Computed: true, Attributes: mksStatusPoolAttrs()},
			"worker_pools":                  dsschema.ListNestedAttribute{Computed: true, NestedObject: dsschema.NestedAttributeObject{Attributes: mksStatusPoolAttrs()}},
		},
	}
}

func (d *mksClusterStatusDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.pd = providerData(req.ProviderData, &resp.Diagnostics)
}

// healthWord turns MKS_HEALTH_STATE_HEALTHY into healthy.
func healthWord(s string) types.String {
	const prefix = "MKS_HEALTH_STATE_"
	s = strings.TrimPrefix(s, prefix)
	return nullableString(strings.ToLower(s))
}

func statusPoolModel(p *apiv1.MKSPoolStatus, health string) *mksStatusPoolModel {
	if p == nil {
		return nil
	}
	m := &mksStatusPoolModel{
		Name: nullableString(p.Name), SKUName: nullableString(p.SKUName), SKUType: nullableString(p.SKUType), ComputeType: nullableString(p.ComputeType),
		OSImage: nullableString(p.OSImage), Datacenter: nullableString(p.Datacenter), Status: nullableString(p.Status), Health: healthWord(health),
	}
	for _, n := range p.Nodes {
		m.Nodes = append(m.Nodes, mksStatusNodeModel{
			Hostname: nullableString(n.Hostname), PrivateIP: nullableString(n.PrivateIP), KubeVersion: nullableString(n.KubeVersion), OS: nullableString(n.OS), Arch: nullableString(n.Arch),
			Roles: listFromStringSlice(n.Roles), Status: nullableString(n.Status), State: mapOrNull(n.State), Labels: mapOrNull(n.Labels),
		})
	}
	if pl := p.Platform; pl != nil {
		m.Platform = &mksStatusPlatformModel{Version: nullableString(pl.Version), Components: mapOrNull(pl.Components)}
	}
	return m
}

// mapOrNull returns a string map, or null when it has no entries.
func mapOrNull(in map[string]string) types.Map {
	if len(in) == 0 {
		return types.MapNull(types.StringType)
	}
	return mapFromStringMap(in)
}

func (d *mksClusterStatusDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	if d.pd == nil {
		return
	}
	var cfg mksClusterStatusModel
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("cluster"), &cfg.Cluster)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("project"), &cfg.Project)...)
	if resp.Diagnostics.HasError() {
		return
	}
	project, cluster := cfg.Project.ValueString(), cfg.Cluster.ValueString()
	st, err := d.pd.Clientset.V1alpha1().MKSClusters(project).Status(ctx, cluster)
	if err != nil {
		handleAPIError(&resp.Diagnostics, err, "Read cluster status", nil)
		return
	}
	cfg.ID = types.StringValue(project + "/" + cluster + "/status")
	status := apiv1.MKSClusterStatus{Status: st.Status.Status}
	cfg.Phase = types.StringValue(status.Phase())
	cfg.Status = nullableString(st.Status.Status)
	cfg.Reason = nullableString(st.Status.Reason)
	cfg.Action = nullableString(st.Status.Action)
	cfg.KubernetesVersion = nullableString(st.Spec.KubernetesVersion)
	cfg.Location = nullableString(st.Spec.Location)
	cfg.AvailableKubernetesVersions = listFromStringSlice(st.Status.AvailableKubernetesVersions)
	cfg.Health = types.StringNull()
	var cpHealth string
	var poolHealth map[string]string
	if h := st.Status.Health; h != nil {
		cfg.Health = healthWord(h.Overall)
		cpHealth, poolHealth = h.ControlPlane, h.NodePools
	}
	cfg.ControlPlane = statusPoolModel(st.Spec.ControlPlane, cpHealth)
	cfg.WorkerPools = nil
	for i := range st.Spec.WorkerPools {
		p := st.Spec.WorkerPools[i]
		cfg.WorkerPools = append(cfg.WorkerPools, *statusPoolModel(&p, poolHealth[p.Name]))
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &cfg)...)
}
