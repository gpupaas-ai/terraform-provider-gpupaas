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
	_ resource.Resource                = (*baremetalMachineResource)(nil)
	_ resource.ResourceWithImportState = (*baremetalMachineResource)(nil)
	_ resource.ResourceWithConfigure   = (*baremetalMachineResource)(nil)
)

// baremetalDesiredActionValues is the canonical allow-list mirroring the verbs
// exposed by typed.BaremetalMachineInterface (PowerOn, PowerOff, Reboot,
// Provision, ReinstallOS). "none" is the explicit no-op. CreateConsoleSession
// and GetStatusInfo are intentionally excluded — they return ephemeral payloads
// rather than mutating the resource, so they are not lifecycle actions.
var baremetalDesiredActionValues = []string{
	"none", "power_on", "power_off", "reboot", "provision", "reinstall_os",
}

// NewBaremetalMachineResource constructs the gpupaas_baremetal_machine resource.
func NewBaremetalMachineResource() resource.Resource { return &baremetalMachineResource{} }

type baremetalMachineResource struct{ pd *client.ProviderData }

type baremetalMachineResourceModel struct {
	ID            types.String                  `tfsdk:"id"`
	APIVersion    types.String                  `tfsdk:"api_version"`
	Kind          types.String                  `tfsdk:"kind"`
	Metadata      MetadataModel                 `tfsdk:"metadata"`
	Spec          baremetalMachineSpec          `tfsdk:"spec"`
	Status        baremetalMachineStatus        `tfsdk:"status"`
	DesiredAction types.String                  `tfsdk:"desired_action"`
	ActionInputs  *baremetalMachineActionInputs `tfsdk:"action_inputs"`
}

type baremetalMachineActionInputs struct {
	// Image is the deployment image used by the `reinstall_os` action.
	Image *baremetalImageModel `tfsdk:"image"`
}

type baremetalMachineSpec struct {
	Architecture             types.String                   `tfsdk:"architecture"`
	AutomatedCleaningMode    types.String                   `tfsdk:"automated_cleaning_mode"`
	BaremetalProvisionerName types.String                   `tfsdk:"baremetal_provisioner_name"`
	BootMode                 types.String                   `tfsdk:"boot_mode"`
	Datacenter               types.String                   `tfsdk:"datacenter"`
	DeviceID                 types.String                   `tfsdk:"device_id"`
	Hostname                 types.String                   `tfsdk:"hostname"`
	Image                    *baremetalImageModel           `tfsdk:"image"`
	MACAddress               types.String                   `tfsdk:"mac_address"`
	Online                   types.Bool                     `tfsdk:"online"`
	Raid                     *baremetalRaidModel            `tfsdk:"raid"`
	RootDeviceHints          *baremetalRootDeviceHintsModel `tfsdk:"root_device_hints"`
	SSHKey                   types.String                   `tfsdk:"ssh_key"`
	SystemUserData           types.String                   `tfsdk:"system_user_data"`
	UserData                 types.String                   `tfsdk:"user_data"`
}

type baremetalImageModel struct {
	Checksum     types.String `tfsdk:"checksum"`
	ChecksumType types.String `tfsdk:"checksum_type"`
	Format       types.String `tfsdk:"format"`
	URL          types.String `tfsdk:"url"`
}

type baremetalRootDeviceHintsModel struct {
	DeviceName         types.String `tfsdk:"device_name"`
	HCTL               types.String `tfsdk:"hctl"`
	MinSizeGigabytes   types.Int64  `tfsdk:"min_size_gigabytes"`
	Model              types.String `tfsdk:"model"`
	Rotational         types.Bool   `tfsdk:"rotational"`
	SerialNumber       types.String `tfsdk:"serial_number"`
	Vendor             types.String `tfsdk:"vendor"`
	WWN                types.String `tfsdk:"wwn"`
	WWNVendorExtension types.String `tfsdk:"wwn_vendor_extension"`
	WWNWithExtension   types.String `tfsdk:"wwn_with_extension"`
}

type baremetalRaidModel struct {
	HardwareRAIDVolumes []baremetalHardwareRAIDVolumeModel `tfsdk:"hardware_raid_volumes"`
	SoftwareRAIDVolumes []baremetalSoftwareRAIDVolumeModel `tfsdk:"software_raid_volumes"`
}

type baremetalHardwareRAIDVolumeModel struct {
	Controller            types.String `tfsdk:"controller"`
	Level                 types.String `tfsdk:"level"`
	Name                  types.String `tfsdk:"name"`
	NumberOfPhysicalDisks types.Int64  `tfsdk:"number_of_physical_disks"`
	PhysicalDisks         types.List   `tfsdk:"physical_disks"`
	Rotational            types.Bool   `tfsdk:"rotational"`
	SizeGibibytes         types.Int64  `tfsdk:"size_gibibytes"`
}

type baremetalSoftwareRAIDVolumeModel struct {
	Level         types.String                    `tfsdk:"level"`
	PhysicalDisks []baremetalRootDeviceHintsModel `tfsdk:"physical_disks"`
	SizeGibibytes types.Int64                     `tfsdk:"size_gibibytes"`
}

type baremetalMachineStatus struct {
	Conditions []baremetalMachineConditionModel `tfsdk:"conditions"`
}

type baremetalMachineConditionModel struct {
	Type        types.String `tfsdk:"type"`
	Status      types.String `tfsdk:"status"`
	Reason      types.String `tfsdk:"reason"`
	LastUpdated types.String `tfsdk:"last_updated"`
}

func (r *baremetalMachineResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_baremetal_machine"
}

func (r *baremetalMachineResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "GPU PaaS baremetal machine (`apiVersion: " + apiv1.APIVersion + "`, `kind: BaremetalMachine`). " +
			"Project-scoped (no workspace). Imperative lifecycle actions (power on/off, reboot, " +
			"provision, reinstall OS) are driven through the `desired_action` attribute.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"api_version": schema.StringAttribute{Computed: true},
			"kind":        schema.StringAttribute{Computed: true},
			"metadata":    MetadataResourceAttribute(false, true),
			"spec":        baremetalMachineSpecAttribute(),
			"status":      baremetalMachineStatusAttribute(),
			"desired_action": schema.StringAttribute{
				Optional: true,
				Computed: true,
				MarkdownDescription: "Imperative lifecycle action to drive on this machine. One of " +
					"`none`, `power_on`, `power_off`, `reboot`, `provision`, `reinstall_os`. " +
					"Changing this attribute (from its prior planned/state value) triggers the " +
					"corresponding SDK call on the next apply. Setting it back to `none` (or " +
					"omitting it) is a no-op — the provider never auto-re-triggers actions on " +
					"refresh. `reinstall_os` requires `action_inputs.image`.",
				Validators: []validator.String{
					stringvalidator.OneOf(baremetalDesiredActionValues...),
				},
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"action_inputs": schema.SingleNestedAttribute{
				Optional: true,
				MarkdownDescription: "Optional payload accompanying `desired_action`. Currently used by " +
					"`reinstall_os`, which reimages the host with `action_inputs.image`.",
				Attributes: map[string]schema.Attribute{
					"image": baremetalImageResourceAttribute(
						"Image to deploy when `desired_action = \"reinstall_os\"`."),
				},
			},
		},
	}
}

func baremetalMachineSpecAttribute() schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{
		Required: true,
		Attributes: map[string]schema.Attribute{
			"architecture": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "CPU architecture (e.g. `x86_64`, `aarch64`). Usually observed via inspection.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"automated_cleaning_mode": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Set to `disabled` to skip cleaning between deployments.",
			},
			"baremetal_provisioner_name": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Name of the provisioner that owns this host.",
			},
			"boot_mode": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Boot mode: `UEFI`, `Legacy`, or `UEFISecureBoot`. Defaults to `UEFI`.",
			},
			"datacenter":  schema.StringAttribute{Optional: true, MarkdownDescription: "Inventory datacenter the host lives in."},
			"device_id":   schema.StringAttribute{Optional: true, MarkdownDescription: "Inventory device identifier."},
			"hostname":    schema.StringAttribute{Optional: true, MarkdownDescription: "Hostname assigned to the machine."},
			"image":       baremetalImageResourceAttribute("Image to deploy on the host."),
			"mac_address": schema.StringAttribute{Optional: true, MarkdownDescription: "MAC address of the primary provisioning NIC."},
			"online": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "Desired power state when the host is stable. Also mutated by the `power_on` / `power_off` actions.",
			},
			"raid":              baremetalRaidResourceAttribute(),
			"root_device_hints": baremetalRootDeviceHintsResourceAttribute("Narrows the disk used for OS deployment."),
			"ssh_key":           schema.StringAttribute{Optional: true, MarkdownDescription: "SSH public key injected for first-boot login."},
			"system_user_data":  schema.StringAttribute{Optional: true, MarkdownDescription: "System cloud-init user data interpreted at first boot."},
			"user_data":         schema.StringAttribute{Optional: true, MarkdownDescription: "Cloud-init user data interpreted at first boot."},
		},
	}
}

func baremetalImageResourceAttribute(description string) schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{
		Optional:            true,
		MarkdownDescription: description,
		Attributes: map[string]schema.Attribute{
			"checksum":      schema.StringAttribute{Optional: true, MarkdownDescription: "Image checksum. Required for all formats except `live-iso`."},
			"checksum_type": schema.StringAttribute{Optional: true, MarkdownDescription: "Checksum algorithm: `md5`, `sha256`, `sha512`, or `auto`."},
			"format":        schema.StringAttribute{Optional: true, MarkdownDescription: "Image format: `raw`, `qcow2`, `live-iso`, ..."},
			"url":           schema.StringAttribute{Optional: true, MarkdownDescription: "Location of the image to deploy."},
		},
	}
}

func baremetalRootDeviceHintsResourceAttribute(description string) schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{
		Optional:            true,
		MarkdownDescription: description,
		Attributes: map[string]schema.Attribute{
			"device_name":          schema.StringAttribute{Optional: true},
			"hctl":                 schema.StringAttribute{Optional: true},
			"min_size_gigabytes":   schema.Int64Attribute{Optional: true},
			"model":                schema.StringAttribute{Optional: true},
			"rotational":           schema.BoolAttribute{Optional: true},
			"serial_number":        schema.StringAttribute{Optional: true},
			"vendor":               schema.StringAttribute{Optional: true},
			"wwn":                  schema.StringAttribute{Optional: true},
			"wwn_vendor_extension": schema.StringAttribute{Optional: true},
			"wwn_with_extension":   schema.StringAttribute{Optional: true},
		},
	}
}

func baremetalRaidResourceAttribute() schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{
		Optional:            true,
		MarkdownDescription: "Hardware and software RAID configuration.",
		Attributes: map[string]schema.Attribute{
			"hardware_raid_volumes": schema.ListNestedAttribute{
				Optional: true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"controller":               schema.StringAttribute{Optional: true},
						"level":                    schema.StringAttribute{Optional: true},
						"name":                     schema.StringAttribute{Optional: true},
						"number_of_physical_disks": schema.Int64Attribute{Optional: true},
						"physical_disks":           schema.ListAttribute{Optional: true, ElementType: types.StringType},
						"rotational":               schema.BoolAttribute{Optional: true},
						"size_gibibytes":           schema.Int64Attribute{Optional: true},
					},
				},
			},
			"software_raid_volumes": schema.ListNestedAttribute{
				Optional: true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"level":          schema.StringAttribute{Optional: true},
						"size_gibibytes": schema.Int64Attribute{Optional: true},
						"physical_disks": schema.ListNestedAttribute{
							Optional: true,
							NestedObject: schema.NestedAttributeObject{
								Attributes: map[string]schema.Attribute{
									"device_name":          schema.StringAttribute{Optional: true},
									"hctl":                 schema.StringAttribute{Optional: true},
									"min_size_gigabytes":   schema.Int64Attribute{Optional: true},
									"model":                schema.StringAttribute{Optional: true},
									"rotational":           schema.BoolAttribute{Optional: true},
									"serial_number":        schema.StringAttribute{Optional: true},
									"vendor":               schema.StringAttribute{Optional: true},
									"wwn":                  schema.StringAttribute{Optional: true},
									"wwn_vendor_extension": schema.StringAttribute{Optional: true},
									"wwn_with_extension":   schema.StringAttribute{Optional: true},
								},
							},
						},
					},
				},
			},
		},
	}
}

func baremetalMachineStatusAttribute() schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{
		Computed: true,
		Attributes: map[string]schema.Attribute{
			"conditions": schema.ListNestedAttribute{
				Computed: true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"type":         schema.StringAttribute{Computed: true},
						"status":       schema.StringAttribute{Computed: true},
						"reason":       schema.StringAttribute{Computed: true},
						"last_updated": schema.StringAttribute{Computed: true},
					},
				},
			},
		},
	}
}

func (r *baremetalMachineResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.pd = providerData(req.ProviderData, &resp.Diagnostics)
}

func (r *baremetalMachineResource) client(project string) typed.BaremetalMachineInterface {
	return r.pd.Clientset.V1alpha1().BaremetalMachines(project)
}

func (r *baremetalMachineResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	if r.pd == nil {
		resp.Diagnostics.AddError("Provider not configured", "Provider data is nil; check provider configuration.")
		return
	}
	var plan baremetalMachineResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	project := plan.Metadata.Project.ValueString()
	obj := baremetalMachineModelToSDK(ctx, plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	out, err := r.client(project).Create(ctx, obj, gpupaas.CreateOptions{})
	if err != nil {
		handleAPIError(&resp.Diagnostics, err, "Create baremetal machine", nil)
		return
	}
	model := baremetalMachineSDKToModel(ctx, out, project, &resp.Diagnostics)
	// desired_action / action_inputs are Terraform-side trigger fields; preserve
	// the planned values verbatim across Create so the next plan stays quiet.
	model.DesiredAction = normalizeDesiredAction(plan.DesiredAction)
	model.ActionInputs = plan.ActionInputs
	resp.Diagnostics.Append(resp.State.Set(ctx, model)...)
}

func (r *baremetalMachineResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	if r.pd == nil {
		return
	}
	var state baremetalMachineResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	project := state.Metadata.Project.ValueString()
	out, err := r.client(project).Get(ctx, state.Metadata.Name.ValueString(), gpupaas.GetOptions{})
	if err != nil {
		handleAPIError(&resp.Diagnostics, err, "Read baremetal machine", func() { resp.State.RemoveResource(ctx) })
		return
	}
	model := baremetalMachineSDKToModel(ctx, out, project, &resp.Diagnostics)
	// Preserve trigger fields across refresh — the SDK never echoes them.
	model.DesiredAction = normalizeDesiredAction(state.DesiredAction)
	model.ActionInputs = state.ActionInputs
	resp.Diagnostics.Append(resp.State.Set(ctx, model)...)
}

func (r *baremetalMachineResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	if r.pd == nil {
		return
	}
	var plan, state baremetalMachineResourceModel
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
	obj := baremetalMachineModelToSDK(ctx, plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	out, err := cli.Create(ctx, obj, gpupaas.CreateOptions{})
	if err != nil {
		handleAPIError(&resp.Diagnostics, err, "Update baremetal machine", nil)
		return
	}

	// 2) Imperative action dispatch. Compare prior state vs new plan; only the
	//    "newAction != oldAction" transition fires an API call.
	oldAction := normalizedActionValue(state.DesiredAction)
	newAction := normalizedActionValue(plan.DesiredAction)
	if newAction != "" && newAction != "none" && newAction != oldAction {
		switch newAction {
		case "power_on":
			out, err = cli.PowerOn(ctx, name, gpupaas.ActionOptions{})
		case "power_off":
			out, err = cli.PowerOff(ctx, name, gpupaas.ActionOptions{})
		case "reboot":
			out, err = cli.Reboot(ctx, name, gpupaas.ActionOptions{})
		case "provision":
			out, err = cli.Provision(ctx, name, gpupaas.ActionOptions{})
		case "reinstall_os":
			image := baremetalReinstallImage(plan.ActionInputs)
			if image == nil {
				resp.Diagnostics.AddError(
					"Missing reinstall image",
					"desired_action = \"reinstall_os\" requires action_inputs.image to be set.",
				)
				return
			}
			out, err = cli.ReinstallOS(ctx, name, image, gpupaas.ActionOptions{})
		}
		if err != nil {
			handleAPIError(&resp.Diagnostics, err, "Execute "+newAction+" on baremetal machine", nil)
			return
		}
	}

	model := baremetalMachineSDKToModel(ctx, out, project, &resp.Diagnostics)
	model.DesiredAction = normalizeDesiredAction(plan.DesiredAction)
	model.ActionInputs = plan.ActionInputs
	resp.Diagnostics.Append(resp.State.Set(ctx, model)...)
}

func (r *baremetalMachineResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	if r.pd == nil {
		return
	}
	var state baremetalMachineResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	project := state.Metadata.Project.ValueString()
	err := r.client(project).Delete(ctx, state.Metadata.Name.ValueString(), gpupaas.DeleteOptions{IgnoreNotFound: true})
	if err != nil && !gpupaas.IsNotFound(err) {
		handleAPIError(&resp.Diagnostics, err, "Delete baremetal machine", nil)
	}
}

func (r *baremetalMachineResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
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

func baremetalMachineModelToSDK(ctx context.Context, m baremetalMachineResourceModel, diags *diag.Diagnostics) *apiv1.BaremetalMachine {
	spec := apiv1.BaremetalMachineSpec{
		Architecture:             stringOr(m.Spec.Architecture, ""),
		AutomatedCleaningMode:    stringOr(m.Spec.AutomatedCleaningMode, ""),
		BaremetalProvisionerName: stringOr(m.Spec.BaremetalProvisionerName, ""),
		BootMode:                 stringOr(m.Spec.BootMode, ""),
		Datacenter:               stringOr(m.Spec.Datacenter, ""),
		DeviceID:                 stringOr(m.Spec.DeviceID, ""),
		Hostname:                 stringOr(m.Spec.Hostname, ""),
		Image:                    baremetalImageToSDK(m.Spec.Image),
		MACAddress:               stringOr(m.Spec.MACAddress, ""),
		Online:                   boolPtrFromTF(m.Spec.Online),
		Raid:                     baremetalRaidToSDK(ctx, m.Spec.Raid, diags),
		RootDeviceHints:          baremetalRootDeviceHintsToSDK(m.Spec.RootDeviceHints),
		SSHKey:                   stringOr(m.Spec.SSHKey, ""),
		SystemUserData:           stringOr(m.Spec.SystemUserData, ""),
		UserData:                 stringOr(m.Spec.UserData, ""),
	}
	return &apiv1.BaremetalMachine{
		TypeMeta: apiv1.TypeMeta{APIVersion: apiv1.APIVersion, Kind: apiv1.KindBaremetalMachine},
		Metadata: metadataToSDK(m.Metadata),
		Spec:     spec,
	}
}

func baremetalMachineSDKToModel(ctx context.Context, b *apiv1.BaremetalMachine, project string, diags *diag.Diagnostics) baremetalMachineResourceModel {
	if b == nil {
		return baremetalMachineResourceModel{}
	}
	if b.Metadata.Project == "" {
		b.Metadata.Project = project
	}
	out := baremetalMachineResourceModel{
		ID:         types.StringValue(b.Metadata.Project + "/" + b.Metadata.Name),
		APIVersion: types.StringValue(firstNonEmpty(b.APIVersion, apiv1.APIVersion)),
		Kind:       types.StringValue(firstNonEmpty(b.Kind, apiv1.KindBaremetalMachine)),
		Metadata:   metadataFromSDK(b.Metadata),
		Spec: baremetalMachineSpec{
			Architecture:             nullableString(b.Spec.Architecture),
			AutomatedCleaningMode:    nullableString(b.Spec.AutomatedCleaningMode),
			BaremetalProvisionerName: nullableString(b.Spec.BaremetalProvisionerName),
			BootMode:                 nullableString(b.Spec.BootMode),
			Datacenter:               nullableString(b.Spec.Datacenter),
			DeviceID:                 nullableString(b.Spec.DeviceID),
			Hostname:                 nullableString(b.Spec.Hostname),
			Image:                    baremetalImageFromSDK(b.Spec.Image),
			MACAddress:               nullableString(b.Spec.MACAddress),
			Online:                   boolFromPtr(b.Spec.Online),
			Raid:                     baremetalRaidFromSDK(b.Spec.Raid),
			RootDeviceHints:          baremetalRootDeviceHintsFromSDK(b.Spec.RootDeviceHints),
			SSHKey:                   nullableString(b.Spec.SSHKey),
			SystemUserData:           nullableString(b.Spec.SystemUserData),
			UserData:                 nullableString(b.Spec.UserData),
		},
		Status:        baremetalMachineStatusFromSDK(b.Status),
		DesiredAction: types.StringNull(),
	}
	return out
}

func baremetalImageToSDK(m *baremetalImageModel) *apiv1.BaremetalImage {
	if m == nil {
		return nil
	}
	return &apiv1.BaremetalImage{
		Checksum:     stringOr(m.Checksum, ""),
		ChecksumType: stringOr(m.ChecksumType, ""),
		Format:       stringOr(m.Format, ""),
		URL:          stringOr(m.URL, ""),
	}
}

func baremetalImageFromSDK(i *apiv1.BaremetalImage) *baremetalImageModel {
	if i == nil {
		return nil
	}
	return &baremetalImageModel{
		Checksum:     nullableString(i.Checksum),
		ChecksumType: nullableString(i.ChecksumType),
		Format:       nullableString(i.Format),
		URL:          nullableString(i.URL),
	}
}

func baremetalRootDeviceHintsToSDK(m *baremetalRootDeviceHintsModel) *apiv1.BaremetalRootDeviceHints {
	if m == nil {
		return nil
	}
	return &apiv1.BaremetalRootDeviceHints{
		DeviceName:         stringOr(m.DeviceName, ""),
		HCTL:               stringOr(m.HCTL, ""),
		MinSizeGigabytes:   m.MinSizeGigabytes.ValueInt64(),
		Model:              stringOr(m.Model, ""),
		Rotational:         boolPtrFromTF(m.Rotational),
		SerialNumber:       stringOr(m.SerialNumber, ""),
		Vendor:             stringOr(m.Vendor, ""),
		WWN:                stringOr(m.WWN, ""),
		WWNVendorExtension: stringOr(m.WWNVendorExtension, ""),
		WWNWithExtension:   stringOr(m.WWNWithExtension, ""),
	}
}

func baremetalRootDeviceHintsFromSDK(h *apiv1.BaremetalRootDeviceHints) *baremetalRootDeviceHintsModel {
	if h == nil {
		return nil
	}
	return &baremetalRootDeviceHintsModel{
		DeviceName:         nullableString(h.DeviceName),
		HCTL:               nullableString(h.HCTL),
		MinSizeGigabytes:   int64OrNull(h.MinSizeGigabytes),
		Model:              nullableString(h.Model),
		Rotational:         boolFromPtr(h.Rotational),
		SerialNumber:       nullableString(h.SerialNumber),
		Vendor:             nullableString(h.Vendor),
		WWN:                nullableString(h.WWN),
		WWNVendorExtension: nullableString(h.WWNVendorExtension),
		WWNWithExtension:   nullableString(h.WWNWithExtension),
	}
}

func baremetalRaidToSDK(ctx context.Context, m *baremetalRaidModel, diags *diag.Diagnostics) *apiv1.BaremetalRaid {
	if m == nil {
		return nil
	}
	out := &apiv1.BaremetalRaid{}
	for _, hw := range m.HardwareRAIDVolumes {
		out.HardwareRAIDVolumes = append(out.HardwareRAIDVolumes, apiv1.BaremetalHardwareRAIDVolumes{
			Controller:            stringOr(hw.Controller, ""),
			Level:                 stringOr(hw.Level, ""),
			Name:                  stringOr(hw.Name, ""),
			NumberOfPhysicalDisks: hw.NumberOfPhysicalDisks.ValueInt64(),
			PhysicalDisks:         stringSliceFromTF(ctx, hw.PhysicalDisks, diags),
			Rotational:            boolPtrFromTF(hw.Rotational),
			SizeGibibytes:         hw.SizeGibibytes.ValueInt64(),
		})
	}
	for _, sw := range m.SoftwareRAIDVolumes {
		vol := apiv1.BaremetalSoftwareRAIDVolumes{
			Level:         stringOr(sw.Level, ""),
			SizeGibibytes: sw.SizeGibibytes.ValueInt64(),
		}
		for i := range sw.PhysicalDisks {
			if h := baremetalRootDeviceHintsToSDK(&sw.PhysicalDisks[i]); h != nil {
				vol.PhysicalDisks = append(vol.PhysicalDisks, *h)
			}
		}
		out.SoftwareRAIDVolumes = append(out.SoftwareRAIDVolumes, vol)
	}
	return out
}

func baremetalRaidFromSDK(r *apiv1.BaremetalRaid) *baremetalRaidModel {
	if r == nil {
		return nil
	}
	out := &baremetalRaidModel{}
	for _, hw := range r.HardwareRAIDVolumes {
		out.HardwareRAIDVolumes = append(out.HardwareRAIDVolumes, baremetalHardwareRAIDVolumeModel{
			Controller:            nullableString(hw.Controller),
			Level:                 nullableString(hw.Level),
			Name:                  nullableString(hw.Name),
			NumberOfPhysicalDisks: int64OrNull(hw.NumberOfPhysicalDisks),
			PhysicalDisks:         listFromStringSlice(hw.PhysicalDisks),
			Rotational:            boolFromPtr(hw.Rotational),
			SizeGibibytes:         int64OrNull(hw.SizeGibibytes),
		})
	}
	for _, sw := range r.SoftwareRAIDVolumes {
		vol := baremetalSoftwareRAIDVolumeModel{
			Level:         nullableString(sw.Level),
			SizeGibibytes: int64OrNull(sw.SizeGibibytes),
		}
		for i := range sw.PhysicalDisks {
			if h := baremetalRootDeviceHintsFromSDK(&sw.PhysicalDisks[i]); h != nil {
				vol.PhysicalDisks = append(vol.PhysicalDisks, *h)
			}
		}
		out.SoftwareRAIDVolumes = append(out.SoftwareRAIDVolumes, vol)
	}
	return out
}

func baremetalMachineStatusFromSDK(s apiv1.BaremetalMachineStatus) baremetalMachineStatus {
	out := baremetalMachineStatus{}
	for _, c := range s.Conditions {
		out.Conditions = append(out.Conditions, baremetalMachineConditionModel{
			Type:        nullableString(c.Type),
			Status:      nullableString(c.Status),
			Reason:      nullableString(c.Reason),
			LastUpdated: nullableString(c.LastUpdated),
		})
	}
	return out
}

// baremetalReinstallImage extracts the BaremetalImage used by the reinstall_os
// action from the Terraform action_inputs payload. Returns nil when no image
// is provided.
func baremetalReinstallImage(in *baremetalMachineActionInputs) *apiv1.BaremetalImage {
	if in == nil || in.Image == nil {
		return nil
	}
	return baremetalImageToSDK(in.Image)
}

// ---- small type helpers --------------------------------------------------

// boolPtrFromTF returns a *bool for a Terraform bool, or nil when null/unknown
// so the SDK omits the field on the wire.
func boolPtrFromTF(b types.Bool) *bool {
	if b.IsNull() || b.IsUnknown() {
		return nil
	}
	v := b.ValueBool()
	return &v
}

// boolFromPtr converts an SDK *bool to a Terraform bool, mapping nil to null.
func boolFromPtr(p *bool) types.Bool {
	if p == nil {
		return types.BoolNull()
	}
	return types.BoolValue(*p)
}

// int64OrNull returns a null Int64 for zero values to keep diffs stable for
// optional numeric fields the backend leaves unset.
func int64OrNull(v int64) types.Int64 {
	if v == 0 {
		return types.Int64Null()
	}
	return types.Int64Value(v)
}
