package resources

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	apiv1 "github.com/gpupaas-ai/gpupaas-go/apis/v1alpha1"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Models for gpupaas_virtual_machine (first-class virtual machine, dev.envmgmt.io/v1).

type vmSpecModel struct {
	Type           types.String  `tfsdk:"type"`
	Preset         types.String  `tfsdk:"preset"`
	CPUCount       types.String  `tfsdk:"cpu_count"`
	Memory         types.String  `tfsdk:"memory"`
	GPUCount       types.String  `tfsdk:"gpu_count"`
	Image          types.String  `tfsdk:"image"`
	SSHKey         types.String  `tfsdk:"ssh_key"`
	GuestPassword  types.String  `tfsdk:"guest_password"`
	Datacenter     types.String  `tfsdk:"datacenter"`
	VPC            types.String  `tfsdk:"vpc"`
	Subnet         types.String  `tfsdk:"subnet"`
	SecurityGroup  types.String  `tfsdk:"security_group"`
	AssignPublicIP types.Bool    `tfsdk:"assign_public_ip"`
	SharedStorage  types.String  `tfsdk:"shared_storage"`
	UserData       types.String  `tfsdk:"user_data"`
	BootDiskSize   types.Int64   `tfsdk:"boot_disk_size"`
	Sharing        *SharingModel `tfsdk:"sharing"`

	GPUModel  types.String `tfsdk:"gpu_model"`
	GPUVendor types.String `tfsdk:"gpu_vendor"`
	VMID      types.String `tfsdk:"vm_id"`
}

type vmStatusModel struct {
	Phase      types.String `tfsdk:"phase"`
	Status     types.String `tfsdk:"status"`
	Reason     types.String `tfsdk:"reason"`
	Action     types.String `tfsdk:"action"`
	PowerState types.String `tfsdk:"power_state"`
	HostName   types.String `tfsdk:"host_name"`
	InstanceID types.String `tfsdk:"instance_id"`
	OSName     types.String `tfsdk:"os_name"`
	PrivateIP  types.String `tfsdk:"private_ip"`
	PublicIP   types.String `tfsdk:"public_ip"`
	ServerHost types.String `tfsdk:"server_host"`
	UserName   types.String `tfsdk:"user_name"`
}

type vmTimeoutsModel struct {
	Create types.String `tfsdk:"create"`
	Update types.String `tfsdk:"update"`
	Delete types.String `tfsdk:"delete"`
	Action types.String `tfsdk:"action"`
}

type vmResourceModel struct {
	ID            types.String     `tfsdk:"id"`
	Metadata      MetadataModel    `tfsdk:"metadata"`
	Spec          vmSpecModel      `tfsdk:"spec"`
	DesiredAction types.String     `tfsdk:"desired_action"`
	Timeouts      *vmTimeoutsModel `tfsdk:"timeouts"`
	Status        vmStatusModel    `tfsdk:"status"`
}

func vmID(project, workspace, name string) string {
	if workspace == "" {
		return project + "/" + name
	}
	return project + "/" + workspace + "/" + name
}

// vmPasswordChanged reports whether the password must be sent: on create when set, and on update
// only when it differs from the one the state was applied with. The API never returns it, so it
// cannot be compared with the server.
func vmPasswordChanged(plan vmSpecModel, prior *vmSpecModel) bool {
	if !set(plan.GuestPassword) {
		return false
	}
	return prior == nil || prior.GuestPassword.ValueString() != plan.GuestPassword.ValueString()
}

// vmSize is the cpu, memory and gpu count a VM is created with.
type vmSize struct{ cpu, memory, gpu string }

// vmPresetByName finds the SKU's preset row of that name (case-insensitive).
func vmPresetByName(p *apiv1.VMProfile, name string) (apiv1.VMPreset, bool) {
	for _, row := range p.Spec.Sizes() {
		if strings.EqualFold(row.Name, name) {
			return row, true
		}
	}
	return apiv1.VMPreset{}, false
}

// vmPresetForSize names the SKU's preset row that has exactly this size, or "" when none or several match. The
// platform stores the size, not the preset name, so an import uses this to fill spec.preset.
func vmPresetForSize(p *apiv1.VMProfile, cpu, memory, gpu string) string {
	if p == nil || cpu == "" || memory == "" {
		return ""
	}
	name, matches := "", 0
	for _, row := range p.Spec.Sizes() {
		if row.CPU == cpu && row.Memory == memory && row.GPU == gpu {
			name, matches = row.Name, matches+1
		}
	}
	if matches != 1 {
		return ""
	}
	return name
}

// resolveVMSize returns the size to send: the SKU's preset when spec.preset is set, otherwise the configured
// cpu_count, memory and gpu_count. ok is false when a preset was named that the SKU does not have, or when it
// could not be resolved because the SKU is unreadable.
func resolveVMSize(s vmSpecModel, profile *apiv1.VMProfile) (size vmSize, ok bool) {
	if set(s.Preset) {
		if profile == nil {
			return vmSize{}, false
		}
		row, found := vmPresetByName(profile, s.Preset.ValueString())
		if !found {
			return vmSize{}, false
		}
		return vmSize{cpu: row.CPU, memory: row.Memory, gpu: row.GPU}, true
	}
	return vmSize{cpu: stringOr(s.CPUCount, ""), memory: stringOr(s.Memory, ""), gpu: stringOr(s.GPUCount, "")}, true
}

// buildVM turns the planned model into the object sent to the API. With current set (an update) the VM read
// from the API is the starting point, so fields the provider does not manage - the platform's derived fields,
// anything the SDK does not model - are sent back unchanged; prior is the state the plan was made against.
// profile is the SKU, used to resolve spec.preset (nil when it could not be read).
func buildVM(ctx context.Context, plan vmResourceModel, prior *vmSpecModel, current *apiv1.VMInstance, profile *apiv1.VMProfile, diags *diag.Diagnostics) *apiv1.VMInstance {
	obj := &apiv1.VMInstance{
		TypeMeta: apiv1.TypeMeta{APIVersion: apiv1.APIVersion, Kind: apiv1.KindVMInstance},
		Metadata: metadataToSDK(plan.Metadata),
	}
	var spec apiv1.VMInstanceSpec
	if current != nil {
		cp := current.DeepCopyObject().(*apiv1.VMInstance)
		spec = cp.Spec
		spec.GuestPassword = ""
		// Keep what the platform holds (system labels, for example) and apply only what was configured.
		meta := cp.Metadata
		planned := metadataToSDK(plan.Metadata)
		meta.Name, meta.Project, meta.Workspace = planned.Name, planned.Project, planned.Workspace
		if planned.DisplayName != "" {
			meta.DisplayName = planned.DisplayName
		}
		if planned.Description != "" {
			meta.Description = planned.Description
		}
		if planned.Labels != nil {
			meta.Labels = planned.Labels
		}
		if planned.Annotations != nil {
			meta.Annotations = planned.Annotations
		}
		obj.Metadata = meta
	}

	p := plan.Spec
	spec.Type = p.Type.ValueString()
	spec.Image = p.Image.ValueString()
	spec.SSHKey = stringOr(p.SSHKey, "")
	spec.SecurityGroup = stringOr(p.SecurityGroup, "")
	spec.SharedStorage = stringOr(p.SharedStorage, "")
	// The size is fixed when the VM is created (a different size replaces it), so an update keeps the one the
	// platform holds. That also keeps an update working when the preset can no longer be resolved, for example
	// after the SKU was edited or when it cannot be read.
	if current == nil {
		if size, ok := resolveVMSize(p, profile); ok {
			spec.CPUCount, spec.Memory, spec.GPUCount = size.cpu, size.memory, size.gpu
		} else if set(p.Preset) {
			diags.AddAttributeError(path.Root("spec").AtName("preset"), "Cannot resolve the preset", "The preset "+p.Preset.ValueString()+" is not one of the SKU's presets, or the SKU could not be read. "+
				"Use the gpupaas_virtual_machine_sku data source to list the presets, or set cpu_count and memory.")
		}
	}
	if vmPasswordChanged(p, prior) {
		spec.GuestPassword = p.GuestPassword.ValueString()
	}
	// The API may hold user data in another encoding than the plain text configured, so an unchanged
	// value keeps what the platform returned.
	if prior == nil || current == nil || stringOr(prior.UserData, "") != stringOr(p.UserData, "") {
		spec.UserData = stringOr(p.UserData, "")
	}
	if set(p.Datacenter) {
		spec.Datacenter = p.Datacenter.ValueString()
	}
	if set(p.VPC) {
		spec.VPC = p.VPC.ValueString()
	}
	if set(p.Subnet) {
		spec.Subnet = p.Subnet.ValueString()
	}
	if !p.AssignPublicIP.IsNull() && !p.AssignPublicIP.IsUnknown() {
		spec.AssignPublicIP = p.AssignPublicIP.ValueBool()
	}
	if !p.BootDiskSize.IsNull() && !p.BootDiskSize.IsUnknown() {
		spec.BootDiskSize = int32(p.BootDiskSize.ValueInt64())
	}
	switch {
	case sharingConfigured(p.Sharing):
		spec.Sharing = sharingToSDK(ctx, p.Sharing, diags)
	case current != nil && isDefaultSharing(current.Spec.Sharing):
		// Nothing configured and the platform holds its default: send it back as it is.
	case current == nil:
		// The Developer Hub always sends a sharing block; "None" keeps the VM private.
		spec.Sharing = &apiv1.DevSharingSpec{ShareMode: "None"}
	default:
		spec.Sharing = nil
	}
	obj.Spec = spec
	return obj
}

// vmModelFromServer maps the API VM to Terraform state. base holds the configured values; fromRead is
// true when refreshing existing state.
func vmModelFromServer(ctx context.Context, base vmResourceModel, srv *apiv1.VMInstance, fromRead bool, diags *diag.Diagnostics) vmResourceModel {
	out := base
	importing := base.Spec.Type.IsNull() || base.Spec.Type.IsUnknown()
	out.Metadata = metadataFromServer(base.Metadata, srv.Metadata, fromRead)
	// The platform accepts a display name and a description on a VM but does not store them (seen live), so a
	// refresh must not read their absence as a change: the configured values are kept.
	if srv.Metadata.DisplayName == "" && set(base.Metadata.DisplayName) {
		out.Metadata.DisplayName = base.Metadata.DisplayName
	}
	if srv.Metadata.Description == "" && set(base.Metadata.Description) {
		out.Metadata.Description = base.Metadata.Description
	}
	out.ID = types.StringValue(vmID(out.Metadata.Project.ValueString(), out.Metadata.Workspace.ValueString(), srv.Metadata.Name))

	s := srv.Spec
	spec := base.Spec
	if s.Type != "" {
		spec.Type = types.StringValue(s.Type)
	}
	spec.Image = keepOrDrift(base.Spec.Image, s.Image, importing)
	if importing && s.Image != "" {
		spec.Image = types.StringValue(s.Image)
	}
	spec.SSHKey = keepOrDrift(base.Spec.SSHKey, s.SSHKey, importing)
	spec.SecurityGroup = keepOrDrift(base.Spec.SecurityGroup, s.SecurityGroup, importing)
	spec.SharedStorage = keepOrDrift(base.Spec.SharedStorage, s.SharedStorage, importing)
	// The password is write-only; user data may come back encoded. Both stay as configured, and user data is
	// taken from the platform only on import.
	if importing {
		spec.UserData = nullableString(s.UserData)
	}
	spec.CPUCount = computedOrDrift(base.Spec.CPUCount, s.CPUCount)
	spec.Memory = computedOrDrift(base.Spec.Memory, s.Memory)
	spec.GPUCount = computedOrDrift(base.Spec.GPUCount, s.GPUCount)
	spec.Datacenter = computedOrDrift(base.Spec.Datacenter, s.Datacenter)
	spec.VPC = computedOrDrift(base.Spec.VPC, s.VPC)
	spec.Subnet = computedOrDrift(base.Spec.Subnet, s.Subnet)

	// The API leaves a false public IP out, so a configured value is only compared on refresh.
	if base.Spec.AssignPublicIP.IsNull() || base.Spec.AssignPublicIP.IsUnknown() || fromRead {
		spec.AssignPublicIP = types.BoolValue(s.AssignPublicIP)
	}
	switch {
	case base.Spec.BootDiskSize.IsNull() || base.Spec.BootDiskSize.IsUnknown():
		if s.BootDiskSize != 0 {
			spec.BootDiskSize = types.Int64Value(int64(s.BootDiskSize))
		} else {
			spec.BootDiskSize = types.Int64Null()
		}
	case s.BootDiskSize != 0 && int64(s.BootDiskSize) != base.Spec.BootDiskSize.ValueInt64():
		spec.BootDiskSize = types.Int64Value(int64(s.BootDiskSize))
	}
	// spec.preset is a Terraform-side shortcut: it is kept as configured, and it is null after an import.
	if importing {
		spec.Preset = types.StringNull()
	}

	spec.Sharing = nil
	if s.Sharing != nil && !(isDefaultSharing(s.Sharing) && !sharingConfigured(base.Spec.Sharing)) {
		spec.Sharing = sharingFromSDK(ctx, s.Sharing, base.Spec.Sharing, diags)
	}

	spec.GPUModel = nullableString(s.GPUModel)
	spec.GPUVendor = nullableString(s.GPUVendor)
	spec.VMID = nullableString(s.VMID)
	out.Spec = spec

	st := srv.Status
	out.Status = vmStatusModel{
		Phase:      nullableString(st.Phase()),
		Status:     nullableString(st.Status),
		Reason:     nullableString(st.Reason),
		Action:     nullableString(st.Action),
		PowerState: types.StringValue(st.PowerState()),
		HostName:   types.StringNull(), InstanceID: types.StringNull(), OSName: types.StringNull(), PrivateIP: types.StringNull(),
		PublicIP: types.StringNull(), ServerHost: types.StringNull(), UserName: types.StringNull(),
	}
	if o := st.Output; o != nil {
		out.Status.HostName, out.Status.InstanceID, out.Status.OSName = nullableString(o.HostName), nullableString(o.InstanceID), nullableString(o.OSName)
		out.Status.PrivateIP, out.Status.PublicIP = nullableString(o.PrivateIP), nullableString(vmPublicIP(o.PublicIP))
		out.Status.ServerHost, out.Status.UserName = nullableString(o.ServerHost), nullableString(o.UserName)
	}
	return out
}

// sameVMRequest reports whether sending want would change nothing the platform holds in current. It keeps
// client-side changes (timeouts, desired_action) from re-applying a VM.
func sameVMRequest(want, current *apiv1.VMInstance) bool {
	if want.Spec.GuestPassword != "" {
		return false
	}
	ws, errW := json.Marshal(want.Spec)
	cs, errC := json.Marshal(current.Spec)
	if errW != nil || errC != nil || string(ws) != string(cs) {
		return false
	}
	w, c := want.Metadata, current.Metadata
	return w.DisplayName == c.DisplayName && w.Description == c.Description && sameStringMap(w.Labels, c.Labels) && sameStringMap(w.Annotations, c.Annotations)
}

// keepUnchangedNames drops a display name or description that did not change in the configuration from the
// request. The platform stores neither for a VM, so what it returns never equals what was configured, and
// sending them again would make every update look like a change and re-apply the VM.
func keepUnchangedNames(obj, current *apiv1.VMInstance, plan, prior MetadataModel) {
	if plan.DisplayName.ValueString() == prior.DisplayName.ValueString() {
		obj.Metadata.DisplayName = current.Metadata.DisplayName
	}
	if plan.Description.ValueString() == prior.Description.ValueString() {
		obj.Metadata.Description = current.Metadata.Description
	}
}

// vmActionNeeded decides whether desired_action must run. start and stop are level triggers (they run
// when the power state differs); reboot is edge triggered (it runs when the configured value changes, or
// on create).
func vmActionNeeded(action, priorAction, power string) bool {
	switch action {
	case "start":
		return power != apiv1.VMPowerRunning
	case "stop":
		return power != apiv1.VMPowerStopped
	default:
		return action != priorAction
	}
}

// numeric reports whether s is a plain non-negative number, such as "3" or "0.5".
func numeric(s string) (float64, bool) {
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return f, err == nil && f >= 0
}

// vmPublicIP returns the public address a VM reports, or "" when it has none: the platform writes the text
// "No public IP configured" in that field, which must not be mistaken for an address.
func vmPublicIP(v string) string {
	if strings.EqualFold(strings.TrimSpace(v), "No public IP configured") {
		return ""
	}
	return v
}
