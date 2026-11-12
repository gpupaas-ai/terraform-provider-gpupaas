package resources

import (
	"context"

	gpupaas "github.com/gpupaas-ai/gpupaas-go"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	dsschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/gpupaas-ai/terraform-provider-gpupaas/internal/client"
)

var (
	_ datasource.DataSource              = (*storageDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*storageDataSource)(nil)
)

// NewHighSpeedStorageDataSource constructs the gpupaas_high_speed_storage data source.
func NewHighSpeedStorageDataSource() datasource.DataSource {
	return &storageDataSource{kind: highSpeedStorage}
}

// NewObjectStorageDataSource constructs the gpupaas_object_storage data source.
func NewObjectStorageDataSource() datasource.DataSource {
	return &storageDataSource{kind: objectStorage}
}

type storageDataSource struct {
	pd   *client.ProviderData
	kind storageKind
}

type storageDataSpecModel struct {
	Type                      types.String `tfsdk:"type"`
	Size                      types.String `tfsdk:"size"`
	Datacenter                types.String `tfsdk:"datacenter"`
	StorageType               types.String `tfsdk:"storage_type"`
	AccessPolicy              types.String `tfsdk:"access_policy"`
	ContractTerm              types.String `tfsdk:"contract_term"`
	EnableEncryptionAtRest    types.Bool   `tfsdk:"enable_encryption_at_rest"`
	EnableEncryptionInTransit types.Bool   `tfsdk:"enable_encryption_in_transit"`
}

type storageDataSourceModel struct {
	ID        types.String         `tfsdk:"id"`
	Name      types.String         `tfsdk:"name"`
	Project   types.String         `tfsdk:"project"`
	Workspace types.String         `tfsdk:"workspace"`
	Metadata  MetadataModel        `tfsdk:"metadata"`
	Spec      storageDataSpecModel `tfsdk:"spec"`
	Status    storageStatusModel   `tfsdk:"status"`
}

func (d *storageDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_" + d.kind.suffix
}

func (d *storageDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	str := func(desc string) dsschema.StringAttribute {
		return dsschema.StringAttribute{Computed: true, MarkdownDescription: desc}
	}
	resp.Schema = dsschema.Schema{
		MarkdownDescription: "Look up a " + d.kind.title + " volume by name within a project, optionally scoped to a workspace. " +
			"Fails when the volume belongs to the other storage family.",
		Attributes: map[string]dsschema.Attribute{
			"id":        dsschema.StringAttribute{Computed: true},
			"name":      dsschema.StringAttribute{Required: true},
			"project":   dsschema.StringAttribute{Required: true},
			"workspace": dsschema.StringAttribute{Optional: true, MarkdownDescription: "Workspace that owns the volume; omit for a project-scoped one."},
			"metadata":  MetadataDataSourceAttribute(),
			"spec": dsschema.SingleNestedAttribute{
				Computed: true,
				Attributes: map[string]dsschema.Attribute{
					"type":                         str("Storage profile (SKU) the volume was provisioned from."),
					"size":                         str("Capacity in the unit the profile defines (GB unless it says GiB); unset for a profile with a fixed volume."),
					"datacenter":                   str("Datacenter the volume lives in."),
					"storage_type":                 str("Classification: `block`, `file` or `object`."),
					"access_policy":                str("Access policy."),
					"contract_term":                str("Contract term."),
					"enable_encryption_at_rest":    dsschema.BoolAttribute{Computed: true},
					"enable_encryption_in_transit": dsschema.BoolAttribute{Computed: true},
				},
			},
			"status": dsschema.SingleNestedAttribute{
				Computed: true,
				Attributes: map[string]dsschema.Attribute{
					"status": dsschema.StringAttribute{Computed: true},
					"reason": dsschema.StringAttribute{Computed: true},
					"action": dsschema.StringAttribute{Computed: true},
					"output": dsschema.MapAttribute{Computed: true, Sensitive: true, ElementType: types.StringType,
						MarkdownDescription: "What the platform reports about the volume (servers, mount or S3 credentials, size in bytes), as strings. The platform returns credentials in plain text, so the whole map is sensitive."},
				},
			},
		},
	}
}

func (d *storageDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.pd = providerData(req.ProviderData, &resp.Diagnostics)
}

func (d *storageDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	if d.pd == nil {
		return
	}
	var cfg storageDataSourceModel
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("name"), &cfg.Name)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("project"), &cfg.Project)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("workspace"), &cfg.Workspace)...)
	if resp.Diagnostics.HasError() {
		return
	}
	project, workspace, name := cfg.Project.ValueString(), stringOr(cfg.Workspace, ""), cfg.Name.ValueString()
	srv, err := storages(d.pd, project, workspace).Get(ctx, name, gpupaas.GetOptions{})
	if err != nil {
		handleAPIError(&resp.Diagnostics, err, "Read volume", nil)
		return
	}
	if inOwn, inOther := storageFamily(ctx, d.pd, d.kind, project, workspace, name); inOther && !inOwn {
		resp.Diagnostics.AddError("Wrong storage data source", wrongFamilyMessage(d.kind, srv.Spec.Type, name))
		return
	}
	cfg.ID = types.StringValue(scopedID(project, workspace, name))
	cfg.Metadata = metadataFromSDK(srv.Metadata)
	cfg.Spec = storageDataSpecModel{
		Type:                      nullableString(srv.Spec.Type),
		Size:                      nullableString(srv.Spec.Size),
		Datacenter:                nullableString(srv.Spec.Datacenter),
		StorageType:               nullableString(srv.Spec.StorageType),
		AccessPolicy:              nullableString(srv.Spec.AccessPolicy),
		ContractTerm:              nullableString(srv.Spec.ContractTerm),
		EnableEncryptionAtRest:    boolFromWire(srv.Spec.EnableEncryptionAtRest),
		EnableEncryptionInTransit: boolFromWire(srv.Spec.EnableEncryptionInTransit),
	}
	cfg.Status = storageStatusFromServer(srv)
	resp.Diagnostics.Append(resp.State.Set(ctx, &cfg)...)
}
