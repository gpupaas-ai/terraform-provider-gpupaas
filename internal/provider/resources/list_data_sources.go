package resources

import (
	"context"
	"fmt"
	"sort"
	"strings"

	gpupaas "github.com/gpupaas-ai/gpupaas-go"
	apiv1 "github.com/gpupaas-ai/gpupaas-go/apis/v1alpha1"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	dsschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/gpupaas-ai/terraform-provider-gpupaas/internal/client"
)

// The list data sources return the objects of a project, or of one workspace of it, so that a configuration can
// discover what exists (and `for_each` over it) instead of naming each object. They all have one shape:
//
//	project   (required)
//	workspace (optional, where the kind lives in workspaces): only that workspace; unset lists the whole project,
//	          workspaces included
//	names     the names, sorted
//	items     one object per entry with a few identifying fields, all strings, sorted by name
//
// An item never carries credentials or the spec of the object: look one up by name with the singular data
// source for the rest. SKU lists carry no workspace argument, because SKUs belong to the project.

// listField is one attribute of an item.
type listField struct{ name, description string }

// listKind describes one list data source.
type listKind struct {
	suffix string // the type name after the provider prefix, plural
	title  string // for descriptions
	// scoped is true when the kind lives in workspaces and the data source takes the optional workspace argument.
	scoped bool
	fields []listField
	fetch  func(ctx context.Context, pd *client.ProviderData, project, workspace string) ([]map[string]string, error)
	// note adds to the description.
	note string
}

var (
	fieldName      = listField{"name", "Name."}
	fieldWorkspace = listField{"workspace", "Workspace that owns it; empty for a project-scoped one."}
	fieldSKU       = listField{"sku", "The SKU (profile) it was created from."}
	fieldStatus    = listField{"status", "Normalized platform status, such as `running`, `success`, `in_progress`, `failed`."}
	fieldDesc      = listField{"description", "Description."}
)

var listKinds = []listKind{
	{suffix: "workspaces", title: "workspaces", fields: []listField{fieldName, {"display_name", "Display name."}, fieldDesc}, fetch: listWorkspaces},
	{suffix: "ssh_keys", title: "SSH keys", scoped: true, fields: []listField{fieldName, fieldWorkspace, {"public_key", "The OpenSSH public key."}}, fetch: listSshKeys},
	{suffix: "security_groups", title: "security groups", scoped: true, fields: []listField{fieldName, fieldWorkspace}, fetch: listSecurityGroups},
	{suffix: "virtual_machines", title: "virtual machines", scoped: true, fields: []listField{fieldName, fieldWorkspace, fieldSKU, fieldStatus}, fetch: listVMs},
	{suffix: "bare_metal_instances", title: "bare-metal instances", scoped: true, fields: []listField{fieldName, fieldWorkspace, fieldSKU, fieldStatus}, fetch: listBareMetals},
	{suffix: "kubernetes_clusters", title: "Kubernetes clusters", scoped: true, fields: []listField{fieldName, fieldWorkspace, fieldSKU, {"kubernetes_version", "Kubernetes version."}, fieldStatus}, fetch: listClusters},
	{suffix: "high_speed_storages", title: "high-speed storage volumes", scoped: true, fields: storageListFields, fetch: listStorageOf(highSpeedStorage),
		note: " It relies on the controller's `serviceType` list filter; volumes of a custom storage profile are in neither storage list."},
	{suffix: "object_storages", title: "object storage volumes", scoped: true, fields: storageListFields, fetch: listStorageOf(objectStorage),
		note: " It relies on the controller's `serviceType` list filter; volumes of a custom storage profile are in neither storage list."},
	{suffix: "virtual_machine_skus", title: "virtual machine SKUs", fields: []listField{fieldName, fieldDesc}, fetch: listVMSKUs},
	{suffix: "bare_metal_skus", title: "bare-metal SKUs", fields: []listField{fieldName, fieldDesc}, fetch: listBareMetalSKUs},
	{suffix: "kubernetes_skus", title: "Kubernetes cluster SKUs", fields: []listField{fieldName, fieldDesc}, fetch: listKubernetesSKUs},
}

var storageListFields = []listField{fieldName, fieldWorkspace, {"sku", "The storage SKU (profile) it was provisioned from."}, {"size", "Capacity in the unit the profile defines."}, fieldStatus}

// ListDataSources returns the constructors of every list data source.
func ListDataSources() []func() datasource.DataSource {
	out := make([]func() datasource.DataSource, 0, len(listKinds))
	for _, k := range listKinds {
		k := k
		out = append(out, func() datasource.DataSource { return &listDataSource{kind: k} })
	}
	return out
}

var (
	_ datasource.DataSource              = (*listDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*listDataSource)(nil)
)

type listDataSource struct {
	pd   *client.ProviderData
	kind listKind
}

func (d *listDataSource) itemType() types.ObjectType {
	attrs := map[string]attr.Type{}
	for _, f := range d.kind.fields {
		attrs[f.name] = types.StringType
	}
	return types.ObjectType{AttrTypes: attrs}
}

func (d *listDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_" + d.kind.suffix
}

func (d *listDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	item := map[string]dsschema.Attribute{}
	for _, f := range d.kind.fields {
		item[f.name] = dsschema.StringAttribute{Computed: true, MarkdownDescription: f.description}
	}
	attrs := map[string]dsschema.Attribute{
		"id":      dsschema.StringAttribute{Computed: true},
		"project": dsschema.StringAttribute{Required: true, MarkdownDescription: "Project to list."},
		"names":   dsschema.ListAttribute{Computed: true, ElementType: types.StringType, MarkdownDescription: "Names of the " + d.kind.title + ", sorted."},
		"items": dsschema.ListNestedAttribute{
			Computed:            true,
			MarkdownDescription: "The " + d.kind.title + ", sorted by name, with a few identifying fields. Look one up by name for the rest.",
			NestedObject:        dsschema.NestedAttributeObject{Attributes: item},
		},
	}
	desc := "List the " + d.kind.title + " of a project"
	if d.kind.scoped {
		attrs["workspace"] = dsschema.StringAttribute{Optional: true, MarkdownDescription: "Only the entries of this workspace. Unset lists the whole project, the entries of its workspaces included."}
		desc += ", or of one workspace"
	}
	resp.Schema = dsschema.Schema{MarkdownDescription: desc + "." + d.kind.note, Attributes: attrs}
}

func (d *listDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.pd = providerData(req.ProviderData, &resp.Diagnostics)
}

func (d *listDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	if d.pd == nil {
		return
	}
	var project, workspace types.String
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("project"), &project)...)
	if d.kind.scoped {
		resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("workspace"), &workspace)...)
	}
	if resp.Diagnostics.HasError() {
		return
	}
	p, w := project.ValueString(), stringOr(workspace, "")
	rows, err := d.kind.fetch(ctx, d.pd, p, w)
	if err != nil {
		handleAPIError(&resp.Diagnostics, err, "List "+d.kind.title, nil)
		return
	}
	// A stable order keeps a plan from changing when the platform returns the same entries in another order.
	sort.SliceStable(rows, func(i, j int) bool { return rows[i]["name"] < rows[j]["name"] })
	names := make([]attr.Value, 0, len(rows))
	items := make([]attr.Value, 0, len(rows))
	for _, row := range rows {
		names = append(names, types.StringValue(row["name"]))
		vals := map[string]attr.Value{}
		for _, f := range d.kind.fields {
			vals[f.name] = types.StringValue(row[f.name])
		}
		obj, diags := types.ObjectValue(d.itemType().AttrTypes, vals)
		resp.Diagnostics.Append(diags...)
		items = append(items, obj)
	}
	nameList, diags := types.ListValue(types.StringType, names)
	resp.Diagnostics.Append(diags...)
	itemList, diags := types.ListValue(d.itemType(), items)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	id := p
	if w != "" {
		id += "/" + w
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), id)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("project"), p)...)
	if d.kind.scoped {
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("workspace"), workspace)...)
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("names"), nameList)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("items"), itemList)...)
}

// listAll reads every page of a list. Unlike listPages it fails when any page fails, and it keeps following the
// continue marker over an empty page (a controller that filters after paging returns such pages): a list that was cut
// short would look like a complete one. It ends at an empty marker or a marker that repeats, which is how a
// controller says there is nothing more.
func listAll[T any](fetch func(opts gpupaas.ListOptions) (items []T, next string, err error)) ([]T, error) {
	var all []T
	opts := gpupaas.ListOptions{Limit: "100"}
	for page := 0; ; page++ {
		if page >= maxListPages {
			return nil, fmt.Errorf("the list has more than %d pages", maxListPages)
		}
		items, next, err := fetch(opts)
		if err != nil {
			return nil, err
		}
		all = append(all, items...)
		if next == "" || next == opts.Continue {
			return all, nil
		}
		opts.Continue = next
	}
}

func listWorkspaces(ctx context.Context, pd *client.ProviderData, project, _ string) ([]map[string]string, error) {
	items, err := listAll(func(opts gpupaas.ListOptions) ([]apiv1.Workspace, string, error) {
		l, err := pd.Clientset.V1alpha1().Workspaces(project).List(ctx, opts)
		if err != nil {
			return nil, "", err
		}
		return l.Items, l.Metadata.Continue, nil
	})
	rows := make([]map[string]string, 0, len(items))
	for _, i := range items {
		rows = append(rows, map[string]string{"name": i.Metadata.Name, "display_name": i.Metadata.DisplayName, "description": i.Metadata.Description})
	}
	return rows, err
}

func listSshKeys(ctx context.Context, pd *client.ProviderData, project, workspace string) ([]map[string]string, error) {
	cs := pd.Clientset.V1alpha1()
	api := cs.SshKeys(project)
	if workspace != "" {
		api = cs.Workspaces(project).SshKeys(workspace)
	}
	items, err := listAll(func(opts gpupaas.ListOptions) ([]apiv1.SshKey, string, error) {
		l, err := api.List(ctx, opts)
		if err != nil {
			return nil, "", err
		}
		return l.Items, l.Metadata.Continue, nil
	})
	rows := make([]map[string]string, 0, len(items))
	for _, i := range items {
		rows = append(rows, map[string]string{"name": i.Metadata.Name, "workspace": i.Metadata.Workspace, "public_key": i.Spec.PublicKey})
	}
	return rows, err
}

func listSecurityGroups(ctx context.Context, pd *client.ProviderData, project, workspace string) ([]map[string]string, error) {
	cs := pd.Clientset.V1alpha1()
	api := cs.SecurityGroups(project)
	if workspace != "" {
		api = cs.Workspaces(project).SecurityGroups(workspace)
	}
	items, err := listAll(func(opts gpupaas.ListOptions) ([]apiv1.SecurityGroup, string, error) {
		l, err := api.List(ctx, opts)
		if err != nil {
			return nil, "", err
		}
		return l.Items, l.Metadata.Continue, nil
	})
	rows := make([]map[string]string, 0, len(items))
	for _, i := range items {
		rows = append(rows, map[string]string{"name": i.Metadata.Name, "workspace": i.Metadata.Workspace})
	}
	return rows, err
}

func listVMs(ctx context.Context, pd *client.ProviderData, project, workspace string) ([]map[string]string, error) {
	cs := pd.Clientset.V1alpha1()
	api := cs.VMInstances(project)
	if workspace != "" {
		api = cs.Workspaces(project).VMInstances(workspace)
	}
	items, err := listAll(func(opts gpupaas.ListOptions) ([]apiv1.VMInstance, string, error) {
		l, err := api.List(ctx, opts)
		if err != nil {
			return nil, "", err
		}
		return l.Items, l.Metadata.Continue, nil
	})
	rows := make([]map[string]string, 0, len(items))
	for _, i := range items {
		rows = append(rows, map[string]string{"name": i.Metadata.Name, "workspace": i.Metadata.Workspace, "sku": i.Spec.Type, "status": i.Status.Phase()})
	}
	return rows, err
}

func listBareMetals(ctx context.Context, pd *client.ProviderData, project, workspace string) ([]map[string]string, error) {
	cs := pd.Clientset.V1alpha1()
	api := cs.BareMetals(project)
	if workspace != "" {
		api = cs.Workspaces(project).BareMetals(workspace)
	}
	items, err := listAll(func(opts gpupaas.ListOptions) ([]apiv1.BareMetal, string, error) {
		l, err := api.List(ctx, opts)
		if err != nil {
			return nil, "", err
		}
		return l.Items, l.Metadata.Continue, nil
	})
	rows := make([]map[string]string, 0, len(items))
	for _, i := range items {
		rows = append(rows, map[string]string{"name": i.Metadata.Name, "workspace": i.Metadata.Workspace, "sku": i.Spec.Type, "status": i.Status.Phase()})
	}
	return rows, err
}

func listClusters(ctx context.Context, pd *client.ProviderData, project, workspace string) ([]map[string]string, error) {
	cs := pd.Clientset.V1alpha1()
	api := cs.MKSClusters(project)
	if workspace != "" {
		api = cs.Workspaces(project).MKSClusters(workspace)
	}
	items, err := listAll(func(opts gpupaas.ListOptions) ([]apiv1.MKSCluster, string, error) {
		l, err := api.List(ctx, opts)
		if err != nil {
			return nil, "", err
		}
		return l.Items, l.Metadata.Continue, nil
	})
	rows := make([]map[string]string, 0, len(items))
	for _, i := range items {
		sku := ""
		if i.Spec.Profile != nil {
			sku = i.Spec.Profile.Name
		}
		rows = append(rows, map[string]string{"name": i.Metadata.Name, "workspace": i.Metadata.Workspace, "sku": sku,
			"kubernetes_version": i.Spec.KubernetesVersion, "status": i.Status.Phase()})
	}
	return rows, err
}

// listStorageOf lists the volumes of one storage family: the controller filters by serviceType, which it
// stamps from the profile when a volume is created.
func listStorageOf(kind storageKind) func(context.Context, *client.ProviderData, string, string) ([]map[string]string, error) {
	return func(ctx context.Context, pd *client.ProviderData, project, workspace string) ([]map[string]string, error) {
		api := storages(pd, project, workspace)
		items, err := listAll(func(opts gpupaas.ListOptions) ([]apiv1.Storage, string, error) {
			opts.ServiceType = kind.serviceType
			l, err := api.List(ctx, opts)
			if err != nil {
				return nil, "", err
			}
			return l.Items, l.Metadata.Continue, nil
		})
		rows := make([]map[string]string, 0, len(items))
		for _, i := range items {
			rows = append(rows, map[string]string{"name": i.Metadata.Name, "workspace": i.Metadata.Workspace, "sku": i.Spec.Type,
				"size": i.Spec.Size, "status": i.Status.Phase()})
		}
		return rows, err
	}
}

func skuRows[T any](items []T, meta func(T) apiv1.ObjectMeta) []map[string]string {
	rows := make([]map[string]string, 0, len(items))
	for _, i := range items {
		m := meta(i)
		rows = append(rows, map[string]string{"name": m.Name, "description": strings.TrimSpace(m.Description)})
	}
	return rows
}

func listVMSKUs(ctx context.Context, pd *client.ProviderData, project, _ string) ([]map[string]string, error) {
	items, err := listAll(func(opts gpupaas.ListOptions) ([]apiv1.VMProfile, string, error) {
		l, err := pd.Clientset.V1alpha1().VMProfiles(project).List(ctx, opts)
		if err != nil {
			return nil, "", err
		}
		return l.Items, l.Metadata.Continue, nil
	})
	return skuRows(items, func(p apiv1.VMProfile) apiv1.ObjectMeta { return p.Metadata }), err
}

func listBareMetalSKUs(ctx context.Context, pd *client.ProviderData, project, _ string) ([]map[string]string, error) {
	items, err := listAll(func(opts gpupaas.ListOptions) ([]apiv1.BmProfile, string, error) {
		l, err := pd.Clientset.V1alpha1().BmProfiles(project).List(ctx, opts)
		if err != nil {
			return nil, "", err
		}
		return l.Items, l.Metadata.Continue, nil
	})
	return skuRows(items, func(p apiv1.BmProfile) apiv1.ObjectMeta { return p.Metadata }), err
}

func listKubernetesSKUs(ctx context.Context, pd *client.ProviderData, project, _ string) ([]map[string]string, error) {
	items, err := listAll(func(opts gpupaas.ListOptions) ([]apiv1.MKSClusterProfile, string, error) {
		l, err := pd.Clientset.V1alpha1().MKSClusterProfiles(project).List(ctx, opts)
		if err != nil {
			return nil, "", err
		}
		return l.Items, l.Metadata.Continue, nil
	})
	return skuRows(items, func(p apiv1.MKSClusterProfile) apiv1.ObjectMeta { return p.Metadata }), err
}
