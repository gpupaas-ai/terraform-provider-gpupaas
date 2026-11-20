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
	_ datasource.DataSource              = (*vmDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*vmDataSource)(nil)
	_ datasource.DataSource              = (*vmProfileDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*vmProfileDataSource)(nil)
)

// --- instance --------------------------------------------------------------

// NewVMDataSource constructs the gpupaas_virtual_machine data source.
func NewVMDataSource() datasource.DataSource { return &vmDataSource{} }

type vmDataSource struct{ pd *client.ProviderData }

// vmDataSpecModel leaves out what is not safe or useful to expose: the write-only password and the
// cloud-init script, which commonly holds secrets.
type vmDataSpecModel struct {
	Type           types.String `tfsdk:"type"`
	CPUCount       types.String `tfsdk:"cpu_count"`
	Memory         types.String `tfsdk:"memory"`
	GPUCount       types.String `tfsdk:"gpu_count"`
	Image          types.String `tfsdk:"image"`
	SSHKey         types.String `tfsdk:"ssh_key"`
	Datacenter     types.String `tfsdk:"datacenter"`
	VPC            types.String `tfsdk:"vpc"`
	Subnet         types.String `tfsdk:"subnet"`
	SecurityGroup  types.String `tfsdk:"security_group"`
	AssignPublicIP types.Bool   `tfsdk:"assign_public_ip"`
	SharedStorage  types.String `tfsdk:"shared_storage"`
	BootDiskSize   types.Int64  `tfsdk:"boot_disk_size"`
	GPUModel       types.String `tfsdk:"gpu_model"`
	GPUVendor      types.String `tfsdk:"gpu_vendor"`
	VMID           types.String `tfsdk:"vm_id"`
}

type vmDataSourceModel struct {
	ID        types.String    `tfsdk:"id"`
	Name      types.String    `tfsdk:"name"`
	Project   types.String    `tfsdk:"project"`
	Workspace types.String    `tfsdk:"workspace"`
	Metadata  MetadataModel   `tfsdk:"metadata"`
	Spec      vmDataSpecModel `tfsdk:"spec"`
	Status    vmStatusModel   `tfsdk:"status"`
}

func (d *vmDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_virtual_machine"
}

func (d *vmDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	str := func() dsschema.Attribute { return dsschema.StringAttribute{Computed: true} }
	resp.Schema = dsschema.Schema{
		MarkdownDescription: "Look up a first-class virtual machine by name within a project, optionally scoped to a workspace. " +
			"The password and the cloud-init script are never returned.",
		Attributes: map[string]dsschema.Attribute{
			"id":        dsschema.StringAttribute{Computed: true},
			"name":      dsschema.StringAttribute{Required: true},
			"project":   dsschema.StringAttribute{Required: true},
			"workspace": dsschema.StringAttribute{Optional: true, MarkdownDescription: "Workspace that owns the VM; omit for a project-scoped VM."},
			"metadata":  MetadataDataSourceAttribute(),
			"spec": dsschema.SingleNestedAttribute{
				Computed: true,
				Attributes: map[string]dsschema.Attribute{
					"type": str(), "cpu_count": str(), "memory": str(), "gpu_count": str(), "image": str(), "ssh_key": str(), "datacenter": str(),
					"vpc": str(), "subnet": str(), "security_group": str(), "assign_public_ip": dsschema.BoolAttribute{Computed: true},
					"shared_storage": str(), "boot_disk_size": dsschema.Int64Attribute{Computed: true},
					"gpu_model": str(), "gpu_vendor": str(), "vm_id": str(),
				},
			},
			"status": dsschema.SingleNestedAttribute{
				Computed: true,
				Attributes: map[string]dsschema.Attribute{
					"phase": str(), "status": str(), "reason": str(), "action": str(), "power_state": str(),
					"host_name": str(), "instance_id": str(), "os_name": str(), "private_ip": str(), "public_ip": str(),
					"server_host": str(), "user_name": str(),
				},
			},
		},
	}
}

func (d *vmDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.pd = providerData(req.ProviderData, &resp.Diagnostics)
}

func (d *vmDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	if d.pd == nil {
		return
	}
	var cfg vmDataSourceModel
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("name"), &cfg.Name)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("project"), &cfg.Project)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("workspace"), &cfg.Workspace)...)
	if resp.Diagnostics.HasError() {
		return
	}
	project, workspace, name := cfg.Project.ValueString(), stringOr(cfg.Workspace, ""), cfg.Name.ValueString()
	var machines interface {
		Get(context.Context, string, gpupaas.GetOptions) (*apiv1.VMInstance, error)
	}
	if workspace == "" {
		machines = d.pd.Clientset.V1alpha1().VMInstances(project)
	} else {
		machines = d.pd.Clientset.V1alpha1().Workspaces(project).VMInstances(workspace)
	}
	srv, err := machines.Get(ctx, name, gpupaas.GetOptions{})
	if err != nil {
		handleAPIError(&resp.Diagnostics, err, "Read VM", nil)
		return
	}
	// An empty spec marks the read as an import, so every field takes the server value.
	m := vmModelFromServer(ctx, vmResourceModel{Metadata: MetadataModel{Name: cfg.Name, Project: cfg.Project, Workspace: cfg.Workspace}}, srv, false, &resp.Diagnostics)
	cfg.ID = types.StringValue(vmID(project, workspace, name))
	cfg.Metadata = m.Metadata
	cfg.Spec = vmDataSpecModel{
		Type: m.Spec.Type, CPUCount: m.Spec.CPUCount, Memory: m.Spec.Memory, GPUCount: m.Spec.GPUCount, Image: m.Spec.Image, SSHKey: m.Spec.SSHKey,
		Datacenter: m.Spec.Datacenter, VPC: m.Spec.VPC, Subnet: m.Spec.Subnet, SecurityGroup: m.Spec.SecurityGroup, AssignPublicIP: m.Spec.AssignPublicIP,
		SharedStorage: m.Spec.SharedStorage, BootDiskSize: m.Spec.BootDiskSize, GPUModel: m.Spec.GPUModel, GPUVendor: m.Spec.GPUVendor, VMID: m.Spec.VMID,
	}
	cfg.Status = m.Status
	resp.Diagnostics.Append(resp.State.Set(ctx, &cfg)...)
}

// --- SKU -------------------------------------------------------------------

// NewVMProfileDataSource constructs the gpupaas_virtual_machine_sku data source.
func NewVMProfileDataSource() datasource.DataSource { return &vmProfileDataSource{} }

type vmProfileDataSource struct{ pd *client.ProviderData }

type vmPresetModel struct {
	Name   types.String `tfsdk:"name"`
	CPU    types.String `tfsdk:"cpu"`
	Memory types.String `tfsdk:"memory"`
	GPU    types.String `tfsdk:"gpu"`
}

type vmProfileDataSourceModel struct {
	ID                      types.String    `tfsdk:"id"`
	Name                    types.String    `tfsdk:"name"`
	Project                 types.String    `tfsdk:"project"`
	Images                  types.List      `tfsdk:"images"`
	DefaultImage            types.String    `tfsdk:"default_image"`
	Datacenters             types.List      `tfsdk:"datacenters"`
	GPUsEnabled             types.Bool      `tfsdk:"gpus_enabled"`
	Presets                 []vmPresetModel `tfsdk:"presets"`
	GPUName                 types.String    `tfsdk:"gpu_name"`
	GPUModel                types.String    `tfsdk:"gpu_model"`
	GPUVendor               types.String    `tfsdk:"gpu_vendor"`
	BootDiskMin             types.Int64     `tfsdk:"boot_disk_min"`
	BootDiskMax             types.Int64     `tfsdk:"boot_disk_max"`
	BootDiskIncrement       types.Int64     `tfsdk:"boot_disk_increment"`
	PublicIP                types.String    `tfsdk:"public_ip"`
	InstanceType            types.String    `tfsdk:"instance_type"`
	SharedFilesystemEnabled types.Bool      `tfsdk:"shared_filesystem_enabled"`
	Agents                  types.List      `tfsdk:"agents"`
	Status                  types.String    `tfsdk:"status"`
	TemplateVersion         types.String    `tfsdk:"template_version"`
}

func (d *vmProfileDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_virtual_machine_sku"
}

func (d *vmProfileDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	strList := func(desc string) dsschema.Attribute {
		return dsschema.ListAttribute{Computed: true, ElementType: types.StringType, MarkdownDescription: desc}
	}
	resp.Schema = dsschema.Schema{
		MarkdownDescription: "A VM SKU (VmProfile): the images, sizes (presets), datacenters and options a `gpupaas_virtual_machine` created from it may use.",
		Attributes: map[string]dsschema.Attribute{
			"id":            dsschema.StringAttribute{Computed: true},
			"name":          dsschema.StringAttribute{Required: true},
			"project":       dsschema.StringAttribute{Required: true, MarkdownDescription: "Project that holds the SKU, usually `system-catalog`."},
			"images":        strList("Images VMs of this SKU may use."),
			"default_image": dsschema.StringAttribute{Computed: true},
			"datacenters":   strList("Datacenters VMs of this SKU may be placed in."),
			"gpus_enabled":  dsschema.BoolAttribute{Computed: true, MarkdownDescription: "Whether the SKU has GPUs; then `presets` are its GPU presets, otherwise its CPU presets."},
			"presets": dsschema.ListNestedAttribute{
				Computed:            true,
				MarkdownDescription: "The sizes a VM of this SKU may be created with. Use a name as `spec.preset` of `gpupaas_virtual_machine`.",
				NestedObject: dsschema.NestedAttributeObject{Attributes: map[string]dsschema.Attribute{
					"name": dsschema.StringAttribute{Computed: true}, "cpu": dsschema.StringAttribute{Computed: true},
					"memory": dsschema.StringAttribute{Computed: true}, "gpu": dsschema.StringAttribute{Computed: true},
				}},
			},
			"gpu_name":                  dsschema.StringAttribute{Computed: true},
			"gpu_model":                 dsschema.StringAttribute{Computed: true},
			"gpu_vendor":                dsschema.StringAttribute{Computed: true},
			"boot_disk_min":             dsschema.Int64Attribute{Computed: true, MarkdownDescription: "Smallest boot disk in GB."},
			"boot_disk_max":             dsschema.Int64Attribute{Computed: true, MarkdownDescription: "Largest boot disk in GB."},
			"boot_disk_increment":       dsschema.Int64Attribute{Computed: true, MarkdownDescription: "Boot disk sizes step by this many GB above the minimum."},
			"public_ip":                 dsschema.StringAttribute{Computed: true, MarkdownDescription: "Public IP mode: `None`, `Always` or `Optional`."},
			"instance_type":             dsschema.StringAttribute{Computed: true},
			"shared_filesystem_enabled": dsschema.BoolAttribute{Computed: true},
			"agents": strList("Orchestration agents that run the SKU's workflows. Provisioning fails with \"no healthy agents found\" when none of them is healthy; " +
				"the list is not a health check."),
			"status":           dsschema.StringAttribute{Computed: true},
			"template_version": dsschema.StringAttribute{Computed: true},
		},
	}
}

func (d *vmProfileDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.pd = providerData(req.ProviderData, &resp.Diagnostics)
}

func (d *vmProfileDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	if d.pd == nil {
		return
	}
	var cfg vmProfileDataSourceModel
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("name"), &cfg.Name)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("project"), &cfg.Project)...)
	if resp.Diagnostics.HasError() {
		return
	}
	project, name := cfg.Project.ValueString(), cfg.Name.ValueString()
	p, err := getVMProfile(ctx, d.pd.Clientset.V1alpha1(), project, name)
	if err != nil {
		handleAPIError(&resp.Diagnostics, err, "Read VM profile", nil)
		return
	}
	s := p.Spec
	cfg.ID = types.StringValue(project + "/" + name)
	cfg.Images = listFromStringSlice(s.Image)
	cfg.DefaultImage = nullableString(s.ImageDefault)
	cfg.Datacenters = listFromStringSlice(s.Datacenter)
	cfg.GPUsEnabled = types.BoolValue(s.GPUsEnabled)
	cfg.Presets = nil
	for _, row := range s.Sizes() {
		cfg.Presets = append(cfg.Presets, vmPresetModel{Name: nullableString(row.Name), CPU: nullableString(row.CPU), Memory: nullableString(row.Memory), GPU: nullableString(row.GPU)})
	}
	cfg.GPUName, cfg.GPUModel, cfg.GPUVendor = nullableString(s.GPUName), nullableString(s.GPUModel), nullableString(s.GPUVendor)
	cfg.BootDiskMin, cfg.BootDiskMax, cfg.BootDiskIncrement = types.Int64Null(), types.Int64Null(), types.Int64Null()
	if bd := s.BootDisk; bd != nil {
		if v, ok := numeric(bd.MinSize); ok {
			cfg.BootDiskMin = types.Int64Value(int64(v))
		}
		if v, ok := numeric(bd.MaxSize); ok {
			cfg.BootDiskMax = types.Int64Value(int64(v))
		}
		if v, ok := numeric(bd.Increment); ok {
			cfg.BootDiskIncrement = types.Int64Value(int64(v))
		}
	}
	cfg.PublicIP = nullableString(s.PublicIP)
	cfg.InstanceType = nullableString(s.InstanceType)
	cfg.SharedFilesystemEnabled = types.BoolValue(s.SharedFilesystemEnabled)
	var agents []string
	if gs := p.Status.GlobalSettings; gs != nil {
		agents = gs.Agents
	}
	cfg.Agents = listFromStringSlice(agents)
	cfg.Status = nullableString(p.Status.Status)
	cfg.TemplateVersion = nullableString(s.TemplateVersion)
	resp.Diagnostics.Append(resp.State.Set(ctx, &cfg)...)
}
