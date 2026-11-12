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
	_ resource.Resource                = (*bareMetalResource)(nil)
	_ resource.ResourceWithConfigure   = (*bareMetalResource)(nil)
	_ resource.ResourceWithImportState = (*bareMetalResource)(nil)
	_ resource.ResourceWithModifyPlan  = (*bareMetalResource)(nil)
)

// Default time limits. A bare-metal machine takes 15-25 minutes to provision (PXE boot, image
// write); power actions take a few minutes.
const (
	defaultBareMetalCreateTimeout = 60 * time.Minute
	defaultBareMetalUpdateTimeout = 30 * time.Minute
	defaultBareMetalDeleteTimeout = 30 * time.Minute
	defaultBareMetalActionTimeout = 20 * time.Minute
)

// bareMetalStableRunning is how many consecutive polls must show the machine settled before a
// create, update or action counts as finished: right after a write the platform can still show
// the previous state for a moment.
const bareMetalStableRunning = 2

// bareMetalStaleFailed is how many polls after a delete may still show a failed record that has
// not started its teardown before that counts as a failed delete.
const bareMetalStaleFailed = 8

var bareMetalDesiredActions = []string{"none", "start", "stop", "reboot", "power_cycle", "power_reset"}

// bareMetalAPIAction maps a desired_action value to the action name the API expects. The platform
// lists "Power cycle" and "Power reset" for BCM bare-metal SKUs and refuses "reboot" there.
func bareMetalAPIAction(action string) string {
	switch action {
	case "power_cycle":
		return "Power cycle"
	case "power_reset":
		return "Power reset"
	}
	return action
}

// NewBareMetalResource constructs the gpupaas_bare_metal_instance resource.
func NewBareMetalResource() resource.Resource { return &bareMetalResource{} }

type bareMetalResource struct{ pd *client.ProviderData }

func (r *bareMetalResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_bare_metal_instance"
}

func (r *bareMetalResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	md := MetadataResourceAttribute(true, true)
	for _, k := range []string{"name", "project", "workspace"} {
		sa := md.Attributes[k].(schema.StringAttribute)
		sa.PlanModifiers = append(sa.PlanModifiers, stringplanmodifier.RequiresReplace())
		md.Attributes[k] = sa
	}
	useState := []planmodifier.String{stringplanmodifier.UseStateForUnknown()}
	resp.Schema = schema.Schema{
		MarkdownDescription: "A first-class bare-metal machine (`dev.envmgmt.io/v1` `BareMetal`), created from a bare-metal SKU (BmProfile). " +
			"Creating the resource provisions a machine from the SKU's inventory and waits until it is running, which takes many minutes. " +
			"Use the `gpupaas_bare_metal_sku` data source to see which images, datacenters and options a SKU allows; Terraform checks your values against it at plan time. " +
			"Power actions (`start`, `stop`, `power_cycle`, `power_reset`) are driven by `desired_action`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "`<project>/<workspace>/<name>`, or `<project>/<name>` for a project-scoped instance.",
				PlanModifiers:       useState,
			},
			"metadata": md,
			"spec": schema.SingleNestedAttribute{
				Required: true,
				Attributes: map[string]schema.Attribute{
					"type": schema.StringAttribute{
						Required:            true,
						MarkdownDescription: "The bare-metal SKU (BmProfile) to create the machine from. Changing it replaces the machine.",
						PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
					},
					"image": schema.StringAttribute{
						Required:            true,
						MarkdownDescription: "Operating system image, one of the SKU's images. Changing it replaces the machine (the platform reinstalls an OS only through its `Reinstall OS` action).",
						PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
					},
					"ssh_key": schema.StringAttribute{
						Optional:            true,
						MarkdownDescription: "Name of an existing SSH key in the instance's workspace to install. Set this, `user_password`, or both.",
					},
					"user_password": schema.StringAttribute{
						Optional:  true,
						Sensitive: true,
						MarkdownDescription: "Password for the machine's user. Write-only: the API never returns it, so Terraform cannot detect a change made elsewhere. " +
							"It is sent on create and again only when you change it here. It is stored in the Terraform state, so protect the state.",
					},
					"datacenter": schema.StringAttribute{
						Optional:            true,
						Computed:            true,
						PlanModifiers:       useState,
						MarkdownDescription: "Datacenter the machine is placed in, one of the SKU's datacenters. Defaults to the SKU's only datacenter.",
					},
					"vpc": schema.StringAttribute{
						Optional:            true,
						Computed:            true,
						PlanModifiers:       useState,
						MarkdownDescription: "VPC to attach the machine to. Defaults to the SKU's VPC.",
					},
					"subnet": schema.StringAttribute{
						Optional:            true,
						Computed:            true,
						PlanModifiers:       useState,
						MarkdownDescription: "Subnet of the VPC. Defaults to the SKU's subnet.",
					},
					"security_group": schema.StringAttribute{Optional: true, MarkdownDescription: "Name of an existing security group of the workspace. Day-2: change it, or remove the attribute to detach the group, on a running machine (the console's Security Group edit); the machine is updated in place, not replaced. The platform re-runs the machine's workflow for about a minute."},
					"assign_public_ip": schema.BoolAttribute{
						Optional:            true,
						Computed:            true,
						PlanModifiers:       []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
						MarkdownDescription: "Assign a public IP. Allowed only when the SKU's public IP policy is `Enabled` or `CustomerSelectable`. Day-2: switch it on or off on a running machine (the console's Public IP Assignment edit); the machine is updated in place, not replaced, and the platform re-runs its workflow for about a minute.",
					},
					"hss_storage":    schema.StringAttribute{Optional: true, MarkdownDescription: "Name of a high-speed shared filesystem storage to mount."},
					"object_storage": schema.StringAttribute{Optional: true, MarkdownDescription: "Name of an object storage to attach."},
					"user_data": schema.StringAttribute{
						Optional:  true,
						Sensitive: true,
						MarkdownDescription: "Cloud-init script as plain text; the platform encodes it. Kept as configured: the API's stored form is not compared. " +
							"Marked sensitive because scripts often carry secrets (the platform's own cloud-init for cluster nodes embeds credentials), so it is hidden in plans and CLI output; it is still stored in the Terraform state, so protect the state.",
					},
					"contract_term": schema.Int64Attribute{
						Optional:            true,
						Computed:            true,
						PlanModifiers:       []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
						MarkdownDescription: "Contract term in months, one the SKU offers.",
					},
					"sharing": SharingResourceAttribute(),
					"gpu_name": schema.StringAttribute{
						Computed:            true,
						PlanModifiers:       useState,
						MarkdownDescription: "GPU model, inherited from the SKU.",
					},
					"provisioner_type": schema.StringAttribute{
						Computed:            true,
						PlanModifiers:       useState,
						MarkdownDescription: "How the platform provisions the machine: `bcm` or `non_bcm`.",
					},
					"compute_instance_id": schema.StringAttribute{
						Computed:            true,
						PlanModifiers:       useState,
						MarkdownDescription: "Identifier of the hidden compute instance behind the machine, which holds its workflow log.",
					},
				},
			},
			"desired_action": schema.StringAttribute{
				Optional: true,
				MarkdownDescription: "A power action to run on the machine: `start`, `stop`, `power_cycle` (force power off and back on), " +
					"`power_reset` (reset the hardware power state) or `reboot`. " +
					"`start` and `stop` run only when the machine is not already in that power state; the other actions run when the value changes. An action runs during an apply that changes `desired_action`; Terraform does not undo a power change made outside Terraform. If an action fails, an error leaves the previous value in the state so the next apply retries it; on create it is only a warning, so the machine is not tainted. " +
					"The platform decides which actions a machine accepts: its environment template lists them (the BCM node-provision template offers `start`, `stop`, `Power cycle`, `Power reset`, `Reinstall OS` and `Replace Instance`, and refuses `reboot`). When an action is refused the error lists what is accepted. " +
					"Leave unset or `none` to do nothing.",
				Validators: []validator.String{stringvalidator.OneOf(bareMetalDesiredActions...)},
			},
			"timeouts": schema.SingleNestedAttribute{
				Optional:            true,
				MarkdownDescription: "Time limits as durations such as `45m`. Defaults: create 60m, update 30m, delete 30m, action 20m.",
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
					"private_ip":  schema.StringAttribute{Computed: true},
					"public_ip":   schema.StringAttribute{Computed: true},
					"user_name":   schema.StringAttribute{Computed: true, MarkdownDescription: "User to log in as."},
					"platform":    schema.StringAttribute{Computed: true},
					"gpus":        schema.StringAttribute{Computed: true},
					"vcpus":       schema.StringAttribute{Computed: true},
					"memory_gib":  schema.StringAttribute{Computed: true},
					"network":     schema.StringAttribute{Computed: true},
				},
			},
		},
	}
}

func (r *bareMetalResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.pd = providerData(req.ProviderData, &resp.Diagnostics)
}

func (r *bareMetalResource) machines(project, workspace string) typed.BareMetalInterface {
	if workspace == "" {
		return r.pd.Clientset.V1alpha1().BareMetals(project)
	}
	return r.pd.Clientset.V1alpha1().Workspaces(project).BareMetals(workspace)
}

func (t *bareMetalTimeoutsModel) get(kind string, def time.Duration, diags *diag.Diagnostics) time.Duration {
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

// addBareMetalFailure turns a failed run into a Terraform error with the status and the reason,
// plus the workflow error and log excerpt of the compute instance behind the machine: the
// machine's own reason is generic ("due to an internal error").
func (r *bareMetalResource) addBareMetalFailure(ctx context.Context, diags *diag.Diagnostics, summary, project, workspace, name string, err error) {
	var failed *typed.BareMetalFailedError
	if !errors.As(err, &failed) {
		diags.AddError(summary, err.Error())
		return
	}
	msg := fmt.Sprintf("Status %q after %q: %s", failed.Phase, failed.Action, failed.Reason)
	details := hiddenInstanceFailure(ctx, hiddenInstances(r.pd, project, workspace), name, failed.Reason)
	if details == "" && failed.BareMetal != nil && failed.BareMetal.Status.ReasonDetail != "" {
		// The compute instance behind the machine is gone or unreadable (after a teardown, for
		// example): the machine's own detail still names the cause.
		details = "\n\nWorkflow details:\n  " + truncate(failed.BareMetal.Status.ReasonDetail, 700)
	}
	diags.AddError(summary, msg+details)
}

// bareMetalSettled is the wait predicate for an update: the machine is up or down, not mid-change.
func bareMetalSettled(obj *apiv1.BareMetal) (bool, error) {
	if obj.Status.Failed() {
		return false, &typed.BareMetalFailedError{Name: obj.Metadata.Name, Phase: obj.Status.Phase(), Action: obj.Status.Action, Reason: obj.Status.Reason, BareMetal: obj}
	}
	switch obj.Status.Phase() {
	case apiv1.BareMetalPhaseRunning, apiv1.BareMetalPhaseStopped:
		return true, nil
	}
	return false, nil
}

// bareMetalGone is the wait predicate for a delete. A failed record that has not started its
// teardown still reads "failed" with its deploy action for a while after the DELETE, so a few such
// polls are tolerated; a failed teardown (destroy action, destroyfailed, delete_pending) ends the
// wait at once.
func bareMetalGone(maxStale int) func(*apiv1.BareMetal) (bool, error) {
	stale := 0
	return func(obj *apiv1.BareMetal) (bool, error) {
		if !obj.Status.Failed() {
			stale = 0
			return false, nil
		}
		if p := obj.Status.Phase(); p == apiv1.BareMetalPhaseFailed && !strings.EqualFold(obj.Status.Action, "destroy") {
			if stale++; stale <= maxStale {
				return false, nil
			}
		}
		return typed.BareMetalGone(obj)
	}
}

func (r *bareMetalResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	if r.pd == nil {
		resp.Diagnostics.AddError("Provider not configured", "Provider data is nil; check provider configuration.")
		return
	}
	var plan bareMetalResourceModel
	resp.Diagnostics.Append(decodePlan(ctx, req.Plan, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	timeout := plan.Timeouts.get("create", defaultBareMetalCreateTimeout, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	project, workspace, name := plan.Metadata.Project.ValueString(), plan.Metadata.Workspace.ValueString(), plan.Metadata.Name.ValueString()
	machines := r.machines(project, workspace)
	obj := buildBareMetal(ctx, plan, nil, nil, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	// The Developer Hub fills the datacenter when the SKU has only one.
	if profile := r.readBmProfile(ctx, project, plan.Spec.Type.ValueString()); profile != nil && obj.Spec.Datacenter == "" && len(profile.Spec.Datacenter) == 1 {
		obj.Spec.Datacenter = profile.Spec.Datacenter[0]
	}
	// The name must be free. Checking first is what makes it safe to adopt the instance a failed create
	// leaves behind: anything found under the name afterwards was created by this request.
	if _, gerr := machines.Get(ctx, name, gpupaas.GetOptions{}); gerr == nil {
		resp.Diagnostics.AddError("Bare metal already exists", fmt.Sprintf("A bare metal instance named %q already exists in this project and workspace. "+
			"Import it with terraform import, or use another name.", name))
		return
	}
	stored, err := machines.Create(ctx, obj, gpupaas.CreateOptions{})
	if err != nil {
		if kept := r.keptAfterFailedCreate(ctx, machines, name, err); kept != nil {
			// The platform stores the instance (failed) even when starting its provisioning
			// fails. Keep it in state so that a destroy removes it instead of leaving it behind.
			resp.Diagnostics.Append(resp.State.Set(ctx, bareMetalModelFromServer(ctx, plan, kept, false, &resp.Diagnostics))...)
			resp.Diagnostics.AddError("Create bare metal failed", fmt.Sprintf("%s\n\nThe platform kept the instance in status %q (%s). It is tracked in the Terraform state as tainted: fix the cause and apply again, or run terraform destroy to remove it.",
				err.Error(), kept.Status.Phase(), kept.Status.Reason))
			return
		}
		handleAPIError(&resp.Diagnostics, err, "Create bare metal", nil)
		return
	}
	// The instance exists from here on: save state now so that a failure below leaves a tainted
	// resource that a later apply or destroy cleans up, instead of an orphan.
	resp.Diagnostics.Append(resp.State.Set(ctx, bareMetalModelFromServer(ctx, plan, stored, false, &resp.Diagnostics))...)

	done, err := typed.WaitForBareMetal(ctx, machines, name, typed.BareMetalWaitOptions{Interval: pollInterval, Done: typed.BareMetalRunning, Stable: bareMetalStableRunning})
	if done != nil {
		resp.Diagnostics.Append(resp.State.Set(ctx, bareMetalModelFromServer(ctx, plan, done, false, &resp.Diagnostics))...)
	}
	if err != nil {
		r.addBareMetalFailure(ctx, &resp.Diagnostics, "Bare metal provisioning failed", project, workspace, name, err)
		return
	}
	// An initial desired_action (for example "stop") runs once the machine is up. The machine exists
	// and works, so a failing action must not fail the create: Terraform would taint the machine and
	// the next apply would destroy and rebuild it.
	var actionDiags diag.Diagnostics
	if final, ok := r.applyDesiredAction(ctx, machines, plan, nil, done, &actionDiags); ok && final != nil {
		done = final
	}
	resp.Diagnostics.Append(errorsAsWarnings(actionDiags, "The machine was created and is not tainted. desired_action stays recorded in the state, so Terraform will not retry it: "+
		"set desired_action to \"none\", apply, then set it again to retry.")...)
	resp.Diagnostics.Append(resp.State.Set(ctx, bareMetalModelFromServer(ctx, plan, done, false, &resp.Diagnostics))...)
}

// errorsAsWarnings reports the errors of in as warnings, each followed by note, and keeps its
// other diagnostics as they are.
func errorsAsWarnings(in diag.Diagnostics, note string) diag.Diagnostics {
	var out diag.Diagnostics
	for _, d := range in {
		if d.Severity() == diag.SeverityError {
			out.AddWarning(d.Summary(), d.Detail()+"\n\n"+note)
			continue
		}
		out = append(out, d)
	}
	return out
}

func (r *bareMetalResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	if r.pd == nil {
		return
	}
	var state bareMetalResourceModel
	resp.Diagnostics.Append(decodeState(ctx, req.State, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	srv, err := r.machines(state.Metadata.Project.ValueString(), state.Metadata.Workspace.ValueString()).Get(ctx, state.Metadata.Name.ValueString(), gpupaas.GetOptions{})
	if err != nil {
		handleAPIError(&resp.Diagnostics, err, "Read bare metal", func() { resp.State.RemoveResource(ctx) })
		return
	}
	model := bareMetalModelFromServer(ctx, state, srv, true, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, model)...)
}

func (r *bareMetalResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	if r.pd == nil {
		return
	}
	var plan, state bareMetalResourceModel
	resp.Diagnostics.Append(decodePlan(ctx, req.Plan, &plan)...)
	resp.Diagnostics.Append(decodeState(ctx, req.State, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	timeout := plan.Timeouts.get("update", defaultBareMetalUpdateTimeout, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	project, workspace, name := plan.Metadata.Project.ValueString(), plan.Metadata.Workspace.ValueString(), plan.Metadata.Name.ValueString()
	machines := r.machines(project, workspace)
	// Start from what the API holds now: apply replaces the whole spec, so everything the
	// provider does not manage must be sent back.
	current, err := machines.Get(ctx, name, gpupaas.GetOptions{})
	if err != nil {
		handleAPIError(&resp.Diagnostics, err, "Read bare metal", nil)
		return
	}
	obj := buildBareMetal(ctx, plan, &state.Spec, current, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	if !sameBareMetalRequest(obj, current) {
		stored, err := machines.Update(ctx, obj, gpupaas.UpdateOptions{})
		if err != nil {
			handleAPIError(&resp.Diagnostics, err, "Update bare metal", nil)
			return
		}
		resp.Diagnostics.Append(resp.State.Set(ctx, bareMetalModelFromServer(ctx, plan, stored, false, &resp.Diagnostics))...)
		done, err := typed.WaitForBareMetal(ctx, machines, name, typed.BareMetalWaitOptions{Interval: pollInterval, Done: bareMetalSettled, Stable: bareMetalStableRunning})
		if done != nil {
			current = done
			resp.Diagnostics.Append(resp.State.Set(ctx, bareMetalModelFromServer(ctx, plan, done, false, &resp.Diagnostics))...)
		}
		if err != nil {
			r.addBareMetalFailure(ctx, &resp.Diagnostics, "Bare metal update failed", project, workspace, name, err)
			return
		}
	}
	// Only Terraform-side settings may have changed (timeouts, desired_action): nothing was sent,
	// and re-applying the instance could start a redeploy.
	errorsBefore := resp.Diagnostics.ErrorsCount()
	if final, ok := r.applyDesiredAction(ctx, machines, plan, &state, current, &resp.Diagnostics); ok && final != nil {
		current = final
	}
	recorded := plan
	if resp.Diagnostics.ErrorsCount() > errorsBefore {
		// The action failed: keep the previous desired_action in the state, so that the next plan
		// shows the change again and the action is retried instead of being forgotten.
		recorded.DesiredAction = state.DesiredAction
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, bareMetalModelFromServer(ctx, recorded, current, false, &resp.Diagnostics))...)
}

// applyDesiredAction runs plan.DesiredAction when it is needed. It returns the instance after the
// action and whether an action ran. prior is nil on create.
func (r *bareMetalResource) applyDesiredAction(ctx context.Context, machines typed.BareMetalInterface, plan bareMetalResourceModel,
	prior *bareMetalResourceModel, current *apiv1.BareMetal, diags *diag.Diagnostics) (*apiv1.BareMetal, bool) {
	action := strings.ToLower(normalizedActionValue(plan.DesiredAction))
	if action == "" || action == "none" {
		return nil, false
	}
	priorAction := ""
	if prior != nil {
		priorAction = strings.ToLower(normalizedActionValue(prior.DesiredAction))
	}
	if !bareMetalActionNeeded(action, priorAction, current.Status.PowerState()) {
		return nil, false
	}
	timeout := plan.Timeouts.get("action", defaultBareMetalActionTimeout, diags)
	actx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	name := plan.Metadata.Name.ValueString()
	apiAction := bareMetalAPIAction(action)
	if _, err := machines.Action(actx, name, apiAction, map[string]any{}); err != nil {
		handleAPIError(diags, err, fmt.Sprintf("Run action %q", apiAction), nil)
		return nil, false
	}
	done, err := typed.WaitForBareMetal(actx, machines, name, typed.BareMetalWaitOptions{Interval: pollInterval, Done: typed.BareMetalActionDone(apiAction), Stable: bareMetalStableRunning})
	if err != nil {
		r.addBareMetalFailure(ctx, diags, fmt.Sprintf("Action %q failed", apiAction), plan.Metadata.Project.ValueString(), plan.Metadata.Workspace.ValueString(), name, err)
		return done, true
	}
	return done, true
}

func (r *bareMetalResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	if r.pd == nil {
		return
	}
	var state bareMetalResourceModel
	resp.Diagnostics.Append(decodeState(ctx, req.State, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	timeout := state.Timeouts.get("delete", defaultBareMetalDeleteTimeout, &resp.Diagnostics)
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
			// The platform parked an earlier delete for the operator (delete_pending) and refuses
			// further ones from end users.
			resp.Diagnostics.AddError("Bare metal delete refused", err.Error()+"\n\nThe platform parked this instance's delete for an administrator (delete_pending); end users cannot delete it again. "+
				"It stays in the Terraform state, and its machine stays allocated, until an administrator removes it.")
			return
		}
		handleAPIError(&resp.Diagnostics, err, "Delete bare metal", nil)
		return
	}
	_, err := typed.WaitForBareMetal(ctx, machines, name, typed.BareMetalWaitOptions{Interval: pollInterval, Done: bareMetalGone(bareMetalStaleFailed), GoneIsDone: true})
	if err == nil {
		err = waitForHiddenInstanceGone(ctx, r.pd, project, workspace, name)
	}
	if err != nil {
		r.addBareMetalFailure(ctx, &resp.Diagnostics, "Bare metal teardown failed", project, workspace, name, err)
		var failed *typed.BareMetalFailedError
		if errors.As(err, &failed) {
			if failed.Phase == apiv1.BareMetalPhaseDeletePending {
				resp.Diagnostics.AddWarning("Teardown needs an administrator", "The platform parked the delete for the operator (delete_pending); end users cannot delete the instance again. The instance is kept in state until it is gone.")
			} else {
				resp.Diagnostics.AddWarning("Teardown can be retried", "Run terraform destroy again to retry the teardown; the instance is kept in state until it is gone.")
			}
		}
	}
}

func (r *bareMetalResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
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
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), bareMetalID(project, workspace, name))...)
}

// keptAfterFailedCreate returns the instance the platform stored although the create call failed,
// or nil. A 409 means the name belonged to an existing instance, which is never adopted.
func (r *bareMetalResource) keptAfterFailedCreate(ctx context.Context, machines typed.BareMetalInterface, name string, createErr error) *apiv1.BareMetal {
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
