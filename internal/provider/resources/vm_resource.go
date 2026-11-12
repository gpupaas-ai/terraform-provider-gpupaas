package resources

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	gpupaas "github.com/gpupaas-ai/gpupaas-go"
	apiv1 "github.com/gpupaas-ai/gpupaas-go/apis/v1alpha1"
	typed "github.com/gpupaas-ai/gpupaas-go/clientset/typed/v1alpha1"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/gpupaas-ai/terraform-provider-gpupaas/internal/client"
)

var (
	_ resource.Resource                = (*vmResource)(nil)
	_ resource.ResourceWithConfigure   = (*vmResource)(nil)
	_ resource.ResourceWithImportState = (*vmResource)(nil)
	_ resource.ResourceWithModifyPlan  = (*vmResource)(nil)
)

// Default time limits. A VM takes a few minutes to provision (image copy, cloud-init); power actions
// take about a minute.
const (
	defaultVMCreateTimeout = 45 * time.Minute
	defaultVMUpdateTimeout = 20 * time.Minute
	defaultVMDeleteTimeout = 30 * time.Minute
	defaultVMActionTimeout = 15 * time.Minute
)

// vmStableRunning is how many consecutive polls must show the VM settled before a create, update or
// action counts as finished: right after a write the platform can still show the previous state for a moment.
const vmStableRunning = 2

// vmStaleFailed is how many polls after a delete may still show a failed record that has not started its
// teardown before that counts as a failed delete.
const vmStaleFailed = 8

var vmDesiredActions = []string{"none", "start", "stop", "reboot"}

// NewVMResource constructs the gpupaas_virtual_machine resource.
func NewVMResource() resource.Resource { return &vmResource{} }

type vmResource struct{ pd *client.ProviderData }

func (r *vmResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_virtual_machine"
}

func (r *vmResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	md := MetadataResourceAttribute(true, true)
	for _, k := range []string{"name", "project", "workspace"} {
		sa := md.Attributes[k].(schema.StringAttribute)
		sa.PlanModifiers = append(sa.PlanModifiers, stringplanmodifier.RequiresReplace())
		md.Attributes[k] = sa
	}
	useState := []planmodifier.String{stringplanmodifier.UseStateForUnknown()}
	replaceUseState := []planmodifier.String{stringplanmodifier.UseStateForUnknown(), stringplanmodifier.RequiresReplace()}
	resp.Schema = schema.Schema{
		MarkdownDescription: "A first-class virtual machine (`dev.envmgmt.io/v1` `VirtualMachine`), created from a VM SKU (VmProfile). " +
			"Creating the resource provisions the VM on a host of the SKU's datacenter and waits until it is running. " +
			"Use the `gpupaas_virtual_machine_sku` data source to see which images, sizes (presets), datacenters and options a SKU allows; Terraform checks your values against it at plan time. " +
			"Power actions (`start`, `stop`, `reboot`) are driven by `desired_action`. " +
			"The platform does not store `metadata.display_name` and `metadata.description` for a VM: Terraform keeps them as configured, but they do not show up in the console.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "`<project>/<workspace>/<name>`, or `<project>/<name>` for a project-scoped VM.",
				PlanModifiers:       useState,
			},
			"metadata": md,
			"spec": schema.SingleNestedAttribute{
				Required: true,
				Attributes: map[string]schema.Attribute{
					"type": schema.StringAttribute{
						Required:            true,
						MarkdownDescription: "The VM SKU (VmProfile) to create the VM from. Changing it replaces the VM.",
						PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
					},
					"preset": schema.StringAttribute{
						Optional: true,
						MarkdownDescription: "Name of one of the SKU's size presets, such as `Small`. Terraform looks the preset up in the SKU and sets `cpu_count`, `memory` (and `gpu_count`) from it. " +
							"Set this or `cpu_count` and `memory`, not both.",
					},
					"cpu_count": schema.StringAttribute{
						Optional: true,
						Computed: true,
						MarkdownDescription: "Number of vCPUs, as the SKU lists it. Together with `memory` (and `gpu_count` for a GPU SKU) it must match one preset row of the SKU. " +
							"Changing it replaces the VM.",
						PlanModifiers: replaceUseState,
					},
					"memory": schema.StringAttribute{
						Optional:            true,
						Computed:            true,
						MarkdownDescription: "Memory in GiB, as the SKU lists it. Must match the preset row of `cpu_count`. Changing it replaces the VM.",
						PlanModifiers:       replaceUseState,
					},
					"gpu_count": schema.StringAttribute{
						Optional:            true,
						Computed:            true,
						MarkdownDescription: "Number of GPUs; required for a GPU SKU, where it must match a GPU preset row. Changing it replaces the VM.",
						PlanModifiers:       replaceUseState,
					},
					"image": schema.StringAttribute{
						Required:            true,
						MarkdownDescription: "Operating system image, one of the SKU's images. Changing it replaces the VM.",
						PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
					},
					"ssh_key": schema.StringAttribute{
						Optional:            true,
						MarkdownDescription: "Name of an existing SSH key in the VM's workspace to install. Set this, `guest_password`, or both.",
					},
					"guest_password": schema.StringAttribute{
						Optional:  true,
						Sensitive: true,
						MarkdownDescription: "Password for the VM's user. Write-only: the API never returns it, so Terraform cannot detect a change made elsewhere. " +
							"It is sent on create and again only when you change it here. It is stored in the Terraform state, so protect the state.",
					},
					"datacenter": schema.StringAttribute{
						Optional:            true,
						Computed:            true,
						PlanModifiers:       useState,
						MarkdownDescription: "Datacenter the VM is placed in, one of the SKU's datacenters. Defaults to the SKU's only datacenter.",
					},
					"vpc": schema.StringAttribute{
						Optional:            true,
						Computed:            true,
						PlanModifiers:       useState,
						MarkdownDescription: "VPC to attach the VM to. Defaults to the platform's choice.",
					},
					"subnet": schema.StringAttribute{
						Optional:            true,
						Computed:            true,
						PlanModifiers:       useState,
						MarkdownDescription: "Subnet of the VPC. Defaults to the platform's choice.",
					},
					"security_group": schema.StringAttribute{Optional: true, MarkdownDescription: "Name of an existing security group of the workspace (one). Day-2: change it, or remove the attribute to detach the group, on a running VM (the console's Security Group edit); the VM is updated in place, not replaced."},
					"assign_public_ip": schema.BoolAttribute{
						Optional:            true,
						Computed:            true,
						PlanModifiers:       []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
						MarkdownDescription: "Assign a public IP. Honored only when the SKU's public IP mode is `Optional`; `Always` and `None` fix it.",
					},
					"shared_storage": schema.StringAttribute{Optional: true, MarkdownDescription: "Name of a shared filesystem storage to mount."},
					"user_data": schema.StringAttribute{
						Optional:  true,
						Sensitive: true,
						MarkdownDescription: "Cloud-init script as plain text. Kept as configured: the API's stored form is not compared. " +
							"Marked sensitive because scripts often carry secrets, so it is hidden in plans and CLI output; it is still stored in the Terraform state, so protect the state.",
					},
					"boot_disk_size": schema.Int64Attribute{
						Optional: true,
						Computed: true,
						MarkdownDescription: "Boot disk size in GB, within the SKU's range and a multiple of its step above the minimum. " +
							"Defaults to the SKU's minimum: Terraform sends it explicitly, because the platform's own default is 40 GB, which can be more than the SKU or the host allows. Changing it replaces the VM.",
						PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown(), int64planmodifier.RequiresReplace()},
					},
					"sharing": SharingResourceAttribute(),
					"gpu_model": schema.StringAttribute{
						Computed:            true,
						PlanModifiers:       useState,
						MarkdownDescription: "GPU model, inherited from the SKU.",
					},
					"gpu_vendor": schema.StringAttribute{
						Computed:            true,
						PlanModifiers:       useState,
						MarkdownDescription: "GPU vendor, inherited from the SKU.",
					},
					"vm_id": schema.StringAttribute{
						Computed:            true,
						PlanModifiers:       useState,
						MarkdownDescription: "Identifier of the VM in the platform's inventory, set once it is provisioned.",
					},
				},
			},
			"desired_action": schema.StringAttribute{
				Optional: true,
				MarkdownDescription: "A power action to run on the VM: `start`, `stop` or `reboot`. " +
					"`start` and `stop` run only when the VM is not already in that power state; `reboot` runs when the value changes. An action runs during an apply that changes `desired_action`; Terraform does not undo a power change made outside Terraform. If an action fails, an error leaves the previous value in the state so the next apply retries it; on create it is only a warning, so the VM is not tainted. " +
					"The platform decides which actions a VM accepts (the live VM SKUs list `start`, `stop`, `reboot` and `migrate`); when an action is refused the error lists what is accepted. " +
					"Leave unset or `none` to do nothing.",
				Validators: []validator.String{stringvalidator.OneOf(vmDesiredActions...)},
			},
			"timeouts": schema.SingleNestedAttribute{
				Optional:            true,
				MarkdownDescription: "Time limits as durations such as `30m`. Defaults: create 45m, update 20m, delete 30m, action 15m.",
				Attributes: map[string]schema.Attribute{
					"create": schema.StringAttribute{Optional: true},
					"update": schema.StringAttribute{Optional: true},
					"delete": schema.StringAttribute{Optional: true},
					"action": schema.StringAttribute{Optional: true},
				},
			},
			"status": schema.SingleNestedAttribute{
				Computed: true,
				Attributes: map[string]schema.Attribute{
					"phase":       schema.StringAttribute{Computed: true, MarkdownDescription: "Normalized status: `in_progress`, `running`, `stopped`, `failed`, `deleting`, `delete_pending`, ..."},
					"status":      schema.StringAttribute{Computed: true, MarkdownDescription: "Raw status as reported by the API."},
					"reason":      schema.StringAttribute{Computed: true},
					"action":      schema.StringAttribute{Computed: true, MarkdownDescription: "Last or in-flight operation: `deploy`, `start`, `stop`, `reboot`, `destroy`, ..."},
					"power_state": schema.StringAttribute{Computed: true, MarkdownDescription: "`running`, `stopped` or `unknown`."},
					"host_name":   schema.StringAttribute{Computed: true},
					"instance_id": schema.StringAttribute{Computed: true},
					"os_name":     schema.StringAttribute{Computed: true},
					"private_ip":  schema.StringAttribute{Computed: true},
					"public_ip":   schema.StringAttribute{Computed: true},
					"server_host": schema.StringAttribute{Computed: true, MarkdownDescription: "The host the VM runs on."},
					"user_name":   schema.StringAttribute{Computed: true, MarkdownDescription: "User to log in as."},
				},
			},
		},
	}
}

func (r *vmResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.pd = providerData(req.ProviderData, &resp.Diagnostics)
}

func (r *vmResource) machines(project, workspace string) typed.VMInstanceInterface {
	if workspace == "" {
		return r.pd.Clientset.V1alpha1().VMInstances(project)
	}
	return r.pd.Clientset.V1alpha1().Workspaces(project).VMInstances(workspace)
}

func (t *vmTimeoutsModel) get(kind string, def time.Duration, diags *diag.Diagnostics) time.Duration {
	if t == nil {
		return def
	}
	var v types.String
	switch kind {
	case "create":
		v = t.Create
	case "update":
		v = t.Update
	case "delete":
		v = t.Delete
	case "action":
		v = t.Action
	}
	if v.IsNull() || v.IsUnknown() || v.ValueString() == "" {
		return def
	}
	d, err := time.ParseDuration(v.ValueString())
	if err != nil || d <= 0 {
		diags.AddAttributeError(path.Root("timeouts").AtName(kind), "Invalid timeout", fmt.Sprintf("%q is not a positive duration such as 30m: %v", v.ValueString(), err))
		return def
	}
	return d
}

// addVMFailure turns a failed run into a Terraform error with the status and the reason, plus the
// workflow error and log excerpt of the compute instance behind the VM: the VM's own reason is generic
// ("due to an internal error").
func (r *vmResource) addVMFailure(ctx context.Context, diags *diag.Diagnostics, summary, project, workspace, name string, err error) {
	var failed *typed.VMInstanceFailedError
	if !errors.As(err, &failed) {
		diags.AddError(summary, err.Error())
		return
	}
	msg := fmt.Sprintf("Status %q after %q: %s", failed.Phase, failed.Action, failed.Reason)
	details := hiddenInstanceFailure(ctx, hiddenInstances(r.pd, project, workspace), name, failed.Reason)
	if details == "" && failed.VMInstance != nil && failed.VMInstance.Status.ReasonDetail != "" {
		// The compute instance behind the VM is gone or unreadable (after a teardown, for example): the VM's
		// own detail still names the cause.
		details = "\n\nWorkflow details:\n  " + truncate(failed.VMInstance.Status.ReasonDetail, 700)
	}
	diags.AddError(summary, msg+details)
}

// vmSettled is the wait predicate for an update: the VM is up or down, not mid-change.
func vmSettled(obj *apiv1.VMInstance) (bool, error) {
	if obj.Status.Failed() {
		return false, &typed.VMInstanceFailedError{Name: obj.Metadata.Name, Phase: obj.Status.Phase(), Action: obj.Status.Action, Reason: obj.Status.Reason, VMInstance: obj}
	}
	switch obj.Status.Phase() {
	case apiv1.VMPhaseRunning, apiv1.VMPhaseStopped:
		return true, nil
	}
	return false, nil
}

// vmGone is the wait predicate for a delete. A failed record that has not started its teardown still
// reads "failed" with its deploy action for a while after the DELETE, so a few such polls are tolerated;
// a failed teardown (destroy action, destroyfailed, delete_pending) ends the wait at once.
func vmGone(maxStale int) func(*apiv1.VMInstance) (bool, error) {
	stale := 0
	return func(obj *apiv1.VMInstance) (bool, error) {
		if !obj.Status.Failed() {
			stale = 0
			return false, nil
		}
		if p := obj.Status.Phase(); p == apiv1.VMPhaseFailed && !strings.EqualFold(obj.Status.Action, "destroy") {
			if stale++; stale <= maxStale {
				return false, nil
			}
		}
		return typed.VMInstanceGone(obj)
	}
}

func (r *vmResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	if r.pd == nil {
		resp.Diagnostics.AddError("Provider not configured", "Provider data is nil; check provider configuration.")
		return
	}
	var plan vmResourceModel
	resp.Diagnostics.Append(decodePlan(ctx, req.Plan, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	timeout := plan.Timeouts.get("create", defaultVMCreateTimeout, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	project, workspace, name := plan.Metadata.Project.ValueString(), plan.Metadata.Workspace.ValueString(), plan.Metadata.Name.ValueString()
	machines := r.machines(project, workspace)
	profile := r.readVMProfile(ctx, project, plan.Spec.Type.ValueString())
	obj := buildVM(ctx, plan, nil, nil, profile, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	// The Developer Hub fills the datacenter when the SKU has only one.
	if profile != nil && obj.Spec.Datacenter == "" && len(profile.Spec.Datacenter) == 1 {
		obj.Spec.Datacenter = profile.Spec.Datacenter[0]
	}
	// The name must be free. Checking first is what makes it safe to adopt the VM a failed create leaves
	// behind: anything found under the name afterwards was created by this request.
	if _, gerr := machines.Get(ctx, name, gpupaas.GetOptions{}); gerr == nil {
		resp.Diagnostics.AddError("VM already exists", fmt.Sprintf("A VM named %q already exists in this project and workspace. "+
			"Import it with terraform import, or use another name.", name))
		return
	}
	stored, err := machines.Create(ctx, obj, gpupaas.CreateOptions{})
	if err != nil {
		if kept := r.keptAfterFailedCreate(ctx, machines, name, err); kept != nil {
			// The platform stores the VM (failed) even when starting its provisioning fails. Keep it in
			// state so that a destroy removes it instead of leaving it behind.
			resp.Diagnostics.Append(resp.State.Set(ctx, vmModelFromServer(ctx, plan, kept, false, &resp.Diagnostics))...)
			resp.Diagnostics.AddError("Create VM failed", fmt.Sprintf("%s\n\nThe platform kept the VM in status %q (%s). It is tracked in the Terraform state as tainted: fix the cause and apply again, or run terraform destroy to remove it.",
				err.Error(), kept.Status.Phase(), kept.Status.Reason))
			return
		}
		handleAPIError(&resp.Diagnostics, err, "Create VM", nil)
		return
	}
	// The VM exists from here on: save state now so that a failure below leaves a tainted resource that a
	// later apply or destroy cleans up, instead of an orphan.
	resp.Diagnostics.Append(resp.State.Set(ctx, vmModelFromServer(ctx, plan, stored, false, &resp.Diagnostics))...)

	done, err := typed.WaitForVMInstance(ctx, machines, name, typed.VMInstanceWaitOptions{Interval: pollInterval, Done: typed.VMInstanceRunning, Stable: vmStableRunning})
	if done != nil {
		resp.Diagnostics.Append(resp.State.Set(ctx, vmModelFromServer(ctx, plan, done, false, &resp.Diagnostics))...)
	}
	if err != nil {
		r.addVMFailure(ctx, &resp.Diagnostics, "VM provisioning failed", project, workspace, name, err)
		return
	}
	// An initial desired_action (for example "stop") runs once the VM is up. The VM exists and works, so a
	// failing action must not fail the create: Terraform would taint the VM and the next apply would destroy
	// and rebuild it.
	var actionDiags diag.Diagnostics
	if final, ok := r.applyDesiredAction(ctx, machines, plan, nil, done, &actionDiags); ok && final != nil {
		done = final
	}
	resp.Diagnostics.Append(errorsAsWarnings(actionDiags, "The VM was created and is not tainted. desired_action stays recorded in the state, so Terraform will not retry it: "+
		"set desired_action to \"none\", apply, then set it again to retry.")...)
	resp.Diagnostics.Append(resp.State.Set(ctx, vmModelFromServer(ctx, plan, done, false, &resp.Diagnostics))...)
}

func (r *vmResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	if r.pd == nil {
		return
	}
	var state vmResourceModel
	resp.Diagnostics.Append(decodeState(ctx, req.State, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	srv, err := r.machines(state.Metadata.Project.ValueString(), state.Metadata.Workspace.ValueString()).Get(ctx, state.Metadata.Name.ValueString(), gpupaas.GetOptions{})
	if err != nil {
		handleAPIError(&resp.Diagnostics, err, "Read VM", func() { resp.State.RemoveResource(ctx) })
		return
	}
	model := vmModelFromServer(ctx, state, srv, true, &resp.Diagnostics)
	// After an import the preset is unknown (the platform keeps the size, not the preset name): name the SKU's
	// preset that matches, so a configuration that uses spec.preset shows no difference.
	if state.Spec.Type.IsNull() || state.Spec.Type.IsUnknown() {
		if name := vmPresetForSize(r.readVMProfile(ctx, state.Metadata.Project.ValueString(), srv.Spec.Type), srv.Spec.CPUCount, srv.Spec.Memory, srv.Spec.GPUCount); name != "" {
			model.Spec.Preset = types.StringValue(name)
		}
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, model)...)
}

func (r *vmResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	if r.pd == nil {
		return
	}
	var plan, state vmResourceModel
	resp.Diagnostics.Append(decodePlan(ctx, req.Plan, &plan)...)
	resp.Diagnostics.Append(decodeState(ctx, req.State, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	timeout := plan.Timeouts.get("update", defaultVMUpdateTimeout, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	project, workspace, name := plan.Metadata.Project.ValueString(), plan.Metadata.Workspace.ValueString(), plan.Metadata.Name.ValueString()
	machines := r.machines(project, workspace)
	// Start from what the API holds now: apply replaces the whole spec, so everything the provider does not
	// manage must be sent back.
	current, err := machines.Get(ctx, name, gpupaas.GetOptions{})
	if err != nil {
		handleAPIError(&resp.Diagnostics, err, "Read VM", nil)
		return
	}
	obj := buildVM(ctx, plan, &state.Spec, current, r.readVMProfile(ctx, project, plan.Spec.Type.ValueString()), &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	keepUnchangedNames(obj, current, plan.Metadata, state.Metadata)
	// Until the desired_action has been handled, the state keeps the previous one: if the update or its wait
	// fails, the action must still show as pending in the next plan instead of being recorded as done.
	interim := plan
	interim.DesiredAction = state.DesiredAction
	if !sameVMRequest(obj, current) {
		stored, err := machines.Update(ctx, obj, gpupaas.UpdateOptions{})
		if err != nil {
			handleAPIError(&resp.Diagnostics, err, "Update VM", nil)
			return
		}
		resp.Diagnostics.Append(resp.State.Set(ctx, vmModelFromServer(ctx, interim, stored, false, &resp.Diagnostics))...)
		done, err := typed.WaitForVMInstance(ctx, machines, name, typed.VMInstanceWaitOptions{Interval: pollInterval, Done: vmSettled, Stable: vmStableRunning})
		if done != nil {
			current = done
			resp.Diagnostics.Append(resp.State.Set(ctx, vmModelFromServer(ctx, interim, done, false, &resp.Diagnostics))...)
		}
		if err != nil {
			r.addVMFailure(ctx, &resp.Diagnostics, "VM update failed", project, workspace, name, err)
			return
		}
	}
	// Only Terraform-side settings may have changed (timeouts, desired_action): nothing was sent, and
	// re-applying the VM could start a redeploy.
	errorsBefore := resp.Diagnostics.ErrorsCount()
	if final, ok := r.applyDesiredAction(ctx, machines, plan, &state, current, &resp.Diagnostics); ok && final != nil {
		current = final
	}
	recorded := plan
	if resp.Diagnostics.ErrorsCount() > errorsBefore {
		// The action failed: keep the previous desired_action in the state, so that the next plan shows the
		// change again and the action is retried instead of being forgotten.
		recorded.DesiredAction = state.DesiredAction
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, vmModelFromServer(ctx, recorded, current, false, &resp.Diagnostics))...)
}

// applyDesiredAction runs plan.DesiredAction when it is needed. It returns the VM after the action and
// whether an action ran. prior is nil on create.
func (r *vmResource) applyDesiredAction(ctx context.Context, machines typed.VMInstanceInterface, plan vmResourceModel,
	prior *vmResourceModel, current *apiv1.VMInstance, diags *diag.Diagnostics) (*apiv1.VMInstance, bool) {
	action := strings.ToLower(normalizedActionValue(plan.DesiredAction))
	if action == "" || action == "none" {
		return nil, false
	}
	priorAction := ""
	if prior != nil {
		priorAction = strings.ToLower(normalizedActionValue(prior.DesiredAction))
	}
	if !vmActionNeeded(action, priorAction, current.Status.PowerState()) {
		return nil, false
	}
	timeout := plan.Timeouts.get("action", defaultVMActionTimeout, diags)
	actx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	name := plan.Metadata.Name.ValueString()
	if _, err := machines.Action(actx, name, action, map[string]any{}); err != nil {
		handleAPIError(diags, err, fmt.Sprintf("Run action %q", action), nil)
		return nil, false
	}
	done, err := typed.WaitForVMInstance(actx, machines, name, typed.VMInstanceWaitOptions{Interval: pollInterval, Done: typed.VMInstanceActionDone(action), Stable: vmStableRunning})
	if err != nil {
		r.addVMFailure(ctx, diags, fmt.Sprintf("Action %q failed", action), plan.Metadata.Project.ValueString(), plan.Metadata.Workspace.ValueString(), name, err)
		return done, true
	}
	return done, true
}

func (r *vmResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	if r.pd == nil {
		return
	}
	var state vmResourceModel
	resp.Diagnostics.Append(decodeState(ctx, req.State, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	timeout := state.Timeouts.get("delete", defaultVMDeleteTimeout, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	project, workspace, name := state.Metadata.Project.ValueString(), state.Metadata.Workspace.ValueString(), state.Metadata.Name.ValueString()
	machines := r.machines(project, workspace)
	if err := machines.Delete(ctx, name, gpupaas.DeleteOptions{IgnoreNotFound: true}); err != nil {
		var apiErr *gpupaas.APIError
		if errors.As(err, &apiErr) && apiErr.StatusCode == 412 {
			// The platform parked an earlier delete for the operator (delete_pending) and refuses further
			// ones from end users.
			resp.Diagnostics.AddError("VM delete refused", err.Error()+"\n\nThe platform parked this VM's delete for an administrator (delete_pending); end users cannot delete it again. "+
				"It stays in the Terraform state, and its host capacity stays allocated, until an administrator removes it.")
			return
		}
		handleAPIError(&resp.Diagnostics, err, "Delete VM", nil)
		return
	}
	_, err := typed.WaitForVMInstance(ctx, machines, name, typed.VMInstanceWaitOptions{Interval: pollInterval, Done: vmGone(vmStaleFailed), GoneIsDone: true})
	if err == nil {
		err = waitForHiddenInstanceGone(ctx, r.pd, project, workspace, name)
	}
	if err != nil {
		r.addVMFailure(ctx, &resp.Diagnostics, "VM teardown failed", project, workspace, name, err)
		var failed *typed.VMInstanceFailedError
		if errors.As(err, &failed) {
			if failed.Phase == apiv1.VMPhaseDeletePending {
				resp.Diagnostics.AddWarning("Teardown needs an administrator", "The platform parked the delete for the operator (delete_pending); end users cannot delete the VM again. The VM is kept in state until it is gone.")
			} else {
				resp.Diagnostics.AddWarning("Teardown can be retried", "Run terraform destroy again to retry the teardown; the VM is kept in state until it is gone.")
			}
		}
	}
}

func (r *vmResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	project, workspace, name, err := ParseFlexibleScopedImportID(req.ID)
	if err != nil {
		resp.Diagnostics.AddError("Invalid import ID", "Use <project>/<workspace>/<name> or <project>/<name>: "+err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("metadata").AtName("project"), project)...)
	if workspace != "" {
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("metadata").AtName("workspace"), workspace)...)
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("metadata").AtName("name"), name)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), vmID(project, workspace, name))...)
}

// keptAfterFailedCreate returns the VM the platform stored although the create call failed, or nil. A 409
// means the name belonged to an existing VM, which is never adopted.
func (r *vmResource) keptAfterFailedCreate(ctx context.Context, machines typed.VMInstanceInterface, name string, createErr error) *apiv1.VMInstance {
	var apiErr *gpupaas.APIError
	if errors.As(createErr, &apiErr) && apiErr.StatusCode == 409 {
		return nil
	}
	kept, err := machines.Get(ctx, name, gpupaas.GetOptions{})
	if err != nil {
		return nil
	}
	return kept
}
