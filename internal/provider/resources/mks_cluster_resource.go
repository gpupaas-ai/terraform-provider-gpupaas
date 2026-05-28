package resources

import (
	"context"

	gpupaas "github.com/gpupaas-ai/gpupaas-go"
	apiv1 "github.com/gpupaas-ai/gpupaas-go/apis/v1alpha1"
	typed "github.com/gpupaas-ai/gpupaas-go/clientset/typed/v1alpha1"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/gpupaas-ai/terraform-provider-gpupaas/internal/client"
)

var (
	_ resource.Resource                = (*mksClusterResource)(nil)
	_ resource.ResourceWithImportState = (*mksClusterResource)(nil)
	_ resource.ResourceWithConfigure   = (*mksClusterResource)(nil)
)

// mksClusterDesiredActionValues is the canonical allow-list mirroring the
// cluster-level verbs exposed by typed.MKSClusterInterface (Upgrade,
// ScaleNodeGroup, AddNodeGroup, RemoveNodeGroup). "none" is the explicit no-op.
//
// Node-level verbs (drain, cordon, uncordon) live on MKSNodeInterface, not on
// the cluster, so they are intentionally excluded here — they belong to a node
// resource, not gpupaas_mks_cluster.
var mksClusterDesiredActionValues = []string{
	"none", "upgrade", "scale_node_group", "add_node_group", "remove_node_group",
}

// NewMKSClusterResource constructs the gpupaas_mks_cluster resource.
func NewMKSClusterResource() resource.Resource { return &mksClusterResource{} }

type mksClusterResource struct{ pd *client.ProviderData }

type mksClusterResourceModel struct {
	ID            types.String            `tfsdk:"id"`
	APIVersion    types.String            `tfsdk:"api_version"`
	Kind          types.String            `tfsdk:"kind"`
	Metadata      MetadataModel           `tfsdk:"metadata"`
	Spec          mksClusterSpecModel     `tfsdk:"spec"`
	Status        mksClusterStatusModel   `tfsdk:"status"`
	DesiredAction types.String            `tfsdk:"desired_action"`
	ActionInputs  *mksClusterActionInputs `tfsdk:"action_inputs"`
}

type mksClusterSpecModel struct {
	KubernetesVersion     types.String        `tfsdk:"kubernetes_version"`
	PlatformVersion       types.String        `tfsdk:"platform_version"`
	CNI                   types.String        `tfsdk:"cni"`
	CNIVersion            types.String        `tfsdk:"cni_version"`
	OS                    types.String        `tfsdk:"os"`
	HAEnabled             types.Bool          `tfsdk:"ha_enabled"`
	DedicatedControlPlane types.Bool          `tfsdk:"dedicated_control_plane"`
	Location              types.String        `tfsdk:"location"`
	Blueprint             *mksBlueprintModel  `tfsdk:"blueprint"`
	Networking            *mksNetworkingModel `tfsdk:"networking"`
	Proxy                 *mksProxyModel      `tfsdk:"proxy"`
	Storage               *mksStorageModel    `tfsdk:"storage"`
	Tags                  types.Map           `tfsdk:"tags"`
	ControlPlaneNodeGroup *mksNodeGroupModel  `tfsdk:"control_plane_node_group"`
	WorkerNodeGroups      []mksNodeGroupModel `tfsdk:"worker_node_groups"`
	Nodes                 []mksNodeSpecModel  `tfsdk:"nodes"`
}

type mksBlueprintModel struct {
	Name    types.String `tfsdk:"name"`
	Version types.String `tfsdk:"version"`
}

type mksNetworkingModel struct {
	VPC            types.String `tfsdk:"vpc"`
	Subnet         types.String `tfsdk:"subnet"`
	PodCIDR        types.String `tfsdk:"pod_cidr"`
	ServiceCIDR    types.String `tfsdk:"service_cidr"`
	IPFamily       types.String `tfsdk:"ip_family"`
	PodCIDRV6      types.String `tfsdk:"pod_cidr_v6"`
	ServiceCIDRV6  types.String `tfsdk:"service_cidr_v6"`
	SecurityGroups types.List   `tfsdk:"security_groups"`
}

type mksProxyModel struct {
	HTTPProxy    types.String `tfsdk:"http_proxy"`
	HTTPSProxy   types.String `tfsdk:"https_proxy"`
	NoProxy      types.String `tfsdk:"no_proxy"`
	ProxyRootCA  types.String `tfsdk:"proxy_root_ca"`
	TLSTerminate types.Bool   `tfsdk:"tls_terminate"`
}

type mksStorageModel struct {
	Block               *mksStorageBackendModel `tfsdk:"block"`
	SharedFS            *mksStorageBackendModel `tfsdk:"shared_fs"`
	Object              *mksStorageBackendModel `tfsdk:"object"`
	HighSpeed           *mksStorageBackendModel `tfsdk:"high_speed"`
	DefaultStorageClass types.String            `tfsdk:"default_storage_class"`
}

type mksStorageBackendModel struct {
	Type          types.String `tfsdk:"type"`
	AccessMode    types.String `tfsdk:"access_mode"`
	ReclaimPolicy types.String `tfsdk:"reclaim_policy"`
	Config        types.Map    `tfsdk:"config"`
}

type mksNodeGroupModel struct {
	ID              types.String           `tfsdk:"id"`
	SKU             types.String           `tfsdk:"sku"`
	ScalingMode     types.String           `tfsdk:"scaling_mode"`
	NodeCount       types.Int64            `tfsdk:"node_count"`
	MinNodes        types.Int64            `tfsdk:"min_nodes"`
	MaxNodes        types.Int64            `tfsdk:"max_nodes"`
	DesiredNodes    types.Int64            `tfsdk:"desired_nodes"`
	PublicIP        types.Bool             `tfsdk:"public_ip"`
	SSHKey          types.String           `tfsdk:"ssh_key"`
	UserData        types.String           `tfsdk:"user_data"`
	NodeLabels      types.Map              `tfsdk:"node_labels"`
	NodeAnnotations types.Map              `tfsdk:"node_annotations"`
	KubeletConfig   *mksKubeletConfigModel `tfsdk:"kubelet_config"`
}

type mksKubeletConfigModel struct {
	KeyValue types.Map    `tfsdk:"key_value"`
	YAML     types.String `tfsdk:"yaml"`
}

type mksNodeSpecModel struct {
	Hostname        types.String           `tfsdk:"hostname"`
	InventorySource types.String           `tfsdk:"inventory_source"`
	Roles           types.List             `tfsdk:"roles"`
	SSHKey          types.String           `tfsdk:"ssh_key"`
	SSHUserName     types.String           `tfsdk:"ssh_user_name"`
	SSHPort         types.Int64            `tfsdk:"ssh_port"`
	PrivateIP       types.String           `tfsdk:"private_ip"`
	Arch            types.String           `tfsdk:"arch"`
	OperatingSystem types.String           `tfsdk:"operating_system"`
	Interface       types.String           `tfsdk:"interface"`
	NodeLabels      types.Map              `tfsdk:"node_labels"`
	NodeAnnotations types.Map              `tfsdk:"node_annotations"`
	NodeTaints      types.Map              `tfsdk:"node_taints"`
	UserData        types.String           `tfsdk:"user_data"`
	KubeletConfig   *mksKubeletConfigModel `tfsdk:"kubelet_config"`
	NodePool        types.String           `tfsdk:"node_pool"`
	SKU             types.String           `tfsdk:"sku"`
	PublicIP        types.Bool             `tfsdk:"public_ip"`
}

type mksClusterStatusModel struct {
	Condition       types.String           `tfsdk:"condition"`
	ConditionReason types.String           `tfsdk:"condition_reason"`
	Output          *mksClusterOutputModel `tfsdk:"output"`
	Action          types.String           `tfsdk:"action"`
}

type mksClusterOutputModel struct {
	APIServerEndpoint types.String `tfsdk:"api_server_endpoint"`
	ClusterIDEdgesrv  types.String `tfsdk:"cluster_id_edgesrv"`
}

// mksClusterActionInputs carries the per-action payloads accompanying
// desired_action. Each sub-block maps to a single imperative verb.
type mksClusterActionInputs struct {
	Upgrade         *mksUpgradeInputs         `tfsdk:"upgrade"`
	ScaleNodeGroup  *mksScaleNodeGroupInputs  `tfsdk:"scale_node_group"`
	AddNodeGroup    *mksAddNodeGroupInputs    `tfsdk:"add_node_group"`
	RemoveNodeGroup *mksRemoveNodeGroupInputs `tfsdk:"remove_node_group"`
}

type mksUpgradeInputs struct {
	K8sVersion      types.String `tfsdk:"k8s_version"`
	PlatformVersion types.String `tfsdk:"platform_version"`
}

type mksScaleNodeGroupInputs struct {
	NodeGroupName types.String `tfsdk:"node_group_name"`
	DesiredCount  types.Int64  `tfsdk:"desired_count"`
	MinCount      types.Int64  `tfsdk:"min_count"`
	MaxCount      types.Int64  `tfsdk:"max_count"`
}

type mksAddNodeGroupInputs struct {
	NodeGroup *mksNodeGroupModel `tfsdk:"node_group"`
}

type mksRemoveNodeGroupInputs struct {
	NodeGroupName types.String `tfsdk:"node_group_name"`
}

func (r *mksClusterResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_mks_cluster"
}

func (r *mksClusterResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "GPU PaaS managed Kubernetes cluster (`apiVersion: " + apiv1.APIVersion + "`, `kind: MKSCluster`). " +
			"Project-scoped (no workspace). Imperative lifecycle actions (upgrade, scale/add/remove " +
			"worker node groups) are driven through the `desired_action` attribute. Node-level " +
			"operations (drain/cordon/uncordon) are managed separately at the node level.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"api_version": schema.StringAttribute{Computed: true},
			"kind":        schema.StringAttribute{Computed: true},
			"metadata":    MetadataResourceAttribute(false, true),
			"spec":        mksClusterSpecAttribute(),
			"status":      mksClusterStatusAttribute(),
			"desired_action": schema.StringAttribute{
				Optional: true,
				Computed: true,
				MarkdownDescription: "Imperative lifecycle action to drive on this cluster. One of " +
					"`none`, `upgrade`, `scale_node_group`, `add_node_group`, `remove_node_group`. " +
					"Changing this attribute (from its prior planned/state value) triggers the " +
					"corresponding SDK call on the next apply. Setting it back to `none` (or " +
					"omitting it) is a no-op — the provider never auto-re-triggers actions on " +
					"refresh. Each verb requires its matching `action_inputs.<verb>` payload.",
				Validators: []validator.String{
					stringvalidator.OneOf(mksClusterDesiredActionValues...),
				},
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"action_inputs": mksClusterActionInputsAttribute(),
		},
	}
}

func mksClusterSpecAttribute() schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{
		Required: true,
		Attributes: map[string]schema.Attribute{
			"kubernetes_version": schema.StringAttribute{Optional: true, MarkdownDescription: "Kubernetes version in `major.minor` format (e.g. `1.31`)."},
			"platform_version":   schema.StringAttribute{Optional: true, MarkdownDescription: "Target platform version."},
			"cni":                schema.StringAttribute{Optional: true, MarkdownDescription: "CNI plugin (e.g. `calico`)."},
			"cni_version":        schema.StringAttribute{Optional: true, MarkdownDescription: "CNI plugin version."},
			"os":                 schema.StringAttribute{Optional: true, MarkdownDescription: "Node operating system (e.g. `ubuntu22.04`)."},
			"ha_enabled":         schema.BoolAttribute{Optional: true, MarkdownDescription: "Enable a highly-available control plane. Defaults to `false`."},
			"dedicated_control_plane": schema.BoolAttribute{
				Optional:            true,
				MarkdownDescription: "Run the control plane on dedicated nodes. Defaults to `false`.",
			},
			"location":                 schema.StringAttribute{Optional: true, MarkdownDescription: "Deployment location / region."},
			"blueprint":                mksBlueprintResourceAttribute(),
			"networking":               mksNetworkingResourceAttribute(),
			"proxy":                    mksProxyResourceAttribute(),
			"storage":                  mksStorageResourceAttribute(),
			"tags":                     schema.MapAttribute{Optional: true, ElementType: types.StringType, MarkdownDescription: "Free-form key/value tags applied to the cluster."},
			"control_plane_node_group": mksNodeGroupResourceAttribute("Control-plane node group configuration."),
			"worker_node_groups": schema.ListNestedAttribute{
				Optional:            true,
				MarkdownDescription: "Worker node groups provisioned with the cluster.",
				NestedObject:        schema.NestedAttributeObject{Attributes: mksNodeGroupResourceAttributes()},
			},
			"nodes": schema.ListNestedAttribute{
				Optional:            true,
				MarkdownDescription: "Node specifications for device-based clusters.",
				NestedObject:        schema.NestedAttributeObject{Attributes: mksNodeSpecResourceAttributes()},
			},
		},
	}
}

func mksBlueprintResourceAttribute() schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{
		Optional:            true,
		MarkdownDescription: "Blueprint applied to the cluster.",
		Attributes: map[string]schema.Attribute{
			"name":    schema.StringAttribute{Optional: true, MarkdownDescription: "Blueprint name (e.g. `minimal`)."},
			"version": schema.StringAttribute{Optional: true, MarkdownDescription: "Blueprint version (e.g. `v1`)."},
		},
	}
}

func mksNetworkingResourceAttribute() schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{
		Optional:            true,
		MarkdownDescription: "Cluster networking configuration.",
		Attributes: map[string]schema.Attribute{
			"vpc":             schema.StringAttribute{Optional: true},
			"subnet":          schema.StringAttribute{Optional: true},
			"pod_cidr":        schema.StringAttribute{Optional: true, MarkdownDescription: "Pod CIDR (e.g. `192.168.0.0/16`)."},
			"service_cidr":    schema.StringAttribute{Optional: true, MarkdownDescription: "Service CIDR (e.g. `10.96.0.0/12`)."},
			"ip_family":       schema.StringAttribute{Optional: true, MarkdownDescription: "IP family: `IPv4`, `IPv6`, or `DualStack`."},
			"pod_cidr_v6":     schema.StringAttribute{Optional: true},
			"service_cidr_v6": schema.StringAttribute{Optional: true},
			"security_groups": schema.ListAttribute{Optional: true, ElementType: types.StringType},
		},
	}
}

func mksProxyResourceAttribute() schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{
		Optional:            true,
		MarkdownDescription: "Outbound proxy configuration.",
		Attributes: map[string]schema.Attribute{
			"http_proxy":    schema.StringAttribute{Optional: true},
			"https_proxy":   schema.StringAttribute{Optional: true},
			"no_proxy":      schema.StringAttribute{Optional: true},
			"proxy_root_ca": schema.StringAttribute{Optional: true},
			"tls_terminate": schema.BoolAttribute{Optional: true},
		},
	}
}

func mksStorageBackendResourceAttribute(description string) schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{
		Optional:            true,
		MarkdownDescription: description,
		Attributes: map[string]schema.Attribute{
			"type":           schema.StringAttribute{Optional: true},
			"access_mode":    schema.StringAttribute{Optional: true},
			"reclaim_policy": schema.StringAttribute{Optional: true},
			"config":         schema.MapAttribute{Optional: true, ElementType: types.StringType},
		},
	}
}

func mksStorageResourceAttribute() schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{
		Optional:            true,
		MarkdownDescription: "Cluster storage configuration.",
		Attributes: map[string]schema.Attribute{
			"block":                 mksStorageBackendResourceAttribute("Block storage backend."),
			"shared_fs":             mksStorageBackendResourceAttribute("Shared filesystem storage backend."),
			"object":                mksStorageBackendResourceAttribute("Object storage backend."),
			"high_speed":            mksStorageBackendResourceAttribute("High-speed storage backend."),
			"default_storage_class": schema.StringAttribute{Optional: true},
		},
	}
}

func mksKubeletConfigResourceAttribute() schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{
		Optional:            true,
		MarkdownDescription: "Kubelet configuration overrides.",
		Attributes: map[string]schema.Attribute{
			"key_value": schema.MapAttribute{Optional: true, ElementType: types.StringType},
			"yaml":      schema.StringAttribute{Optional: true},
		},
	}
}

func mksNodeGroupResourceAttributes() map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"id":               schema.StringAttribute{Optional: true, MarkdownDescription: "Node group identifier."},
		"sku":              schema.StringAttribute{Optional: true},
		"scaling_mode":     schema.StringAttribute{Optional: true},
		"node_count":       schema.Int64Attribute{Optional: true},
		"min_nodes":        schema.Int64Attribute{Optional: true},
		"max_nodes":        schema.Int64Attribute{Optional: true},
		"desired_nodes":    schema.Int64Attribute{Optional: true},
		"public_ip":        schema.BoolAttribute{Optional: true},
		"ssh_key":          schema.StringAttribute{Optional: true},
		"user_data":        schema.StringAttribute{Optional: true},
		"node_labels":      schema.MapAttribute{Optional: true, ElementType: types.StringType},
		"node_annotations": schema.MapAttribute{Optional: true, ElementType: types.StringType},
		"kubelet_config":   mksKubeletConfigResourceAttribute(),
	}
}

func mksNodeGroupResourceAttribute(description string) schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{
		Optional:            true,
		MarkdownDescription: description,
		Attributes:          mksNodeGroupResourceAttributes(),
	}
}

func mksNodeSpecResourceAttributes() map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"hostname":         schema.StringAttribute{Optional: true},
		"inventory_source": schema.StringAttribute{Optional: true},
		"roles":            schema.ListAttribute{Optional: true, ElementType: types.StringType, MarkdownDescription: "Node roles: `master`, `worker`."},
		"ssh_key":          schema.StringAttribute{Optional: true, MarkdownDescription: "SSH private key (PEM format)."},
		"ssh_user_name":    schema.StringAttribute{Optional: true},
		"ssh_port":         schema.Int64Attribute{Optional: true, MarkdownDescription: "SSH port. Defaults to `22`."},
		"private_ip":       schema.StringAttribute{Optional: true},
		"arch":             schema.StringAttribute{Optional: true, MarkdownDescription: "CPU architecture: `amd64`, `arm64`."},
		"operating_system": schema.StringAttribute{Optional: true},
		"interface":        schema.StringAttribute{Optional: true},
		"node_labels":      schema.MapAttribute{Optional: true, ElementType: types.StringType},
		"node_annotations": schema.MapAttribute{Optional: true, ElementType: types.StringType},
		"node_taints":      schema.MapAttribute{Optional: true, ElementType: types.StringType},
		"user_data":        schema.StringAttribute{Optional: true},
		"kubelet_config":   mksKubeletConfigResourceAttribute(),
		"node_pool":        schema.StringAttribute{Optional: true},
		"sku":              schema.StringAttribute{Optional: true},
		"public_ip":        schema.BoolAttribute{Optional: true},
	}
}

func mksClusterActionInputsAttribute() schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{
		Optional: true,
		MarkdownDescription: "Optional payloads accompanying `desired_action`. Only the sub-block " +
			"matching the selected verb is consumed.",
		Attributes: map[string]schema.Attribute{
			"upgrade": schema.SingleNestedAttribute{
				Optional:            true,
				MarkdownDescription: "Payload for `desired_action = \"upgrade\"`.",
				Attributes: map[string]schema.Attribute{
					"k8s_version":      schema.StringAttribute{Optional: true, MarkdownDescription: "Target Kubernetes version (e.g. `1.32`)."},
					"platform_version": schema.StringAttribute{Optional: true, MarkdownDescription: "Target platform version."},
				},
			},
			"scale_node_group": schema.SingleNestedAttribute{
				Optional:            true,
				MarkdownDescription: "Payload for `desired_action = \"scale_node_group\"`.",
				Attributes: map[string]schema.Attribute{
					"node_group_name": schema.StringAttribute{Optional: true, MarkdownDescription: "Name of the worker node group to scale (required)."},
					"desired_count":   schema.Int64Attribute{Optional: true},
					"min_count":       schema.Int64Attribute{Optional: true},
					"max_count":       schema.Int64Attribute{Optional: true},
				},
			},
			"add_node_group": schema.SingleNestedAttribute{
				Optional:            true,
				MarkdownDescription: "Payload for `desired_action = \"add_node_group\"`.",
				Attributes: map[string]schema.Attribute{
					"node_group": mksNodeGroupResourceAttribute("Worker node group to add."),
				},
			},
			"remove_node_group": schema.SingleNestedAttribute{
				Optional:            true,
				MarkdownDescription: "Payload for `desired_action = \"remove_node_group\"`.",
				Attributes: map[string]schema.Attribute{
					"node_group_name": schema.StringAttribute{Optional: true, MarkdownDescription: "Name of the worker node group to remove (required)."},
				},
			},
		},
	}
}

func mksClusterStatusAttribute() schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{
		Computed: true,
		Attributes: map[string]schema.Attribute{
			"condition":        schema.StringAttribute{Computed: true},
			"condition_reason": schema.StringAttribute{Computed: true},
			"action":           schema.StringAttribute{Computed: true},
			"output": schema.SingleNestedAttribute{
				Computed: true,
				Attributes: map[string]schema.Attribute{
					"api_server_endpoint": schema.StringAttribute{Computed: true},
					"cluster_id_edgesrv":  schema.StringAttribute{Computed: true},
				},
			},
		},
	}
}

func (r *mksClusterResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.pd = providerData(req.ProviderData, &resp.Diagnostics)
}

func (r *mksClusterResource) client(project string) typed.MKSClusterInterface {
	return r.pd.Clientset.V1alpha1().MKSClusters(project)
}

func (r *mksClusterResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	if r.pd == nil {
		resp.Diagnostics.AddError("Provider not configured", "Provider data is nil; check provider configuration.")
		return
	}
	var plan mksClusterResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	project := plan.Metadata.Project.ValueString()
	obj := mksClusterModelToSDK(ctx, plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	out, err := r.client(project).Create(ctx, obj, gpupaas.CreateOptions{})
	if err != nil {
		handleAPIError(&resp.Diagnostics, err, "Create MKS cluster", nil)
		return
	}
	model := mksClusterSDKToModel(out, project)
	// desired_action / action_inputs are Terraform-side trigger fields; preserve
	// the planned values verbatim across Create so the next plan stays quiet.
	model.DesiredAction = normalizeDesiredAction(plan.DesiredAction)
	model.ActionInputs = plan.ActionInputs
	resp.Diagnostics.Append(resp.State.Set(ctx, model)...)
}

func (r *mksClusterResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	if r.pd == nil {
		return
	}
	var state mksClusterResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	project := state.Metadata.Project.ValueString()
	out, err := r.client(project).Get(ctx, state.Metadata.Name.ValueString(), gpupaas.GetOptions{})
	if err != nil {
		handleAPIError(&resp.Diagnostics, err, "Read MKS cluster", func() { resp.State.RemoveResource(ctx) })
		return
	}
	model := mksClusterSDKToModel(out, project)
	// Preserve trigger fields across refresh — the SDK never echoes them.
	model.DesiredAction = normalizeDesiredAction(state.DesiredAction)
	model.ActionInputs = state.ActionInputs
	resp.Diagnostics.Append(resp.State.Set(ctx, model)...)
}

func (r *mksClusterResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	if r.pd == nil {
		return
	}
	var plan, state mksClusterResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	project := plan.Metadata.Project.ValueString()
	name := plan.Metadata.Name.ValueString()
	cli := r.client(project)

	// 1) Spec reconciliation. The SDK has no Update sub-route; backend Create
	//    is idempotent on (project, name).
	obj := mksClusterModelToSDK(ctx, plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	out, err := cli.Create(ctx, obj, gpupaas.CreateOptions{})
	if err != nil {
		handleAPIError(&resp.Diagnostics, err, "Update MKS cluster", nil)
		return
	}

	// 2) Imperative action dispatch. Compare prior state vs new plan; only the
	//    "newAction != oldAction" transition fires an API call.
	oldAction := normalizedActionValue(state.DesiredAction)
	newAction := normalizedActionValue(plan.DesiredAction)
	if newAction != "" && newAction != "none" && newAction != oldAction {
		switch newAction {
		case "upgrade":
			upgrade := mksUpgradeRequest(plan.ActionInputs)
			if upgrade == nil {
				resp.Diagnostics.AddError(
					"Missing upgrade inputs",
					"desired_action = \"upgrade\" requires action_inputs.upgrade to be set.",
				)
				return
			}
			out, err = cli.Upgrade(ctx, name, upgrade, gpupaas.ActionOptions{})
		case "scale_node_group":
			scale := mksScaleNodeGroupRequest(plan.ActionInputs)
			if scale == nil || scale.NodeGroupName == "" {
				resp.Diagnostics.AddError(
					"Missing scale inputs",
					"desired_action = \"scale_node_group\" requires action_inputs.scale_node_group.node_group_name to be set.",
				)
				return
			}
			out, err = cli.ScaleNodeGroup(ctx, name, scale, gpupaas.ActionOptions{})
		case "add_node_group":
			ng := mksAddNodeGroup(plan.ActionInputs)
			if ng == nil {
				resp.Diagnostics.AddError(
					"Missing node group inputs",
					"desired_action = \"add_node_group\" requires action_inputs.add_node_group.node_group to be set.",
				)
				return
			}
			out, err = cli.AddNodeGroup(ctx, name, ng, gpupaas.ActionOptions{})
		case "remove_node_group":
			ngName := mksRemoveNodeGroupName(plan.ActionInputs)
			if ngName == "" {
				resp.Diagnostics.AddError(
					"Missing node group name",
					"desired_action = \"remove_node_group\" requires action_inputs.remove_node_group.node_group_name to be set.",
				)
				return
			}
			out, err = cli.RemoveNodeGroup(ctx, name, ngName, gpupaas.ActionOptions{})
		}
		if err != nil {
			handleAPIError(&resp.Diagnostics, err, "Execute "+newAction+" on MKS cluster", nil)
			return
		}
	}

	model := mksClusterSDKToModel(out, project)
	model.DesiredAction = normalizeDesiredAction(plan.DesiredAction)
	model.ActionInputs = plan.ActionInputs
	resp.Diagnostics.Append(resp.State.Set(ctx, model)...)
}

func (r *mksClusterResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	if r.pd == nil {
		return
	}
	var state mksClusterResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	project := state.Metadata.Project.ValueString()
	err := r.client(project).Delete(ctx, state.Metadata.Name.ValueString(), gpupaas.DeleteOptions{IgnoreNotFound: true})
	if err != nil && !gpupaas.IsNotFound(err) {
		handleAPIError(&resp.Diagnostics, err, "Delete MKS cluster", nil)
	}
}

func (r *mksClusterResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	project, name, err := ParseProjectScopedImportID(req.ID)
	if err != nil {
		resp.Diagnostics.AddError("Invalid import ID", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("metadata").AtName("project"), project)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("metadata").AtName("name"), name)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), project+"/"+name)...)
	// Imports start with desired_action = "none" so the next plan diff drives
	// any user-specified action transition explicitly.
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("desired_action"), "none")...)
}

// ---- converters ---------------------------------------------------------

func mksClusterModelToSDK(ctx context.Context, m mksClusterResourceModel, diags *diag.Diagnostics) *apiv1.MKSCluster {
	spec := apiv1.MKSClusterSpec{
		KubernetesVersion:     stringOr(m.Spec.KubernetesVersion, ""),
		PlatformVersion:       stringOr(m.Spec.PlatformVersion, ""),
		CNI:                   stringOr(m.Spec.CNI, ""),
		CNIVersion:            stringOr(m.Spec.CNIVersion, ""),
		OS:                    stringOr(m.Spec.OS, ""),
		HAEnabled:             boolPtrFromTF(m.Spec.HAEnabled),
		DedicatedControlPlane: boolPtrFromTF(m.Spec.DedicatedControlPlane),
		Location:              stringOr(m.Spec.Location, ""),
		Blueprint:             mksBlueprintToSDK(m.Spec.Blueprint),
		Networking:            mksNetworkingToSDK(ctx, m.Spec.Networking, diags),
		Proxy:                 mksProxyToSDK(m.Spec.Proxy),
		Storage:               mksStorageToSDK(m.Spec.Storage),
		Tags:                  elementsToStringMap(m.Spec.Tags),
		ControlPlaneNodeGroup: mksNodeGroupToSDK(m.Spec.ControlPlaneNodeGroup),
	}
	for i := range m.Spec.WorkerNodeGroups {
		if g := mksNodeGroupToSDK(&m.Spec.WorkerNodeGroups[i]); g != nil {
			spec.WorkerNodeGroups = append(spec.WorkerNodeGroups, *g)
		}
	}
	for i := range m.Spec.Nodes {
		spec.Nodes = append(spec.Nodes, mksNodeSpecToSDK(ctx, m.Spec.Nodes[i], diags))
	}
	return &apiv1.MKSCluster{
		TypeMeta: apiv1.TypeMeta{APIVersion: apiv1.APIVersion, Kind: apiv1.KindMKSCluster},
		Metadata: metadataToSDK(m.Metadata),
		Spec:     spec,
	}
}

func mksClusterSDKToModel(c *apiv1.MKSCluster, project string) mksClusterResourceModel {
	if c == nil {
		return mksClusterResourceModel{}
	}
	if c.Metadata.Project == "" {
		c.Metadata.Project = project
	}
	return mksClusterResourceModel{
		ID:         types.StringValue(c.Metadata.Project + "/" + c.Metadata.Name),
		APIVersion: types.StringValue(firstNonEmpty(c.APIVersion, apiv1.APIVersion)),
		Kind:       types.StringValue(firstNonEmpty(c.Kind, apiv1.KindMKSCluster)),
		Metadata:   metadataFromSDK(c.Metadata),
		Spec: mksClusterSpecModel{
			KubernetesVersion:     nullableString(c.Spec.KubernetesVersion),
			PlatformVersion:       nullableString(c.Spec.PlatformVersion),
			CNI:                   nullableString(c.Spec.CNI),
			CNIVersion:            nullableString(c.Spec.CNIVersion),
			OS:                    nullableString(c.Spec.OS),
			HAEnabled:             boolFromPtr(c.Spec.HAEnabled),
			DedicatedControlPlane: boolFromPtr(c.Spec.DedicatedControlPlane),
			Location:              nullableString(c.Spec.Location),
			Blueprint:             mksBlueprintFromSDK(c.Spec.Blueprint),
			Networking:            mksNetworkingFromSDK(c.Spec.Networking),
			Proxy:                 mksProxyFromSDK(c.Spec.Proxy),
			Storage:               mksStorageFromSDK(c.Spec.Storage),
			Tags:                  mapFromStringMap(c.Spec.Tags),
			ControlPlaneNodeGroup: mksNodeGroupFromSDK(c.Spec.ControlPlaneNodeGroup),
			WorkerNodeGroups:      mksNodeGroupsFromSDK(c.Spec.WorkerNodeGroups),
			Nodes:                 mksNodeSpecsFromSDK(c.Spec.Nodes),
		},
		Status:        mksClusterStatusFromSDK(c.Status),
		DesiredAction: types.StringNull(),
	}
}

func mksBlueprintToSDK(m *mksBlueprintModel) *apiv1.MKSBlueprint {
	if m == nil {
		return nil
	}
	return &apiv1.MKSBlueprint{
		Name:    stringOr(m.Name, ""),
		Version: stringOr(m.Version, ""),
	}
}

func mksBlueprintFromSDK(b *apiv1.MKSBlueprint) *mksBlueprintModel {
	if b == nil {
		return nil
	}
	return &mksBlueprintModel{
		Name:    nullableString(b.Name),
		Version: nullableString(b.Version),
	}
}

func mksNetworkingToSDK(ctx context.Context, m *mksNetworkingModel, diags *diag.Diagnostics) *apiv1.MKSNetworking {
	if m == nil {
		return nil
	}
	return &apiv1.MKSNetworking{
		VPC:            stringOr(m.VPC, ""),
		Subnet:         stringOr(m.Subnet, ""),
		PodCIDR:        stringOr(m.PodCIDR, ""),
		ServiceCIDR:    stringOr(m.ServiceCIDR, ""),
		IPFamily:       stringOr(m.IPFamily, ""),
		PodCIDRV6:      stringOr(m.PodCIDRV6, ""),
		ServiceCIDRV6:  stringOr(m.ServiceCIDRV6, ""),
		SecurityGroups: stringSliceFromTF(ctx, m.SecurityGroups, diags),
	}
}

func mksNetworkingFromSDK(n *apiv1.MKSNetworking) *mksNetworkingModel {
	if n == nil {
		return nil
	}
	return &mksNetworkingModel{
		VPC:            nullableString(n.VPC),
		Subnet:         nullableString(n.Subnet),
		PodCIDR:        nullableString(n.PodCIDR),
		ServiceCIDR:    nullableString(n.ServiceCIDR),
		IPFamily:       nullableString(n.IPFamily),
		PodCIDRV6:      nullableString(n.PodCIDRV6),
		ServiceCIDRV6:  nullableString(n.ServiceCIDRV6),
		SecurityGroups: listFromStringSlice(n.SecurityGroups),
	}
}

func mksProxyToSDK(m *mksProxyModel) *apiv1.MKSProxy {
	if m == nil {
		return nil
	}
	return &apiv1.MKSProxy{
		HTTPProxy:    stringOr(m.HTTPProxy, ""),
		HTTPSProxy:   stringOr(m.HTTPSProxy, ""),
		NoProxy:      stringOr(m.NoProxy, ""),
		ProxyRootCA:  stringOr(m.ProxyRootCA, ""),
		TLSTerminate: boolPtrFromTF(m.TLSTerminate),
	}
}

func mksProxyFromSDK(p *apiv1.MKSProxy) *mksProxyModel {
	if p == nil {
		return nil
	}
	return &mksProxyModel{
		HTTPProxy:    nullableString(p.HTTPProxy),
		HTTPSProxy:   nullableString(p.HTTPSProxy),
		NoProxy:      nullableString(p.NoProxy),
		ProxyRootCA:  nullableString(p.ProxyRootCA),
		TLSTerminate: boolFromPtr(p.TLSTerminate),
	}
}

func mksStorageBackendToSDK(m *mksStorageBackendModel) *apiv1.MKSStorageBackend {
	if m == nil {
		return nil
	}
	return &apiv1.MKSStorageBackend{
		Type:          stringOr(m.Type, ""),
		AccessMode:    stringOr(m.AccessMode, ""),
		ReclaimPolicy: stringOr(m.ReclaimPolicy, ""),
		Config:        elementsToStringMap(m.Config),
	}
}

func mksStorageBackendFromSDK(b *apiv1.MKSStorageBackend) *mksStorageBackendModel {
	if b == nil {
		return nil
	}
	return &mksStorageBackendModel{
		Type:          nullableString(b.Type),
		AccessMode:    nullableString(b.AccessMode),
		ReclaimPolicy: nullableString(b.ReclaimPolicy),
		Config:        mapFromStringMap(b.Config),
	}
}

func mksStorageToSDK(m *mksStorageModel) *apiv1.MKSStorage {
	if m == nil {
		return nil
	}
	return &apiv1.MKSStorage{
		Block:               mksStorageBackendToSDK(m.Block),
		SharedFS:            mksStorageBackendToSDK(m.SharedFS),
		Object:              mksStorageBackendToSDK(m.Object),
		HighSpeed:           mksStorageBackendToSDK(m.HighSpeed),
		DefaultStorageClass: stringOr(m.DefaultStorageClass, ""),
	}
}

func mksStorageFromSDK(s *apiv1.MKSStorage) *mksStorageModel {
	if s == nil {
		return nil
	}
	return &mksStorageModel{
		Block:               mksStorageBackendFromSDK(s.Block),
		SharedFS:            mksStorageBackendFromSDK(s.SharedFS),
		Object:              mksStorageBackendFromSDK(s.Object),
		HighSpeed:           mksStorageBackendFromSDK(s.HighSpeed),
		DefaultStorageClass: nullableString(s.DefaultStorageClass),
	}
}

func mksKubeletConfigToSDK(m *mksKubeletConfigModel) *apiv1.MKSKubeletConfig {
	if m == nil {
		return nil
	}
	return &apiv1.MKSKubeletConfig{
		KeyValue: elementsToStringMap(m.KeyValue),
		YAML:     stringOr(m.YAML, ""),
	}
}

func mksKubeletConfigFromSDK(k *apiv1.MKSKubeletConfig) *mksKubeletConfigModel {
	if k == nil {
		return nil
	}
	return &mksKubeletConfigModel{
		KeyValue: mapFromStringMap(k.KeyValue),
		YAML:     nullableString(k.YAML),
	}
}

func mksNodeGroupToSDK(m *mksNodeGroupModel) *apiv1.MKSNodeGroup {
	if m == nil {
		return nil
	}
	return &apiv1.MKSNodeGroup{
		ID:              stringOr(m.ID, ""),
		SKU:             stringOr(m.SKU, ""),
		ScalingMode:     stringOr(m.ScalingMode, ""),
		NodeCount:       int32FromTF(m.NodeCount),
		MinNodes:        int32FromTF(m.MinNodes),
		MaxNodes:        int32FromTF(m.MaxNodes),
		DesiredNodes:    int32FromTF(m.DesiredNodes),
		PublicIP:        boolPtrFromTF(m.PublicIP),
		SSHKey:          stringOr(m.SSHKey, ""),
		UserData:        stringOr(m.UserData, ""),
		NodeLabels:      elementsToStringMap(m.NodeLabels),
		NodeAnnotations: elementsToStringMap(m.NodeAnnotations),
		KubeletConfig:   mksKubeletConfigToSDK(m.KubeletConfig),
	}
}

func mksNodeGroupFromSDK(g *apiv1.MKSNodeGroup) *mksNodeGroupModel {
	if g == nil {
		return nil
	}
	return &mksNodeGroupModel{
		ID:              nullableString(g.ID),
		SKU:             nullableString(g.SKU),
		ScalingMode:     nullableString(g.ScalingMode),
		NodeCount:       int64OrNull(int64(g.NodeCount)),
		MinNodes:        int64OrNull(int64(g.MinNodes)),
		MaxNodes:        int64OrNull(int64(g.MaxNodes)),
		DesiredNodes:    int64OrNull(int64(g.DesiredNodes)),
		PublicIP:        boolFromPtr(g.PublicIP),
		SSHKey:          nullableString(g.SSHKey),
		UserData:        nullableString(g.UserData),
		NodeLabels:      mapFromStringMap(g.NodeLabels),
		NodeAnnotations: mapFromStringMap(g.NodeAnnotations),
		KubeletConfig:   mksKubeletConfigFromSDK(g.KubeletConfig),
	}
}

func mksNodeGroupsFromSDK(in []apiv1.MKSNodeGroup) []mksNodeGroupModel {
	if len(in) == 0 {
		return nil
	}
	out := make([]mksNodeGroupModel, 0, len(in))
	for i := range in {
		if g := mksNodeGroupFromSDK(&in[i]); g != nil {
			out = append(out, *g)
		}
	}
	return out
}

func mksNodeSpecToSDK(ctx context.Context, m mksNodeSpecModel, diags *diag.Diagnostics) apiv1.MKSNodeSpec {
	return apiv1.MKSNodeSpec{
		Hostname:        stringOr(m.Hostname, ""),
		InventorySource: stringOr(m.InventorySource, ""),
		Roles:           stringSliceFromTF(ctx, m.Roles, diags),
		SSHKey:          stringOr(m.SSHKey, ""),
		SSHUserName:     stringOr(m.SSHUserName, ""),
		SSHPort:         int32FromTF(m.SSHPort),
		PrivateIP:       stringOr(m.PrivateIP, ""),
		Arch:            stringOr(m.Arch, ""),
		OperatingSystem: stringOr(m.OperatingSystem, ""),
		Interface:       stringOr(m.Interface, ""),
		NodeLabels:      elementsToStringMap(m.NodeLabels),
		NodeAnnotations: elementsToStringMap(m.NodeAnnotations),
		NodeTaints:      elementsToStringMap(m.NodeTaints),
		UserData:        stringOr(m.UserData, ""),
		KubeletConfig:   mksKubeletConfigToSDK(m.KubeletConfig),
		NodePool:        stringOr(m.NodePool, ""),
		SKU:             stringOr(m.SKU, ""),
		PublicIP:        boolPtrFromTF(m.PublicIP),
	}
}

func mksNodeSpecFromSDK(s apiv1.MKSNodeSpec) mksNodeSpecModel {
	return mksNodeSpecModel{
		Hostname:        nullableString(s.Hostname),
		InventorySource: nullableString(s.InventorySource),
		Roles:           listFromStringSlice(s.Roles),
		SSHKey:          nullableString(s.SSHKey),
		SSHUserName:     nullableString(s.SSHUserName),
		SSHPort:         int64OrNull(int64(s.SSHPort)),
		PrivateIP:       nullableString(s.PrivateIP),
		Arch:            nullableString(s.Arch),
		OperatingSystem: nullableString(s.OperatingSystem),
		Interface:       nullableString(s.Interface),
		NodeLabels:      mapFromStringMap(s.NodeLabels),
		NodeAnnotations: mapFromStringMap(s.NodeAnnotations),
		NodeTaints:      mapFromStringMap(s.NodeTaints),
		UserData:        nullableString(s.UserData),
		KubeletConfig:   mksKubeletConfigFromSDK(s.KubeletConfig),
		NodePool:        nullableString(s.NodePool),
		SKU:             nullableString(s.SKU),
		PublicIP:        boolFromPtr(s.PublicIP),
	}
}

func mksNodeSpecsFromSDK(in []apiv1.MKSNodeSpec) []mksNodeSpecModel {
	if len(in) == 0 {
		return nil
	}
	out := make([]mksNodeSpecModel, 0, len(in))
	for i := range in {
		out = append(out, mksNodeSpecFromSDK(in[i]))
	}
	return out
}

func mksClusterStatusFromSDK(s apiv1.MKSClusterStatus) mksClusterStatusModel {
	out := mksClusterStatusModel{
		Condition:       nullableString(s.Condition),
		ConditionReason: nullableString(s.ConditionReason),
		Action:          nullableString(s.Action),
	}
	if s.Output != nil {
		out.Output = &mksClusterOutputModel{
			APIServerEndpoint: nullableString(s.Output.APIServerEndpoint),
			ClusterIDEdgesrv:  nullableString(s.Output.ClusterIDEdgesrv),
		}
	}
	return out
}

// ---- action payload extractors ------------------------------------------

func mksUpgradeRequest(in *mksClusterActionInputs) *apiv1.MKSUpgradeRequest {
	if in == nil || in.Upgrade == nil {
		return nil
	}
	return &apiv1.MKSUpgradeRequest{
		K8sVersion:      stringOr(in.Upgrade.K8sVersion, ""),
		PlatformVersion: stringOr(in.Upgrade.PlatformVersion, ""),
	}
}

func mksScaleNodeGroupRequest(in *mksClusterActionInputs) *apiv1.MKSScaleNodeGroupRequest {
	if in == nil || in.ScaleNodeGroup == nil {
		return nil
	}
	s := in.ScaleNodeGroup
	return &apiv1.MKSScaleNodeGroupRequest{
		NodeGroupName: stringOr(s.NodeGroupName, ""),
		DesiredCount:  int32PtrFromTF(s.DesiredCount),
		MinCount:      int32PtrFromTF(s.MinCount),
		MaxCount:      int32PtrFromTF(s.MaxCount),
	}
}

func mksAddNodeGroup(in *mksClusterActionInputs) *apiv1.MKSNodeGroup {
	if in == nil || in.AddNodeGroup == nil {
		return nil
	}
	return mksNodeGroupToSDK(in.AddNodeGroup.NodeGroup)
}

func mksRemoveNodeGroupName(in *mksClusterActionInputs) string {
	if in == nil || in.RemoveNodeGroup == nil {
		return ""
	}
	return stringOr(in.RemoveNodeGroup.NodeGroupName, "")
}

// ---- small type helpers --------------------------------------------------

// int32FromTF returns an int32 for a Terraform int64, mapping null/unknown to 0
// so the SDK omits the field (all int32 spec fields use omitempty).
func int32FromTF(v types.Int64) int32 {
	if v.IsNull() || v.IsUnknown() {
		return 0
	}
	return int32(v.ValueInt64())
}

// int32PtrFromTF returns a *int32 for a Terraform int64, or nil when
// null/unknown so the SDK omits the field on the wire. Used by the scale
// node group payload where 0 is a meaningful value distinct from "unset".
func int32PtrFromTF(v types.Int64) *int32 {
	if v.IsNull() || v.IsUnknown() {
		return nil
	}
	x := int32(v.ValueInt64())
	return &x
}
