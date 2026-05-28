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
	_ resource.Resource                = (*virtualMachineResource)(nil)
	_ resource.ResourceWithImportState = (*virtualMachineResource)(nil)
	_ resource.ResourceWithConfigure   = (*virtualMachineResource)(nil)
)

// vmDesiredActionValues is the canonical allow-list mirroring the verbs
// exposed by typed.VirtualMachineInterface. Keep this in sync with the SDK
// (Start, Stop, Reboot, plus the generic Action escape hatch). "none" is the
// explicit no-op used when omitting the field would otherwise be ambiguous.
var vmDesiredActionValues = []string{"none", "start", "stop", "reboot"}

// NewVirtualMachineResource constructs the gpupaas_virtual_machine resource.
func NewVirtualMachineResource() resource.Resource { return &virtualMachineResource{} }

type virtualMachineResource struct{ pd *client.ProviderData }

type virtualMachineResourceModel struct {
	ID            types.String         `tfsdk:"id"`
	APIVersion    types.String         `tfsdk:"api_version"`
	Kind          types.String         `tfsdk:"kind"`
	Metadata      MetadataModel        `tfsdk:"metadata"`
	Spec          virtualMachineSpec   `tfsdk:"spec"`
	Status        virtualMachineStatus `tfsdk:"status"`
	DesiredAction types.String         `tfsdk:"desired_action"`
	ActionInputs  *vmActionInputs      `tfsdk:"action_inputs"`
}

type vmActionInputs struct {
	Envs      types.Map `tfsdk:"envs"`
	Variables types.Map `tfsdk:"variables"`
}

type virtualMachineSpec struct {
	VirtualMachine        resourceRefModel `tfsdk:"virtual_machine"`
	VMID                  types.String     `tfsdk:"vm_id"`
	Type                  types.String     `tfsdk:"type"`
	Name                  types.String     `tfsdk:"name"`
	CPUCount              types.String     `tfsdk:"cpu_count"`
	Memory                types.String     `tfsdk:"memory"`
	SecurityGroup         types.String     `tfsdk:"security_group"`
	SSHKey                types.String     `tfsdk:"ssh_key"`
	VPC                   types.String     `tfsdk:"vpc"`
	Subnet                types.String     `tfsdk:"subnet"`
	AssignPublicIP        types.Bool       `tfsdk:"assign_public_ip"`
	Sharing               *SharingModel    `tfsdk:"sharing"`
	Datacenter            types.String     `tfsdk:"datacenter"`
	GuestPassword         types.String     `tfsdk:"guest_password"`
	DNSServers            types.List       `tfsdk:"dns_servers"`
	UserData              types.String     `tfsdk:"user_data"`
	Timezone              types.String     `tfsdk:"timezone"`
	SharedStorage         types.String     `tfsdk:"shared_storage"`
	BlockStorageType      types.String     `tfsdk:"block_storage_type"`
	Image                 types.String     `tfsdk:"image"`
	BootDiskSize          types.Int64      `tfsdk:"boot_disk_size"`
	CreateAdditionalBlock types.Bool       `tfsdk:"create_additional_block"`
	AdditionalBlockSize   types.Int64      `tfsdk:"additional_block_size"`
}

type virtualMachineStatus struct {
	Status          types.String          `tfsdk:"status"`
	Reason          types.String          `tfsdk:"reason"`
	Action          types.String          `tfsdk:"action"`
	Output          *virtualMachineOutput `tfsdk:"output"`
	ProvisionedAt   types.String          `tfsdk:"provisioned_at"`
	LastConnectedAt types.String          `tfsdk:"last_connected_at"`
}

type virtualMachineOutput struct {
	HostName      types.String `tfsdk:"host_name"`
	OSName        types.String `tfsdk:"os_name"`
	PrivateIP     types.String `tfsdk:"private_ip"`
	PublicIP      types.String `tfsdk:"public_ip"`
	ServerHost    types.String `tfsdk:"server_host"`
	UserName      types.String `tfsdk:"user_name"`
	DiskMountPath types.String `tfsdk:"disk_mount_path"`
}

func (r *virtualMachineResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_virtual_machine"
}

func (r *virtualMachineResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "GPU PaaS virtual machine (`apiVersion: " + apiv1.APIVersion + "`, `kind: VirtualMachine`). Supports project- or workspace-scoped placement via metadata.workspace. " +
			"Imperative lifecycle actions (start/stop/reboot) are driven through the `desired_action` attribute.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"api_version": schema.StringAttribute{Computed: true},
			"kind":        schema.StringAttribute{Computed: true},
			"metadata":    MetadataResourceAttribute(true, true),
			"spec":        virtualMachineSpecAttribute(),
			"status":      virtualMachineStatusAttribute(),
			"desired_action": schema.StringAttribute{
				Optional: true,
				Computed: true,
				MarkdownDescription: "Imperative lifecycle action to drive on this VM. One of " +
					"`none`, `start`, `stop`, `reboot`. Changing this attribute (from its " +
					"prior planned/state value) triggers the corresponding SDK call on " +
					"the next apply. Setting it back to `none` (or omitting it) is a " +
					"no-op — the provider never auto-re-triggers actions on refresh.",
				Validators: []validator.String{
					stringvalidator.OneOf(vmDesiredActionValues...),
				},
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"action_inputs": schema.SingleNestedAttribute{
				Optional: true,
				MarkdownDescription: "Optional payload accompanying `desired_action`. Maps to " +
					"`gpupaas.ActionOptions{Envs, Variables}`. Each value is sent as a " +
					"single map entry in the SDK's list-of-maps wire format.",
				Attributes: map[string]schema.Attribute{
					"envs": schema.MapAttribute{
						Optional:            true,
						ElementType:         types.StringType,
						MarkdownDescription: "Environment variables passed to the action.",
					},
					"variables": schema.MapAttribute{
						Optional:            true,
						ElementType:         types.StringType,
						MarkdownDescription: "Action variables (action-specific key/value bag).",
					},
				},
			},
		},
	}
}

func virtualMachineSpecAttribute() schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{
		Required: true,
		Attributes: map[string]schema.Attribute{
			"virtual_machine": resourceRefSchema("Virtual machine catalog reference."),
			"vm_id": schema.StringAttribute{
				Optional: true,
				Computed: true,
				MarkdownDescription: "Inventory device ID of the virtual machine. " +
					"Usually observed from the backend; can be set by clients to pin a " +
					"VM to a specific device. Maps to `spec.vmId` (wire field `vm_id`).",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"type":                    schema.StringAttribute{Optional: true},
			"name":                    schema.StringAttribute{Optional: true, MarkdownDescription: "Optional logical name; falls back to metadata.name."},
			"cpu_count":               schema.StringAttribute{Optional: true},
			"memory":                  schema.StringAttribute{Optional: true},
			"security_group":          schema.StringAttribute{Optional: true},
			"ssh_key":                 schema.StringAttribute{Optional: true},
			"vpc":                     schema.StringAttribute{Optional: true},
			"subnet":                  schema.StringAttribute{Optional: true},
			"assign_public_ip":        schema.BoolAttribute{Optional: true},
			"sharing":                 SharingResourceAttribute(),
			"datacenter":              schema.StringAttribute{Optional: true},
			"guest_password":          schema.StringAttribute{Optional: true, Sensitive: true},
			"dns_servers":             schema.ListAttribute{Optional: true, ElementType: types.StringType},
			"user_data":               schema.StringAttribute{Optional: true},
			"timezone":                schema.StringAttribute{Optional: true},
			"shared_storage":          schema.StringAttribute{Optional: true},
			"block_storage_type":      schema.StringAttribute{Optional: true},
			"image":                   schema.StringAttribute{Optional: true},
			"boot_disk_size":          schema.Int64Attribute{Optional: true},
			"create_additional_block": schema.BoolAttribute{Optional: true},
			"additional_block_size":   schema.Int64Attribute{Optional: true},
		},
	}
}

func virtualMachineStatusAttribute() schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{
		Computed: true,
		Attributes: map[string]schema.Attribute{
			"status":            schema.StringAttribute{Computed: true},
			"reason":            schema.StringAttribute{Computed: true},
			"action":            schema.StringAttribute{Computed: true},
			"provisioned_at":    schema.StringAttribute{Computed: true},
			"last_connected_at": schema.StringAttribute{Computed: true},
			"output": schema.SingleNestedAttribute{
				Computed: true,
				Attributes: map[string]schema.Attribute{
					"host_name":       schema.StringAttribute{Computed: true},
					"os_name":         schema.StringAttribute{Computed: true},
					"private_ip":      schema.StringAttribute{Computed: true},
					"public_ip":       schema.StringAttribute{Computed: true},
					"server_host":     schema.StringAttribute{Computed: true},
					"user_name":       schema.StringAttribute{Computed: true},
					"disk_mount_path": schema.StringAttribute{Computed: true},
				},
			},
		},
	}
}

func (r *virtualMachineResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.pd = providerData(req.ProviderData, &resp.Diagnostics)
}

func (r *virtualMachineResource) client(project, workspace string) typed.VirtualMachineInterface {
	if workspace == "" {
		return r.pd.Clientset.V1alpha1().VirtualMachines(project)
	}
	return r.pd.Clientset.V1alpha1().Workspaces(project).VirtualMachines(workspace)
}

func (r *virtualMachineResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	if r.pd == nil {
		resp.Diagnostics.AddError("Provider not configured", "Provider data is nil; check provider configuration.")
		return
	}
	var plan virtualMachineResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	project := plan.Metadata.Project.ValueString()
	workspace := plan.Metadata.Workspace.ValueString()
	obj := virtualMachineModelToSDK(ctx, plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	out, err := r.client(project, workspace).Create(ctx, obj, gpupaas.CreateOptions{})
	if err != nil {
		handleAPIError(&resp.Diagnostics, err, "Create virtual machine", nil)
		return
	}
	model := virtualMachineSDKToModel(ctx, out, project, workspace, &resp.Diagnostics)
	// desired_action / action_inputs are Terraform-side trigger fields; preserve
	// the planned values verbatim across Create so the next plan stays quiet.
	model.DesiredAction = normalizeDesiredAction(plan.DesiredAction)
	model.ActionInputs = plan.ActionInputs
	resp.Diagnostics.Append(resp.State.Set(ctx, model)...)
}

func (r *virtualMachineResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	if r.pd == nil {
		return
	}
	var state virtualMachineResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	project := state.Metadata.Project.ValueString()
	workspace := state.Metadata.Workspace.ValueString()
	out, err := r.client(project, workspace).Get(ctx, state.Metadata.Name.ValueString(), gpupaas.GetOptions{})
	if err != nil {
		handleAPIError(&resp.Diagnostics, err, "Read virtual machine", func() { resp.State.RemoveResource(ctx) })
		return
	}
	model := virtualMachineSDKToModel(ctx, out, project, workspace, &resp.Diagnostics)
	// Preserve trigger fields across refresh — the SDK never echoes them.
	model.DesiredAction = normalizeDesiredAction(state.DesiredAction)
	model.ActionInputs = state.ActionInputs
	resp.Diagnostics.Append(resp.State.Set(ctx, model)...)
}

func (r *virtualMachineResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	if r.pd == nil {
		return
	}
	var plan, state virtualMachineResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	project := plan.Metadata.Project.ValueString()
	workspace := plan.Metadata.Workspace.ValueString()
	name := plan.Metadata.Name.ValueString()
	cli := r.client(project, workspace)

	// 1) Spec reconciliation. The SDK has no Update sub-route; backend Create
	//    is idempotent on (project, workspace, name).
	obj := virtualMachineModelToSDK(ctx, plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	out, err := cli.Create(ctx, obj, gpupaas.CreateOptions{})
	if err != nil {
		handleAPIError(&resp.Diagnostics, err, "Update virtual machine", nil)
		return
	}

	// 2) Imperative action dispatch. Compare prior state vs new plan; only the
	//    "newAction != oldAction" transition fires an API call.
	oldAction := normalizedActionValue(state.DesiredAction)
	newAction := normalizedActionValue(plan.DesiredAction)
	if newAction != "" && newAction != "none" && newAction != oldAction {
		actionOpts := vmActionOptionsFromInputs(ctx, plan.ActionInputs, &resp.Diagnostics)
		if resp.Diagnostics.HasError() {
			return
		}
		switch newAction {
		case "start":
			out, err = cli.Start(ctx, name, actionOpts)
		case "stop":
			out, err = cli.Stop(ctx, name, actionOpts)
		case "reboot":
			out, err = cli.Reboot(ctx, name, actionOpts)
		default:
			// Forward-compatible: any verb the SDK exposes via Action() can be
			// reached here once it's added to vmDesiredActionValues.
			out, err = cli.Action(ctx, name, newAction, actionOpts)
		}
		if err != nil {
			handleAPIError(&resp.Diagnostics, err, "Execute "+newAction+" on virtual machine", nil)
			return
		}
	}

	model := virtualMachineSDKToModel(ctx, out, project, workspace, &resp.Diagnostics)
	model.DesiredAction = normalizeDesiredAction(plan.DesiredAction)
	model.ActionInputs = plan.ActionInputs
	resp.Diagnostics.Append(resp.State.Set(ctx, model)...)
}

func (r *virtualMachineResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	if r.pd == nil {
		return
	}
	var state virtualMachineResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	project := state.Metadata.Project.ValueString()
	workspace := state.Metadata.Workspace.ValueString()
	err := r.client(project, workspace).Delete(ctx, state.Metadata.Name.ValueString(), gpupaas.DeleteOptions{IgnoreNotFound: true})
	if err != nil && !gpupaas.IsNotFound(err) {
		handleAPIError(&resp.Diagnostics, err, "Delete virtual machine", nil)
	}
}

func (r *virtualMachineResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	project, workspace, name, err := ParseFlexibleScopedImportID(req.ID)
	if err != nil {
		resp.Diagnostics.AddError("Invalid import ID", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("metadata").AtName("project"), project)...)
	if workspace != "" {
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("metadata").AtName("workspace"), workspace)...)
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("metadata").AtName("name"), name)...)
	id := project + "/" + name
	if workspace != "" {
		id = project + "/" + workspace + "/" + name
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), id)...)
	// Imports start with desired_action = "none" so the next plan diff drives
	// any user-specified action transition explicitly.
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("desired_action"), "none")...)
}

// ---- converters ---------------------------------------------------------

func virtualMachineModelToSDK(ctx context.Context, m virtualMachineResourceModel, diags *diag.Diagnostics) *apiv1.VirtualMachine {
	spec := apiv1.VirtualMachineSpec{
		VirtualMachine:        resourceRefToSDK(m.Spec.VirtualMachine),
		VMId:                  stringOr(m.Spec.VMID, ""),
		Type:                  stringOr(m.Spec.Type, ""),
		Name:                  stringOr(m.Spec.Name, ""),
		CPUCount:              stringOr(m.Spec.CPUCount, ""),
		Memory:                stringOr(m.Spec.Memory, ""),
		SecurityGroup:         stringOr(m.Spec.SecurityGroup, ""),
		SSHKey:                stringOr(m.Spec.SSHKey, ""),
		VPC:                   stringOr(m.Spec.VPC, ""),
		Subnet:                stringOr(m.Spec.Subnet, ""),
		AssignPublicIP:        m.Spec.AssignPublicIP.ValueBool(),
		Sharing:               vmSharingToSDK(ctx, m.Spec.Sharing, diags),
		Datacenter:            stringOr(m.Spec.Datacenter, ""),
		GuestPassword:         stringOr(m.Spec.GuestPassword, ""),
		DNSServers:            stringSliceFromTF(ctx, m.Spec.DNSServers, diags),
		UserData:              stringOr(m.Spec.UserData, ""),
		Timezone:              stringOr(m.Spec.Timezone, ""),
		SharedStorage:         stringOr(m.Spec.SharedStorage, ""),
		BlockStorageType:      stringOr(m.Spec.BlockStorageType, ""),
		Image:                 stringOr(m.Spec.Image, ""),
		BootDiskSize:          int32(m.Spec.BootDiskSize.ValueInt64()),
		CreateAdditionalBlock: m.Spec.CreateAdditionalBlock.ValueBool(),
		AdditionalBlockSize:   int32(m.Spec.AdditionalBlockSize.ValueInt64()),
	}
	return &apiv1.VirtualMachine{
		TypeMeta: apiv1.TypeMeta{APIVersion: apiv1.APIVersion, Kind: apiv1.KindVirtualMachine},
		Metadata: metadataToSDK(m.Metadata),
		Spec:     spec,
	}
}

func virtualMachineSDKToModel(ctx context.Context, v *apiv1.VirtualMachine, project, workspace string, diags *diag.Diagnostics) virtualMachineResourceModel {
	if v == nil {
		return virtualMachineResourceModel{}
	}
	if v.Metadata.Project == "" {
		v.Metadata.Project = project
	}
	if v.Metadata.Workspace == "" {
		v.Metadata.Workspace = workspace
	}
	id := v.Metadata.Project + "/" + v.Metadata.Name
	if v.Metadata.Workspace != "" {
		id = v.Metadata.Project + "/" + v.Metadata.Workspace + "/" + v.Metadata.Name
	}
	out := virtualMachineResourceModel{
		ID:         types.StringValue(id),
		APIVersion: types.StringValue(firstNonEmpty(v.APIVersion, apiv1.APIVersion)),
		Kind:       types.StringValue(firstNonEmpty(v.Kind, apiv1.KindVirtualMachine)),
		Metadata:   metadataFromSDK(v.Metadata),
		Spec: virtualMachineSpec{
			VirtualMachine:        resourceRefFromSDK(v.Spec.VirtualMachine),
			VMID:                  nullableString(v.Spec.VMId),
			Type:                  nullableString(v.Spec.Type),
			Name:                  nullableString(v.Spec.Name),
			CPUCount:              nullableString(v.Spec.CPUCount),
			Memory:                nullableString(v.Spec.Memory),
			SecurityGroup:         nullableString(v.Spec.SecurityGroup),
			SSHKey:                nullableString(v.Spec.SSHKey),
			VPC:                   nullableString(v.Spec.VPC),
			Subnet:                nullableString(v.Spec.Subnet),
			AssignPublicIP:        types.BoolValue(v.Spec.AssignPublicIP),
			Sharing:               vmSharingFromSDK(ctx, v.Spec.Sharing, diags),
			Datacenter:            nullableString(v.Spec.Datacenter),
			GuestPassword:         nullableString(v.Spec.GuestPassword),
			DNSServers:            listFromStringSlice(v.Spec.DNSServers),
			UserData:              nullableString(v.Spec.UserData),
			Timezone:              nullableString(v.Spec.Timezone),
			SharedStorage:         nullableString(v.Spec.SharedStorage),
			BlockStorageType:      nullableString(v.Spec.BlockStorageType),
			Image:                 nullableString(v.Spec.Image),
			BootDiskSize:          types.Int64Value(int64(v.Spec.BootDiskSize)),
			CreateAdditionalBlock: types.BoolValue(v.Spec.CreateAdditionalBlock),
			AdditionalBlockSize:   types.Int64Value(int64(v.Spec.AdditionalBlockSize)),
		},
		Status: virtualMachineStatus{
			Status:          nullableString(v.Status.Status),
			Reason:          nullableString(v.Status.Reason),
			Action:          nullableString(v.Status.Action),
			ProvisionedAt:   nullableString(v.Status.ProvisionedAt),
			LastConnectedAt: nullableString(v.Status.LastConnectedAt),
		},
		// DesiredAction / ActionInputs are intentionally left zero here — the
		// caller (Create/Read/Update) is responsible for preserving them
		// across the SDK round-trip since the SDK does not echo trigger
		// fields back.
		DesiredAction: types.StringNull(),
	}
	if v.Status.Output != nil {
		out.Status.Output = &virtualMachineOutput{
			HostName:      nullableString(v.Status.Output.HostName),
			OSName:        nullableString(v.Status.Output.OSName),
			PrivateIP:     nullableString(v.Status.Output.PrivateIP),
			PublicIP:      nullableString(v.Status.Output.PublicIP),
			ServerHost:    nullableString(v.Status.Output.ServerHost),
			UserName:      nullableString(v.Status.Output.UserName),
			DiskMountPath: nullableString(v.Status.Output.DiskMountPath),
		}
	}
	return out
}

// ---- action helpers ------------------------------------------------------

// normalizeDesiredAction returns the value to persist in state. Null/unset
// flattens to "none" so the next plan stays stable.
func normalizeDesiredAction(in types.String) types.String {
	if in.IsNull() || in.IsUnknown() || in.ValueString() == "" {
		return types.StringValue("none")
	}
	return in
}

// normalizedActionValue returns the lowercase comparable form of a
// desired_action value: "" for null/unknown, otherwise the literal string.
// "none" stays as "none" since it has explicit "do-nothing" semantics.
func normalizedActionValue(in types.String) string {
	if in.IsNull() || in.IsUnknown() {
		return ""
	}
	return in.ValueString()
}

// vmActionOptionsFromInputs converts the Terraform action_inputs payload to
// the SDK's gpupaas.ActionOptions. Empty / null inputs map to a zero options
// struct so the SDK can omit the wire payload entirely.
func vmActionOptionsFromInputs(ctx context.Context, in *vmActionInputs, diags *diag.Diagnostics) gpupaas.ActionOptions {
	if in == nil {
		return gpupaas.ActionOptions{}
	}
	opts := gpupaas.ActionOptions{}
	if envs := stringMapEntriesFromTF(ctx, in.Envs, diags); len(envs) > 0 {
		opts.Envs = []map[string]string{envs}
	}
	if vars := stringMapEntriesFromTF(ctx, in.Variables, diags); len(vars) > 0 {
		opts.Variables = []map[string]string{vars}
	}
	return opts
}

// stringMapEntriesFromTF safely flattens a types.Map of strings into a Go map.
// Returns nil for null/unknown maps without recording diagnostics.
func stringMapEntriesFromTF(ctx context.Context, in types.Map, diags *diag.Diagnostics) map[string]string {
	if in.IsNull() || in.IsUnknown() {
		return nil
	}
	out := map[string]string{}
	d := in.ElementsAs(ctx, &out, false)
	diags.Append(d...)
	return out
}
