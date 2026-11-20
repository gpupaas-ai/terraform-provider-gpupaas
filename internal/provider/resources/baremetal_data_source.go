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
	_ datasource.DataSource              = (*bareMetalDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*bareMetalDataSource)(nil)
	_ datasource.DataSource              = (*bareMetalProfileDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*bareMetalProfileDataSource)(nil)
)

// --- instance --------------------------------------------------------------

// NewBareMetalDataSource constructs the gpupaas_bare_metal_instance data source.
func NewBareMetalDataSource() datasource.DataSource { return &bareMetalDataSource{} }

type bareMetalDataSource struct{ pd *client.ProviderData }

// bareMetalDataSpecModel leaves out what is not safe or useful to expose: the write-only password
// and the cloud-init script, which commonly holds secrets.
type bareMetalDataSpecModel struct {
	Type              types.String `tfsdk:"type"`
	Image             types.String `tfsdk:"image"`
	SSHKey            types.String `tfsdk:"ssh_key"`
	Datacenter        types.String `tfsdk:"datacenter"`
	VPC               types.String `tfsdk:"vpc"`
	Subnet            types.String `tfsdk:"subnet"`
	SecurityGroup     types.String `tfsdk:"security_group"`
	AssignPublicIP    types.Bool   `tfsdk:"assign_public_ip"`
	HSSStorage        types.String `tfsdk:"hss_storage"`
	ObjectStorage     types.String `tfsdk:"object_storage"`
	ContractTerm      types.Int64  `tfsdk:"contract_term"`
	GPUName           types.String `tfsdk:"gpu_name"`
	ProvisionerType   types.String `tfsdk:"provisioner_type"`
	ComputeInstanceID types.String `tfsdk:"compute_instance_id"`
}

type bareMetalDataSourceModel struct {
	ID        types.String           `tfsdk:"id"`
	Name      types.String           `tfsdk:"name"`
	Project   types.String           `tfsdk:"project"`
	Workspace types.String           `tfsdk:"workspace"`
	Metadata  MetadataModel          `tfsdk:"metadata"`
	Spec      bareMetalDataSpecModel `tfsdk:"spec"`
	Status    bareMetalStatusModel   `tfsdk:"status"`
}

func (d *bareMetalDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_bare_metal_instance"
}

func (d *bareMetalDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	str := func() dsschema.Attribute { return dsschema.StringAttribute{Computed: true} }
	resp.Schema = dsschema.Schema{
		MarkdownDescription: "Look up a first-class bare-metal machine by name within a project, optionally scoped to a workspace. " +
			"The password and the cloud-init script are never returned.",
		Attributes: map[string]dsschema.Attribute{
			"id":        dsschema.StringAttribute{Computed: true},
			"name":      dsschema.StringAttribute{Required: true},
			"project":   dsschema.StringAttribute{Required: true},
			"workspace": dsschema.StringAttribute{Optional: true, MarkdownDescription: "Workspace that owns the machine; omit for a project-scoped machine."},
			"metadata":  MetadataDataSourceAttribute(),
			"spec": dsschema.SingleNestedAttribute{
				Computed: true,
				Attributes: map[string]dsschema.Attribute{
					"type": str(), "image": str(), "ssh_key": str(), "datacenter": str(), "vpc": str(), "subnet": str(),
					"security_group": str(), "assign_public_ip": dsschema.BoolAttribute{Computed: true},
					"hss_storage": str(), "object_storage": str(), "contract_term": dsschema.Int64Attribute{Computed: true},
					"gpu_name": str(), "provisioner_type": str(), "compute_instance_id": str(),
				},
			},
			"status": dsschema.SingleNestedAttribute{
				Computed: true,
				Attributes: map[string]dsschema.Attribute{
					"phase": str(), "status": str(), "reason": str(), "action": str(), "power_state": str(),
					"host_name": str(), "private_ip": str(), "public_ip": str(), "user_name": str(),
					"platform": str(), "gpus": str(), "vcpus": str(), "memory_gib": str(), "network": str(),
				},
			},
		},
	}
}

func (d *bareMetalDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.pd = providerData(req.ProviderData, &resp.Diagnostics)
}

func (d *bareMetalDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	if d.pd == nil {
		return
	}
	var cfg bareMetalDataSourceModel
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("name"), &cfg.Name)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("project"), &cfg.Project)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("workspace"), &cfg.Workspace)...)
	if resp.Diagnostics.HasError() {
		return
	}
	project, workspace, name := cfg.Project.ValueString(), stringOr(cfg.Workspace, ""), cfg.Name.ValueString()
	var machines interface {
		Get(context.Context, string, gpupaas.GetOptions) (*apiv1.BareMetal, error)
	}
	if workspace == "" {
		machines = d.pd.Clientset.V1alpha1().BareMetals(project)
	} else {
		machines = d.pd.Clientset.V1alpha1().Workspaces(project).BareMetals(workspace)
	}
	srv, err := machines.Get(ctx, name, gpupaas.GetOptions{})
	if err != nil {
		handleAPIError(&resp.Diagnostics, err, "Read bare metal", nil)
		return
	}
	// An empty spec marks the read as an import, so every field takes the server value.
	m := bareMetalModelFromServer(ctx, bareMetalResourceModel{Metadata: MetadataModel{Name: cfg.Name, Project: cfg.Project, Workspace: cfg.Workspace}}, srv, false, &resp.Diagnostics)
	cfg.ID = types.StringValue(bareMetalID(project, workspace, name))
	cfg.Metadata = m.Metadata
	cfg.Spec = bareMetalDataSpecModel{
		Type: m.Spec.Type, Image: m.Spec.Image, SSHKey: m.Spec.SSHKey, Datacenter: m.Spec.Datacenter, VPC: m.Spec.VPC, Subnet: m.Spec.Subnet,
		SecurityGroup: m.Spec.SecurityGroup, AssignPublicIP: m.Spec.AssignPublicIP, HSSStorage: m.Spec.HSSStorage, ObjectStorage: m.Spec.ObjectStorage,
		ContractTerm: m.Spec.ContractTerm, GPUName: m.Spec.GPUName, ProvisionerType: m.Spec.ProvisionerType, ComputeInstanceID: m.Spec.ComputeInstanceID,
	}
	cfg.Status = m.Status
	resp.Diagnostics.Append(resp.State.Set(ctx, &cfg)...)
}

// --- SKU -------------------------------------------------------------------

// NewBareMetalProfileDataSource constructs the gpupaas_bare_metal_sku data source.
func NewBareMetalProfileDataSource() datasource.DataSource { return &bareMetalProfileDataSource{} }

type bareMetalProfileDataSource struct{ pd *client.ProviderData }

type bmInventoryTagModel struct {
	Key   types.String `tfsdk:"key"`
	Value types.String `tfsdk:"value"`
}

type bareMetalProfileDataSourceModel struct {
	ID                      types.String          `tfsdk:"id"`
	Name                    types.String          `tfsdk:"name"`
	Project                 types.String          `tfsdk:"project"`
	ProvisionerType         types.String          `tfsdk:"provisioner_type"`
	Images                  types.List            `tfsdk:"images"`
	DefaultImage            types.String          `tfsdk:"default_image"`
	Datacenters             types.List            `tfsdk:"datacenters"`
	VPC                     types.String          `tfsdk:"vpc"`
	Subnet                  types.String          `tfsdk:"subnet"`
	AssignPublicIP          types.String          `tfsdk:"assign_public_ip"`
	ContractTerms           types.List            `tfsdk:"contract_terms"`
	GPUName                 types.String          `tfsdk:"gpu_name"`
	UseCase                 types.String          `tfsdk:"use_case"`
	InventoryConfig         []bmInventoryTagModel `tfsdk:"inventory_config"`
	SharedFilesystemEnabled types.Bool            `tfsdk:"shared_filesystem_enabled"`
	ObjectStorageEnabled    types.Bool            `tfsdk:"object_storage_enabled"`
	Agents                  types.List            `tfsdk:"agents"`
	Status                  types.String          `tfsdk:"status"`
	TemplateVersion         types.String          `tfsdk:"template_version"`
}

func (d *bareMetalProfileDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_bare_metal_sku"
}

func (d *bareMetalProfileDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	strList := func(desc string) dsschema.Attribute {
		return dsschema.ListAttribute{Computed: true, ElementType: types.StringType, MarkdownDescription: desc}
	}
	resp.Schema = dsschema.Schema{
		MarkdownDescription: "A bare-metal SKU (BmProfile): the images, datacenters and options a `gpupaas_bare_metal_instance` created from it may use.",
		Attributes: map[string]dsschema.Attribute{
			"id":               dsschema.StringAttribute{Computed: true},
			"name":             dsschema.StringAttribute{Required: true},
			"project":          dsschema.StringAttribute{Required: true, MarkdownDescription: "Project that holds the SKU, usually `system-catalog`."},
			"provisioner_type": dsschema.StringAttribute{Computed: true, MarkdownDescription: "`bcm` or `non_bcm`."},
			"images":           strList("Images machines of this SKU may use."),
			"default_image":    dsschema.StringAttribute{Computed: true},
			"datacenters":      strList("Datacenters machines of this SKU may be placed in."),
			"vpc":              dsschema.StringAttribute{Computed: true},
			"subnet":           dsschema.StringAttribute{Computed: true},
			"assign_public_ip": dsschema.StringAttribute{Computed: true, MarkdownDescription: "Public IP policy: `Disabled`, `Enabled` or `CustomerSelectable`."},
			"contract_terms":   dsschema.ListAttribute{Computed: true, ElementType: types.Int64Type, MarkdownDescription: "Contract terms in months the SKU offers."},
			"gpu_name":         dsschema.StringAttribute{Computed: true},
			"use_case":         dsschema.StringAttribute{Computed: true},
			"inventory_config": dsschema.ListNestedAttribute{
				Computed:            true,
				MarkdownDescription: "Inventory tags: devices tagged `key=value` are candidates for machines of this SKU.",
				NestedObject: dsschema.NestedAttributeObject{Attributes: map[string]dsschema.Attribute{
					"key": dsschema.StringAttribute{Computed: true}, "value": dsschema.StringAttribute{Computed: true},
				}},
			},
			"shared_filesystem_enabled": dsschema.BoolAttribute{Computed: true},
			"object_storage_enabled":    dsschema.BoolAttribute{Computed: true},
			"agents": strList("Orchestration agents that run the SKU's workflows. Provisioning fails with \"no healthy agents found\" when none of them is healthy; " +
				"the list is not a health check."),
			"status":           dsschema.StringAttribute{Computed: true},
			"template_version": dsschema.StringAttribute{Computed: true},
		},
	}
}

func (d *bareMetalProfileDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.pd = providerData(req.ProviderData, &resp.Diagnostics)
}

func (d *bareMetalProfileDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	if d.pd == nil {
		return
	}
	var cfg bareMetalProfileDataSourceModel
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("name"), &cfg.Name)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("project"), &cfg.Project)...)
	if resp.Diagnostics.HasError() {
		return
	}
	project, name := cfg.Project.ValueString(), cfg.Name.ValueString()
	p, err := getBmProfile(ctx, d.pd.Clientset.V1alpha1(), project, name)
	if err != nil {
		handleAPIError(&resp.Diagnostics, err, "Read bare metal profile", nil)
		return
	}
	s := p.Spec
	cfg.ID = types.StringValue(project + "/" + name)
	cfg.ProvisionerType = nullableString(s.ProvisionerType)
	cfg.Images = listFromStringSlice(s.Image)
	cfg.DefaultImage = nullableString(s.ImageDefault)
	cfg.Datacenters = listFromStringSlice(s.Datacenter)
	cfg.VPC = nullableString(s.VPC)
	cfg.Subnet = nullableString(s.Subnet)
	cfg.AssignPublicIP = nullableString(s.AssignPublicIP)
	terms := make([]int64, 0, len(s.MRCContractTerm))
	for _, t := range s.MRCContractTerm {
		terms = append(terms, int64(t))
	}
	cfg.ContractTerms, _ = types.ListValueFrom(ctx, types.Int64Type, terms)
	cfg.GPUName = nullableString(s.GPUName)
	cfg.UseCase = nullableString(s.UseCase)
	cfg.InventoryConfig = nil
	for _, t := range s.InventoryConfig {
		cfg.InventoryConfig = append(cfg.InventoryConfig, bmInventoryTagModel{Key: nullableString(t.Key), Value: nullableString(t.Value)})
	}
	cfg.SharedFilesystemEnabled = types.BoolValue(s.SharedFilesystem)
	cfg.ObjectStorageEnabled = types.BoolValue(s.ObjectStorage)
	var agents []string
	if gs := p.Status.GlobalSettings; gs != nil {
		agents = gs.Agents
	}
	cfg.Agents = listFromStringSlice(agents)
	cfg.Status = nullableString(p.Status.Status)
	cfg.TemplateVersion = nullableString(s.TemplateVersion)
	resp.Diagnostics.Append(resp.State.Set(ctx, &cfg)...)
}
