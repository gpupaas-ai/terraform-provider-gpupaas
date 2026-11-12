package resources

import (
	"context"
	"encoding/json"

	apiv1 "github.com/gpupaas-ai/gpupaas-go/apis/v1alpha1"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Models for gpupaas_bare_metal_instance (first-class bare metal, dev.envmgmt.io/v1).

type bareMetalSpecModel struct {
	Type           types.String  `tfsdk:"type"`
	Image          types.String  `tfsdk:"image"`
	SSHKey         types.String  `tfsdk:"ssh_key"`
	UserPassword   types.String  `tfsdk:"user_password"`
	Datacenter     types.String  `tfsdk:"datacenter"`
	VPC            types.String  `tfsdk:"vpc"`
	Subnet         types.String  `tfsdk:"subnet"`
	SecurityGroup  types.String  `tfsdk:"security_group"`
	AssignPublicIP types.Bool    `tfsdk:"assign_public_ip"`
	HSSStorage     types.String  `tfsdk:"hss_storage"`
	ObjectStorage  types.String  `tfsdk:"object_storage"`
	UserData       types.String  `tfsdk:"user_data"`
	ContractTerm   types.Int64   `tfsdk:"contract_term"`
	Sharing        *SharingModel `tfsdk:"sharing"`

	GPUName           types.String `tfsdk:"gpu_name"`
	ProvisionerType   types.String `tfsdk:"provisioner_type"`
	ComputeInstanceID types.String `tfsdk:"compute_instance_id"`
}

type bareMetalStatusModel struct {
	Phase      types.String `tfsdk:"phase"`
	Status     types.String `tfsdk:"status"`
	Reason     types.String `tfsdk:"reason"`
	Action     types.String `tfsdk:"action"`
	PowerState types.String `tfsdk:"power_state"`
	HostName   types.String `tfsdk:"host_name"`
	PrivateIP  types.String `tfsdk:"private_ip"`
	PublicIP   types.String `tfsdk:"public_ip"`
	UserName   types.String `tfsdk:"user_name"`
	Platform   types.String `tfsdk:"platform"`
	GPUs       types.String `tfsdk:"gpus"`
	VCPUs      types.String `tfsdk:"vcpus"`
	MemoryGiB  types.String `tfsdk:"memory_gib"`
	Network    types.String `tfsdk:"network"`
}

type bareMetalTimeoutsModel struct {
	Create types.String `tfsdk:"create"`
	Update types.String `tfsdk:"update"`
	Delete types.String `tfsdk:"delete"`
	Action types.String `tfsdk:"action"`
}

type bareMetalResourceModel struct {
	ID            types.String            `tfsdk:"id"`
	Metadata      MetadataModel           `tfsdk:"metadata"`
	Spec          bareMetalSpecModel      `tfsdk:"spec"`
	DesiredAction types.String            `tfsdk:"desired_action"`
	Timeouts      *bareMetalTimeoutsModel `tfsdk:"timeouts"`
	Status        bareMetalStatusModel    `tfsdk:"status"`
}

// sharingConfigured reports whether a sharing block was set. State decoding turns a null block
// into one whose attributes are all null.
func sharingConfigured(s *SharingModel) bool {
	return s != nil && !(s.ShareMode.IsNull() && s.Workspaces.IsNull() && s.Projects.IsNull())
}

func bareMetalID(project, workspace, name string) string {
	if workspace == "" {
		return project + "/" + name
	}
	return project + "/" + workspace + "/" + name
}

// passwordChanged reports whether the password must be sent: on create when set, and on update
// only when it differs from the one the state was applied with. The API never returns it, so it
// cannot be compared with the server.
func passwordChanged(plan bareMetalSpecModel, prior *bareMetalSpecModel) bool {
	if !set(plan.UserPassword) {
		return false
	}
	return prior == nil || prior.UserPassword.ValueString() != plan.UserPassword.ValueString()
}

// buildBareMetal turns the planned model into the object sent to the API. With current set (an
// update) the instance read from the API is the starting point, so fields the provider does not
// manage - the platform's derived fields, anything the SDK does not model - are sent back
// unchanged; prior is the state the plan was made against. Optional attributes the platform
// defaults (datacenter, vpc, subnet, public IP, contract term) keep the platform's value unless
// configured.
func buildBareMetal(ctx context.Context, plan bareMetalResourceModel, prior *bareMetalSpecModel, current *apiv1.BareMetal, diags *diag.Diagnostics) *apiv1.BareMetal {
	obj := &apiv1.BareMetal{
		TypeMeta: apiv1.TypeMeta{APIVersion: apiv1.APIVersion, Kind: apiv1.KindBareMetal},
		Metadata: metadataToSDK(plan.Metadata),
	}
	var spec apiv1.BareMetalSpec
	if current != nil {
		cp := current.DeepCopyObject().(*apiv1.BareMetal)
		spec = cp.Spec
		spec.UserPassword = ""
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
	spec.HSSStorage = stringOr(p.HSSStorage, "")
	spec.ObjectStorage = stringOr(p.ObjectStorage, "")
	if passwordChanged(p, prior) {
		spec.UserPassword = p.UserPassword.ValueString()
	}
	// The API may hold user data in another encoding than the plain text configured, so an
	// unchanged value keeps what the platform returned.
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
	if !p.ContractTerm.IsNull() && !p.ContractTerm.IsUnknown() {
		spec.ContractTerm = int32(p.ContractTerm.ValueInt64())
	}
	switch {
	case sharingConfigured(p.Sharing):
		spec.Sharing = sharingToSDK(ctx, p.Sharing, diags)
	case current != nil && isDefaultSharing(current.Spec.Sharing):
		// Nothing configured and the platform holds its default: send it back as it is.
	case current == nil:
		// The Developer Hub always sends a sharing block; "None" keeps the instance private.
		spec.Sharing = &apiv1.DevSharingSpec{ShareMode: "None"}
	default:
		spec.Sharing = nil
	}
	obj.Spec = spec
	return obj
}

// computedOrDrift returns what to store for an Optional+Computed attribute: the platform's value
// when nothing was configured (or on import), the configured value while the platform agrees, and
// the platform's value when it now differs (drift).
func computedOrDrift(prior types.String, server string) types.String {
	if prior.IsNull() || prior.IsUnknown() {
		return nullableString(server)
	}
	if server != "" && server != prior.ValueString() {
		return types.StringValue(server)
	}
	return prior
}

// bareMetalModelFromServer maps the API instance to Terraform state. base holds the configured
// values; fromRead is true when refreshing existing state.
func bareMetalModelFromServer(ctx context.Context, base bareMetalResourceModel, srv *apiv1.BareMetal, fromRead bool, diags *diag.Diagnostics) bareMetalResourceModel {
	out := base
	importing := base.Spec.Type.IsNull() || base.Spec.Type.IsUnknown()
	out.Metadata = metadataFromServer(base.Metadata, srv.Metadata, fromRead)
	// The platform accepts a display name and a description on a bare-metal machine but may not store them (the
	// description was seen dropped live), so a refresh must not read their absence as a change: the configured values
	// are kept, as for a VM.
	if srv.Metadata.DisplayName == "" && set(base.Metadata.DisplayName) {
		out.Metadata.DisplayName = base.Metadata.DisplayName
	}
	if srv.Metadata.Description == "" && set(base.Metadata.Description) {
		out.Metadata.Description = base.Metadata.Description
	}
	out.ID = types.StringValue(bareMetalID(out.Metadata.Project.ValueString(), out.Metadata.Workspace.ValueString(), srv.Metadata.Name))

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
	spec.HSSStorage = keepOrDrift(base.Spec.HSSStorage, s.HSSStorage, importing)
	spec.ObjectStorage = keepOrDrift(base.Spec.ObjectStorage, s.ObjectStorage, importing)
	// The password is write-only; user data may come back encoded. Both stay as configured, and
	// user data is taken from the platform only on import.
	if importing {
		spec.UserData = nullableString(s.UserData)
	}
	spec.Datacenter = computedOrDrift(base.Spec.Datacenter, s.Datacenter)
	spec.VPC = computedOrDrift(base.Spec.VPC, s.VPC)
	spec.Subnet = computedOrDrift(base.Spec.Subnet, s.Subnet)

	// The API leaves a false public IP out, so a configured value is only compared on refresh.
	if base.Spec.AssignPublicIP.IsNull() || base.Spec.AssignPublicIP.IsUnknown() || fromRead {
		spec.AssignPublicIP = types.BoolValue(s.AssignPublicIP)
	}
	switch {
	case base.Spec.ContractTerm.IsNull() || base.Spec.ContractTerm.IsUnknown():
		spec.ContractTerm = types.Int64Value(int64(s.ContractTerm))
	case s.ContractTerm != 0 && int64(s.ContractTerm) != base.Spec.ContractTerm.ValueInt64():
		spec.ContractTerm = types.Int64Value(int64(s.ContractTerm))
	}

	spec.Sharing = nil
	if s.Sharing != nil && !(isDefaultSharing(s.Sharing) && !sharingConfigured(base.Spec.Sharing)) {
		spec.Sharing = sharingFromSDK(ctx, s.Sharing, base.Spec.Sharing, diags)
	}

	spec.GPUName = nullableString(s.GPUName)
	spec.ProvisionerType = nullableString(s.ProvisionerType)
	spec.ComputeInstanceID = nullableString(s.ComputeInstanceID)
	out.Spec = spec

	st := srv.Status
	out.Status = bareMetalStatusModel{
		Phase:      nullableString(st.Phase()),
		Status:     nullableString(st.Status),
		Reason:     nullableString(st.Reason),
		Action:     nullableString(st.Action),
		PowerState: types.StringValue(st.PowerState()),
		HostName:   types.StringNull(), PrivateIP: types.StringNull(), PublicIP: types.StringNull(), UserName: types.StringNull(),
		Platform: types.StringNull(), GPUs: types.StringNull(), VCPUs: types.StringNull(), MemoryGiB: types.StringNull(), Network: types.StringNull(),
	}
	if o := st.Output; o != nil {
		out.Status.HostName, out.Status.PrivateIP, out.Status.PublicIP, out.Status.UserName = nullableString(o.HostName), nullableString(o.PrivateIP), nullableString(o.PublicIP), nullableString(o.UserName)
		out.Status.Platform, out.Status.GPUs, out.Status.VCPUs, out.Status.MemoryGiB, out.Status.Network = nullableString(o.Platform), nullableString(o.GPUs), nullableString(o.VCPUs), nullableString(o.MemoryGiB), nullableString(o.Network)
	}
	return out
}

// sameBareMetalRequest reports whether sending want would change nothing the platform holds in
// current. It keeps client-side changes (timeouts, desired_action) from re-applying an instance.
func sameBareMetalRequest(want, current *apiv1.BareMetal) bool {
	if want.Spec.UserPassword != "" {
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

// bareMetalActionNeeded decides whether desired_action must run. start and stop are level
// triggers (they run when the power state differs); every other action is edge triggered (it
// runs when the configured value changes, or on create).
func bareMetalActionNeeded(action, priorAction, power string) bool {
	switch action {
	case "start":
		return power != apiv1.BareMetalPowerRunning
	case "stop":
		return power != apiv1.BareMetalPowerStopped
	default:
		return action != priorAction
	}
}
