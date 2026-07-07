package resources

import (
	"context"
	"time"

	gpupaas "github.com/gpupaas-ai/gpupaas-go"
	apiv1 "github.com/gpupaas-ai/gpupaas-go/apis/v1alpha1"
	typed "github.com/gpupaas-ai/gpupaas-go/clientset/typed/v1alpha1"
	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/gpupaas-ai/terraform-provider-gpupaas/internal/client"
	"github.com/gpupaas-ai/terraform-provider-gpupaas/internal/wait"
)

var (
	_ resource.Resource                = (*virtualMachineResource)(nil)
	_ resource.ResourceWithImportState = (*virtualMachineResource)(nil)
	_ resource.ResourceWithConfigure   = (*virtualMachineResource)(nil)
)

// vmPowerStateValues is the declarative power_state allow-list (plan.md
// 3.4). "reboot" is deliberately excluded — it is a verb, not a
// convergeable state, and stays out-of-band (`paasctl vm reboot`).
var vmPowerStateValues = []string{"on", "off"}

const (
	vmCreateTimeoutDefault = 30 * time.Minute
	vmUpdateTimeoutDefault = 30 * time.Minute
	vmDeleteTimeoutDefault = 15 * time.Minute
)

// NewVirtualMachineResource constructs the gpupaas_virtual_machine resource.
func NewVirtualMachineResource() resource.Resource { return &virtualMachineResource{} }

type virtualMachineResource struct{ pd *client.ProviderData }

type virtualMachineResourceModel struct {
	ID         types.String         `tfsdk:"id"`
	APIVersion types.String         `tfsdk:"api_version"`
	Kind       types.String         `tfsdk:"kind"`
	Metadata   MetadataModel        `tfsdk:"metadata"`
	Spec       virtualMachineSpec   `tfsdk:"spec"`
	Status     virtualMachineStatus `tfsdk:"status"`
	PowerState types.String         `tfsdk:"power_state"`
	Timeouts   timeouts.Value       `tfsdk:"timeouts"`
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
	DNSServers            types.Set        `tfsdk:"dns_servers"`
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

func (r *virtualMachineResource) Schema(ctx context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "GPU PaaS virtual machine (`apiVersion: " + apiv1.APIVersion + "`, `kind: VirtualMachine`). Supports project- or workspace-scoped placement via metadata.workspace. " +
			"Day-2 mutable fields are `guest_password`, `security_group`, and `sharing`; every other spec/metadata field is immutable after creation. " +
			"Declarative power on/off is driven through `power_state`; `reboot` stays out-of-band (`paasctl vm reboot`).",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"api_version": schema.StringAttribute{Computed: true},
			"kind":        schema.StringAttribute{Computed: true},
			"metadata":    immutableMetadataAttribute(true, true),
			"spec":        virtualMachineSpecAttribute(),
			"status":      virtualMachineStatusAttribute(),
			"power_state": schema.StringAttribute{
				Optional: true,
				Computed: true,
				MarkdownDescription: "Declarative power state: `on` or `off`. Defaults to `on` when unset. " +
					"Read reconciles this from the backend-observed status/action, so an out-of-band " +
					"stop/start shows up as drift and the next apply dispatches the corresponding " +
					"Start/Stop call. `reboot` is intentionally not representable here — it is a " +
					"one-shot verb, not a convergeable state; use `paasctl vm reboot` out-of-band.",
				Validators: []validator.String{
					stringvalidator.OneOf(vmPowerStateValues...),
				},
				Default: stringdefault.StaticString("on"),
			},
			"timeouts": timeouts.Attributes(ctx, timeouts.Opts{
				Create: true,
				Update: true,
				Delete: true,
			}),
		},
	}
}

func virtualMachineSpecAttribute() schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{
		Required: true,
		Attributes: map[string]schema.Attribute{
			"virtual_machine": immutableResourceRefSchema("Virtual machine catalog reference."),
			"vm_id": schema.StringAttribute{
				Optional: true,
				Computed: true,
				MarkdownDescription: "Inventory device ID of the virtual machine. " +
					"Usually observed from the backend; can be set by clients to pin a " +
					"VM to a specific device. Maps to `spec.vmId` (wire field `vm_id`).",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"type":      schema.StringAttribute{Optional: true, PlanModifiers: []planmodifier.String{immutableString()}},
			"name":      schema.StringAttribute{Optional: true, MarkdownDescription: "Optional logical name; falls back to metadata.name.", PlanModifiers: []planmodifier.String{immutableString()}},
			"cpu_count": schema.StringAttribute{Optional: true, PlanModifiers: []planmodifier.String{immutableString()}},
			"memory":    schema.StringAttribute{Optional: true, PlanModifiers: []planmodifier.String{immutableString()}},
			// security_group is Day-2 mutable (D4) — no immutable() modifier.
			"security_group": schema.StringAttribute{Optional: true},
			"ssh_key":        schema.StringAttribute{Optional: true, PlanModifiers: []planmodifier.String{immutableString()}},
			"vpc":            schema.StringAttribute{Optional: true, PlanModifiers: []planmodifier.String{immutableString()}},
			"subnet":         schema.StringAttribute{Optional: true, PlanModifiers: []planmodifier.String{immutableString()}},
			"assign_public_ip": schema.BoolAttribute{
				Optional:      true,
				PlanModifiers: []planmodifier.Bool{immutableBool()},
			},
			// sharing is Day-2 mutable (D4) — no immutable() modifier.
			"sharing":    SharingResourceAttribute(),
			"datacenter": schema.StringAttribute{Optional: true, PlanModifiers: []planmodifier.String{immutableString()}},
			// guest_password is Day-2 mutable (D4) and write-only (FR-10):
			// Read/FromSDK must never overwrite it from the (absent/encrypted)
			// server value — see virtualMachineSDKToModel.
			"guest_password": schema.StringAttribute{Optional: true, Sensitive: true},
			"dns_servers": schema.SetAttribute{
				Optional:      true,
				ElementType:   types.StringType,
				PlanModifiers: []planmodifier.Set{immutableSet()},
			},
			"user_data":          schema.StringAttribute{Optional: true, PlanModifiers: []planmodifier.String{immutableString()}},
			"timezone":           schema.StringAttribute{Optional: true, PlanModifiers: []planmodifier.String{immutableString()}},
			"shared_storage":     schema.StringAttribute{Optional: true, PlanModifiers: []planmodifier.String{immutableString()}},
			"block_storage_type": schema.StringAttribute{Optional: true, PlanModifiers: []planmodifier.String{immutableString()}},
			"image":              schema.StringAttribute{Optional: true, PlanModifiers: []planmodifier.String{immutableString()}},
			"boot_disk_size": schema.Int64Attribute{
				Optional:      true,
				PlanModifiers: []planmodifier.Int64{immutableInt64()},
			},
			"create_additional_block": schema.BoolAttribute{
				Optional:      true,
				PlanModifiers: []planmodifier.Bool{immutableBool()},
			},
			"additional_block_size": schema.Int64Attribute{
				Optional:      true,
				PlanModifiers: []planmodifier.Int64{immutableInt64()},
			},
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

// statusGetter returns a wait.Getter that polls GetStatus for name.
func statusGetter(cli typed.VirtualMachineInterface, name string) wait.Getter {
	return func(ctx context.Context) (*apiv1.VirtualMachine, error) {
		return cli.GetStatus(ctx, name, gpupaas.GetOptions{})
	}
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
	name := plan.Metadata.Name.ValueString()
	cli := r.client(project, workspace)
	obj := virtualMachineModelToSDK(ctx, plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	createTimeout, d := plan.Timeouts.Create(ctx, vmCreateTimeoutDefault)
	resp.Diagnostics.Append(d...)
	if resp.Diagnostics.HasError() {
		return
	}

	out, err := cli.Create(ctx, obj, gpupaas.CreateOptions{})
	if err != nil {
		handleAPIError(&resp.Diagnostics, err, "Create virtual machine", nil)
		return
	}
	// The 200 from Create only means SUBMITTED; block until a true terminal
	// state (plan.md 3.4), then re-read so state holds settled output.
	out, err = wait.WaitForVMReady(ctx, statusGetter(cli, name), wait.Options{Timeout: createTimeout})
	if err != nil {
		resp.Diagnostics.AddError("Virtual machine provisioning failed", err.Error())
		return
	}

	wantPower := stringOr(plan.PowerState, "on")
	if wantPower == "off" {
		submittedAt := time.Now()
		out, err = cli.Stop(ctx, name, gpupaas.ActionOptions{})
		if err != nil {
			handleAPIError(&resp.Diagnostics, err, "Stop virtual machine", nil)
			return
		}
		out, err = wait.WaitForVMAction(ctx, statusGetter(cli, name), "stop", wait.Options{Timeout: createTimeout, SubmittedAt: submittedAt})
		if err != nil {
			resp.Diagnostics.AddError("Virtual machine stop failed", err.Error())
			return
		}
	}

	model := virtualMachineSDKToModel(ctx, out, project, workspace, &resp.Diagnostics)
	model.Timeouts = plan.Timeouts
	// guest_password is write-only: preserve the value the user configured
	// since the SDK never echoes it back (FR-10).
	model.Spec.GuestPassword = plan.Spec.GuestPassword
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
	model.Timeouts = state.Timeouts
	// guest_password is write-only (FR-10): never overwrite state from the
	// (absent/encrypted) server value — preserve whatever Terraform already
	// has recorded.
	model.Spec.GuestPassword = state.Spec.GuestPassword
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
	if diags := checkVMImmutableFields(plan, state); diags.HasError() {
		resp.Diagnostics.Append(diags...)
		return
	}

	project := plan.Metadata.Project.ValueString()
	workspace := plan.Metadata.Workspace.ValueString()
	name := plan.Metadata.Name.ValueString()
	cli := r.client(project, workspace)

	updateTimeout, d := plan.Timeouts.Update(ctx, vmUpdateTimeoutDefault)
	resp.Diagnostics.Append(d...)
	if resp.Diagnostics.HasError() {
		return
	}

	// 1) Spec reconciliation for the mutable fields (guest_password,
	//    security_group, sharing). The SDK has no Update sub-route; Create is
	//    the documented upsert, idempotent on (project, workspace, name).
	obj := virtualMachineModelToSDK(ctx, plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	out, err := cli.Create(ctx, obj, gpupaas.CreateOptions{})
	if err != nil {
		handleAPIError(&resp.Diagnostics, err, "Update virtual machine", nil)
		return
	}
	// No SubmittedAt freshness guard here: an upsert of Day-2 fields
	// (password/SG/sharing) may apply synchronously without a visible status
	// transition, so an already-"success" status must be accepted immediately
	// rather than treated as stale (unlike action dispatch below, this path
	// has no distinct prior "operation" whose terminal state could linger).
	out, err = wait.WaitForVMReady(ctx, statusGetter(cli, name), wait.Options{Timeout: updateTimeout})
	if err != nil {
		resp.Diagnostics.AddError("Virtual machine update failed", err.Error())
		return
	}

	// 2) Declarative power LCM: dispatch Start/Stop only on an actual
	//    transition, then wait for the true (non-stale) ACTIONCOMPLETE.
	oldPower := stringOr(state.PowerState, "on")
	newPower := stringOr(plan.PowerState, "on")
	if newPower != oldPower {
		submittedAt := time.Now()
		verb := "start"
		if newPower == "off" {
			verb = "stop"
			out, err = cli.Stop(ctx, name, gpupaas.ActionOptions{})
		} else {
			out, err = cli.Start(ctx, name, gpupaas.ActionOptions{})
		}
		if err != nil {
			handleAPIError(&resp.Diagnostics, err, "Set virtual machine power_state", nil)
			return
		}
		out, err = wait.WaitForVMAction(ctx, statusGetter(cli, name), verb, wait.Options{Timeout: updateTimeout, SubmittedAt: submittedAt})
		if err != nil {
			resp.Diagnostics.AddError("Virtual machine power_state change failed", err.Error())
			return
		}
	}

	model := virtualMachineSDKToModel(ctx, out, project, workspace, &resp.Diagnostics)
	model.Timeouts = plan.Timeouts
	model.Spec.GuestPassword = plan.Spec.GuestPassword
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
	name := state.Metadata.Name.ValueString()
	cli := r.client(project, workspace)

	deleteTimeout, d := state.Timeouts.Delete(ctx, vmDeleteTimeoutDefault)
	resp.Diagnostics.Append(d...)
	if resp.Diagnostics.HasError() {
		return
	}

	err := cli.Delete(ctx, name, gpupaas.DeleteOptions{IgnoreNotFound: true})
	if err != nil && !gpupaas.IsNotFound(err) {
		handleAPIError(&resp.Diagnostics, err, "Delete virtual machine", nil)
		return
	}
	if err := wait.WaitForGone(ctx, statusGetter(cli, name), wait.Options{Timeout: deleteTimeout}); err != nil {
		resp.Diagnostics.AddError("Virtual machine teardown failed", err.Error())
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
	// power_state is derived from status.action on the Read that Terraform
	// performs immediately after import; nothing to seed here.
}

// checkVMImmutableFields defensively re-verifies the immutable-field
// contract in Update, independent of the schema-level immutable() plan
// modifiers (plan.md 3.3 belt-and-braces).
func checkVMImmutableFields(plan, state virtualMachineResourceModel) diag.Diagnostics {
	var diags diag.Diagnostics
	fail := func(attr string) {
		diags.AddError(
			attr+" cannot be modified on an existing resource",
			"Day-2 changes are limited to guest_password, security_group, and sharing. "+
				"Destroy and recreate explicitly, or revert the change.",
		)
	}
	if plan.Metadata.Name.ValueString() != state.Metadata.Name.ValueString() {
		fail("metadata.name")
	}
	if plan.Metadata.Project.ValueString() != state.Metadata.Project.ValueString() {
		fail("metadata.project")
	}
	if stringOr(plan.Metadata.Workspace, "") != stringOr(state.Metadata.Workspace, "") {
		fail("metadata.workspace")
	}
	ps, ss := plan.Spec, state.Spec
	strFields := map[string]struct{ p, s types.String }{
		"spec.virtual_machine.name": {ps.VirtualMachine.Name, ss.VirtualMachine.Name},
		"spec.type":                 {ps.Type, ss.Type},
		"spec.name":                 {ps.Name, ss.Name},
		"spec.cpu_count":            {ps.CPUCount, ss.CPUCount},
		"spec.memory":               {ps.Memory, ss.Memory},
		"spec.ssh_key":              {ps.SSHKey, ss.SSHKey},
		"spec.vpc":                  {ps.VPC, ss.VPC},
		"spec.subnet":               {ps.Subnet, ss.Subnet},
		"spec.datacenter":           {ps.Datacenter, ss.Datacenter},
		"spec.user_data":            {ps.UserData, ss.UserData},
		"spec.timezone":             {ps.Timezone, ss.Timezone},
		"spec.shared_storage":       {ps.SharedStorage, ss.SharedStorage},
		"spec.block_storage_type":   {ps.BlockStorageType, ss.BlockStorageType},
		"spec.image":                {ps.Image, ss.Image},
	}
	for name, pair := range strFields {
		if stringOr(pair.p, "") != stringOr(pair.s, "") {
			fail(name)
		}
	}
	if ps.AssignPublicIP.ValueBool() != ss.AssignPublicIP.ValueBool() {
		fail("spec.assign_public_ip")
	}
	if ps.CreateAdditionalBlock.ValueBool() != ss.CreateAdditionalBlock.ValueBool() {
		fail("spec.create_additional_block")
	}
	if ps.BootDiskSize.ValueInt64() != ss.BootDiskSize.ValueInt64() {
		fail("spec.boot_disk_size")
	}
	if ps.AdditionalBlockSize.ValueInt64() != ss.AdditionalBlockSize.ValueInt64() {
		fail("spec.additional_block_size")
	}
	if !ps.DNSServers.Equal(ss.DNSServers) {
		fail("spec.dns_servers")
	}
	return diags
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
		DNSServers:            sortedStringSliceFromTFSet(ctx, m.Spec.DNSServers, diags),
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
			VirtualMachine: resourceRefFromSDK(v.Spec.VirtualMachine),
			VMID:           nullableString(v.Spec.VMId),
			Type:           nullableString(v.Spec.Type),
			Name:           nullableString(v.Spec.Name),
			CPUCount:       nullableString(v.Spec.CPUCount),
			Memory:         nullableString(v.Spec.Memory),
			SecurityGroup:  nullableString(v.Spec.SecurityGroup),
			SSHKey:         nullableString(v.Spec.SSHKey),
			VPC:            nullableString(v.Spec.VPC),
			Subnet:         nullableString(v.Spec.Subnet),
			AssignPublicIP: types.BoolValue(v.Spec.AssignPublicIP),
			Sharing:        vmSharingFromSDK(ctx, v.Spec.Sharing, diags),
			Datacenter:     nullableString(v.Spec.Datacenter),
			// guest_password is intentionally NOT populated here (write-only,
			// FR-10) — callers (Create/Read/Update) overwrite this field with
			// the prior plan/state value after calling this converter.
			GuestPassword:         types.StringNull(),
			DNSServers:            setFromStringSlice(v.Spec.DNSServers),
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
		PowerState: derivePowerState(v.Status),
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

// derivePowerState infers the declarative on/off power state from the
// backend-observed status.
//
// The VirtualMachineStatus wire type carries no dedicated power indicator, so
// we heuristically derive one from the last recorded action (plan.md 3.4):
//   - last action "stop" (accepted terminal: ACTIONCOMPLETE) => "off"
//   - no action yet (freshly created) or any other action ("start",
//     "reboot", ...) => "on" (VMs power on by default when provisioned, and
//     a completed reboot leaves the VM running)
//
// This is intentionally conservative: it only ever reports "off" when the
// most recent lifecycle action was unambiguously a stop.
func derivePowerState(s apiv1.VirtualMachineStatus) types.String {
	if wait.NormalizeStatus(s.Action) == "stop" {
		return types.StringValue("off")
	}
	return types.StringValue("on")
}
