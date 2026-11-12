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
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	"github.com/gpupaas-ai/terraform-provider-gpupaas/internal/client"
)

var (
	_ resource.Resource                = (*storageResource)(nil)
	_ resource.ResourceWithImportState = (*storageResource)(nil)
	_ resource.ResourceWithConfigure   = (*storageResource)(nil)
)

// Default time limits. A volume takes a few minutes to provision; a delete about as long.
const (
	defaultStorageCreateTimeout = 30 * time.Minute
	defaultStorageUpdateTimeout = 20 * time.Minute
	defaultStorageDeleteTimeout = 30 * time.Minute
)

// storageStable is how many consecutive polls must show the volume settled before a create or update counts as
// finished: right after a write the platform can still show the previous state for a moment.
const storageStable = 2

// storageUnseenPolls is how many settled (or failed) polls after an update may still be the previous run's state before
// they count as the update's result: right after the upsert the platform can keep showing the earlier run for a moment,
// and a run that finishes between two polls is never seen in progress.
const storageUnseenPolls = 3

// storageStaleFailed is how many polls after a delete may still show a failed record that has not started its
// teardown before that counts as a failed delete.
const storageStaleFailed = 8

// storageKind describes one of the two storage resources. They share the platform's Storage kind and differ in
// the family of profiles (SKUs) they accept.
type storageKind struct {
	suffix      string // the type name after the provider prefix
	title       string // for descriptions and messages
	article     string // "A" or "An" before the title at the start of a sentence
	serviceType string // the platform's family name, usable as a list filter
	other       *storageKind
	typeHelp    string // what spec.type names
	attach      string // how an instance uses the volume, as one or more sentences ending in a space
}

var (
	highSpeedStorage = storageKind{
		suffix:      "high_speed_storage",
		title:       "high-speed storage",
		article:     "A",
		serviceType: apiv1.StorageServiceTypeHighSpeed,
		typeHelp:    "Name of the high-speed storage profile (SKU) a platform administrator published, such as a shared filesystem or a block volume.",
		attach:      "Name it in the `hss_storage` argument of a bare-metal instance or the `shared_storage` argument of a virtual machine to attach it. ",
	}
	objectStorage = storageKind{
		suffix:      "object_storage",
		title:       "object storage",
		article:     "An",
		serviceType: apiv1.StorageServiceTypeObject,
		typeHelp:    "Name of the object storage profile (SKU) a platform administrator published.",
		attach:      "Applications reach the bucket with the S3 details in `status.output`. ",
	}
)

func init() {
	highSpeedStorage.other, objectStorage.other = &objectStorage, &highSpeedStorage
}

// NewHighSpeedStorageResource constructs the gpupaas_high_speed_storage resource.
func NewHighSpeedStorageResource() resource.Resource {
	return &storageResource{kind: highSpeedStorage}
}

// NewObjectStorageResource constructs the gpupaas_object_storage resource.
func NewObjectStorageResource() resource.Resource { return &storageResource{kind: objectStorage} }

type storageResource struct {
	pd   *client.ProviderData
	kind storageKind
}

type storageSpecModel struct {
	Type                      types.String  `tfsdk:"type"`
	Size                      types.String  `tfsdk:"size"`
	Datacenter                types.String  `tfsdk:"datacenter"`
	StorageType               types.String  `tfsdk:"storage_type"`
	AccessPolicy              types.String  `tfsdk:"access_policy"`
	ContractTerm              types.String  `tfsdk:"contract_term"`
	EnableEncryptionAtRest    types.Bool    `tfsdk:"enable_encryption_at_rest"`
	EnableEncryptionInTransit types.Bool    `tfsdk:"enable_encryption_in_transit"`
	Sharing                   *SharingModel `tfsdk:"sharing"`
}

type storageStatusModel struct {
	Status types.String `tfsdk:"status"`
	Reason types.String `tfsdk:"reason"`
	Action types.String `tfsdk:"action"`
	Output types.Map    `tfsdk:"output"`
}

type storageTimeoutsModel struct {
	Create types.String `tfsdk:"create"`
	Update types.String `tfsdk:"update"`
	Delete types.String `tfsdk:"delete"`
}

type storageResourceModel struct {
	ID       types.String          `tfsdk:"id"`
	Metadata scopedMetadataModel   `tfsdk:"metadata"`
	Spec     storageSpecModel      `tfsdk:"spec"`
	Status   storageStatusModel    `tfsdk:"status"`
	Timeouts *storageTimeoutsModel `tfsdk:"timeouts"`
}

func (r *storageResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_" + r.kind.suffix
}

func (r *storageResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replaceStr := func() []planmodifier.String {
		return []planmodifier.String{stringplanmodifier.UseStateForUnknown(), stringplanmodifier.RequiresReplace()}
	}
	keepStr := func() []planmodifier.String { return []planmodifier.String{stringplanmodifier.UseStateForUnknown()} }
	replaceBool := func() []planmodifier.Bool {
		return []planmodifier.Bool{boolplanmodifier.UseStateForUnknown(), boolplanmodifier.RequiresReplace()}
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: r.kind.article + " " + r.kind.title + " volume (`dev.envmgmt.io/v1` `Storage`) provisioned from a storage profile (SKU) that a platform administrator published, " +
			"at project or workspace scope. " + r.kind.attach +
			"Provisioning, a size change and deletion run asynchronously; the resource waits for them. " +
			"The profile, datacenter, storage type, contract term and encryption settings are fixed at creation: changing one replaces the volume and its data. " +
			"`size`, `access_policy` and `sharing` are applied to the existing volume.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "`<project>/<workspace>/<name>`, or `<project>/<name>` for a project-scoped volume.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"metadata": scopedMetadataAttribute("volume"),
			"spec": schema.SingleNestedAttribute{
				Required: true,
				Attributes: map[string]schema.Attribute{
					"type": schema.StringAttribute{
						Required:            true,
						MarkdownDescription: r.kind.typeHelp + " Changing it replaces the volume.",
						PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
					},
					"size": schema.StringAttribute{
						Optional: true, Computed: true,
						MarkdownDescription: "Requested capacity, a number in the unit the profile defines (GB unless the profile says GiB). Leave it unset for a profile with a fixed volume. " +
							"A change is applied to the existing volume; if the platform does not support resizing this profile, the apply fails and the state shows the size it kept.",
						PlanModifiers: keepStr(),
					},
					"datacenter": schema.StringAttribute{
						Optional: true, Computed: true,
						MarkdownDescription: "Datacenter to provision in. Unset lets the platform choose. Changing it replaces the volume.",
						PlanModifiers:       replaceStr(),
					},
					"storage_type": schema.StringAttribute{
						Optional: true, Computed: true,
						MarkdownDescription: "Classification of the volume: `block`, `file` or `object`. Unset lets the platform fill it from the profile. Changing it replaces the volume.",
						PlanModifiers:       replaceStr(),
					},
					"access_policy": schema.StringAttribute{
						Optional: true, Computed: true,
						MarkdownDescription: "Access policy of the volume, as the profile defines it. A change is applied to the existing volume.",
						PlanModifiers:       keepStr(),
					},
					"contract_term": schema.StringAttribute{
						Optional: true, Computed: true,
						MarkdownDescription: "Contract term of the volume. Changing it replaces the volume.",
						PlanModifiers:       replaceStr(),
					},
					"enable_encryption_at_rest": schema.BoolAttribute{
						Optional: true, Computed: true,
						MarkdownDescription: "Encrypt the data at rest. Unset leaves it to the profile. Changing it replaces the volume.",
						PlanModifiers:       replaceBool(),
					},
					"enable_encryption_in_transit": schema.BoolAttribute{
						Optional: true, Computed: true,
						MarkdownDescription: "Encrypt the data in transit. Unset leaves it to the profile. Changing it replaces the volume.",
						PlanModifiers:       replaceBool(),
					},
					"sharing": SharingResourceAttribute(),
				},
			},
			"timeouts": schema.SingleNestedAttribute{
				Optional:            true,
				MarkdownDescription: "Time limits as durations such as `30m`. Defaults: create 30m, update 20m, delete 30m.",
				Attributes: map[string]schema.Attribute{
					"create": schema.StringAttribute{Optional: true},
					"update": schema.StringAttribute{Optional: true},
					"delete": schema.StringAttribute{Optional: true},
				},
			},
			"status": schema.SingleNestedAttribute{
				Computed: true,
				Attributes: map[string]schema.Attribute{
					"status": schema.StringAttribute{Computed: true, MarkdownDescription: "Platform status: `success` once the volume is provisioned, `failed`, `in_progress`, `deleting`, `delete_pending`, ..."},
					"reason": schema.StringAttribute{Computed: true},
					"action": schema.StringAttribute{Computed: true, MarkdownDescription: "Last or in-flight operation: `deploy`, `update`, `destroy`, ..."},
					"output": schema.MapAttribute{
						Computed: true, Sensitive: true, ElementType: types.StringType,
						MarkdownDescription: "What the platform reports about the provisioned volume, as strings: a filesystem's servers, mount username and credentials, the volume size in bytes; " +
							"for an object store its bucket and S3 keys. The platform returns credentials in plain text, so the whole map is sensitive. Keys depend on the profile.",
					},
				},
			},
		},
	}
}

func (r *storageResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.pd = providerData(req.ProviderData, &resp.Diagnostics)
}

func (r *storageResource) volumes(project, workspace string) typed.StorageInterface {
	return storages(r.pd, project, workspace)
}

func storages(pd *client.ProviderData, project, workspace string) typed.StorageInterface {
	if workspace == "" {
		return pd.Clientset.V1alpha1().Storages(project)
	}
	return pd.Clientset.V1alpha1().Workspaces(project).Storages(workspace)
}

func (t *storageTimeoutsModel) get(kind string, def time.Duration, diags *diag.Diagnostics) time.Duration {
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

// addStorageFailure turns a failed run into a Terraform error with the status, the action and the reason.
func addStorageFailure(diags *diag.Diagnostics, summary string, err error) {
	var failed *typed.StorageFailedError
	if !errors.As(err, &failed) {
		diags.AddError(summary, err.Error())
		return
	}
	diags.AddError(summary, fmt.Sprintf("Status %q after %q: %s", failed.Phase, failed.Action, failed.Reason))
}

func (r *storageResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	if r.pd == nil {
		resp.Diagnostics.AddError("Provider not configured", "Provider data is nil; check provider configuration.")
		return
	}
	var plan storageResourceModel
	resp.Diagnostics.Append(decodePlan(ctx, req.Plan, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	timeout := plan.Timeouts.get("create", defaultStorageCreateTimeout, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	project, workspace, name := plan.Metadata.Project.ValueString(), stringOr(plan.Metadata.Workspace, ""), plan.Metadata.Name.ValueString()
	vols := r.volumes(project, workspace)
	obj := buildStorage(ctx, plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	// The name must be free. Checking here, and stopping on any failure of the check, is what makes it safe to adopt
	// the volume a failed create leaves behind: anything found under the name afterwards was created by this request.
	if _, gerr := vols.Get(ctx, name, gpupaas.GetOptions{}); gerr == nil {
		resp.Diagnostics.AddError("Volume already exists", fmt.Sprintf("A volume named %q already exists in this project and workspace. Import it with terraform import, or use another name.", name))
		return
	} else if !gpupaas.IsNotFound(gerr) {
		handleAPIError(&resp.Diagnostics, gerr, "Check the volume name", nil)
		return
	}
	stored, err := vols.Create(ctx, obj, gpupaas.CreateOptions{})
	if err != nil {
		if gpupaas.IsConflict(err) {
			resp.Diagnostics.AddError("Volume already exists", fmt.Sprintf("A volume named %q already exists in this project and workspace. Import it with terraform import, or use another name.", name))
			return
		}
		if kept := keptStorageAfterFailedCreate(ctx, vols, name); kept != nil {
			// The platform can store the volume (failed) even when starting its provisioning fails. Keep it in state
			// so that a destroy removes it instead of leaving it behind.
			resp.Diagnostics.Append(resp.State.Set(ctx, storageModelFromServer(ctx, plan, kept, &resp.Diagnostics))...)
			resp.Diagnostics.AddError("Create volume failed", fmt.Sprintf("%s\n\nThe platform kept the volume in status %q (%s). It is tracked in the Terraform state as tainted: fix the cause and apply again, or run terraform destroy to remove it.",
				err.Error(), kept.Status.Phase(), kept.Status.Reason))
			return
		}
		handleAPIError(&resp.Diagnostics, err, "Create volume", nil)
		if !isClientError(err) {
			// The request may have reached the platform before the failure: the volume can exist without being tracked.
			resp.Diagnostics.AddWarning("The volume may exist", fmt.Sprintf("The create failed in a way that does not show whether the platform stored the volume. "+
				"If a volume named %q now exists in this project and workspace, import it with terraform import (or remove it) before applying again.", name))
		}
		return
	}
	// The volume exists from here on: save state now so that a failure below leaves a tainted resource that a
	// later apply or destroy cleans up, instead of an orphan.
	resp.Diagnostics.Append(resp.State.Set(ctx, storageModelFromServer(ctx, plan, stored, &resp.Diagnostics))...)

	if inOwn, inOther := storageFamily(ctx, r.pd, r.kind, project, workspace, name); inOther && !inOwn {
		resp.Diagnostics.AddError("Wrong storage resource", wrongFamilyMessage(r.kind, plan.Spec.Type.ValueString(), name)+
			"\n\nThe volume was created and is tracked in the state as tainted; run terraform destroy to remove it, then declare it with the right resource.")
		return
	}

	done, err := typed.WaitForStorage(ctx, vols, name, typed.StorageWaitOptions{Interval: pollInterval, Done: typed.StorageReady, Stable: storageStable})
	if done != nil {
		resp.Diagnostics.Append(resp.State.Set(ctx, storageModelFromServer(ctx, plan, done, &resp.Diagnostics))...)
	}
	if err != nil {
		addStorageFailure(&resp.Diagnostics, "Volume provisioning failed", err)
	}
}

func (r *storageResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	if r.pd == nil {
		return
	}
	var state storageResourceModel
	resp.Diagnostics.Append(decodeState(ctx, req.State, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	srv, err := r.volumes(state.Metadata.Project.ValueString(), stringOr(state.Metadata.Workspace, "")).Get(ctx, state.Metadata.Name.ValueString(), gpupaas.GetOptions{})
	if err != nil {
		handleAPIError(&resp.Diagnostics, err, "Read volume", func() { resp.State.RemoveResource(ctx) })
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, storageModelFromServer(ctx, state, srv, &resp.Diagnostics))...)
}

func (r *storageResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	if r.pd == nil {
		resp.Diagnostics.AddError("Provider not configured", "Provider data is nil; check provider configuration.")
		return
	}
	var plan storageResourceModel
	resp.Diagnostics.Append(decodePlan(ctx, req.Plan, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	timeout := plan.Timeouts.get("update", defaultStorageUpdateTimeout, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	project, workspace, name := plan.Metadata.Project.ValueString(), stringOr(plan.Metadata.Workspace, ""), plan.Metadata.Name.ValueString()
	vols := r.volumes(project, workspace)
	current, err := vols.Get(ctx, name, gpupaas.GetOptions{})
	if err != nil {
		handleAPIError(&resp.Diagnostics, err, "Read volume", nil)
		return
	}
	obj := buildStorage(ctx, plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	// A removed sharing block means private. Sending nothing may leave the platform's sharing in place, so say "None"
	// explicitly. This is an update-only default: Create sends no sharing for a volume that has none.
	if obj.Spec.Sharing == nil {
		obj.Spec.Sharing = &apiv1.DevSharingSpec{ShareMode: "None"}
	}
	// Only Terraform-side settings (timeouts) may have changed: nothing is sent, and re-applying the volume could
	// start another run.
	if !storageNeedsApply(obj, current) {
		resp.Diagnostics.Append(resp.State.Set(ctx, storageModelFromServer(ctx, plan, current, &resp.Diagnostics))...)
		return
	}
	stored, err := vols.Update(ctx, obj, gpupaas.UpdateOptions{})
	if err != nil {
		handleAPIError(&resp.Diagnostics, err, "Update volume", nil)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, storageModelFromServer(ctx, plan, stored, &resp.Diagnostics))...)
	done, err := typed.WaitForStorage(ctx, vols, name, typed.StorageWaitOptions{Interval: pollInterval, Done: storageUpdateDone(current.Status), Stable: storageStable})
	if done != nil {
		resp.Diagnostics.Append(resp.State.Set(ctx, storageModelFromServer(ctx, plan, done, &resp.Diagnostics))...)
	}
	if err != nil {
		addStorageFailure(&resp.Diagnostics, "Volume update failed", err)
		return
	}
	// The platform accepts a spec and may leave a field as it was (a size change on a profile that cannot resize).
	// That must not pass as success: the state holds what the volume has, and the next plan shows the difference.
	if kept := storageNotApplied(obj, done); len(kept) > 0 {
		resp.Diagnostics.AddError("The platform did not apply the update", fmt.Sprintf("The update finished with status %q, but %s. "+
			"The state shows what the volume has. Check with your administrator whether profile %q supports changing it; "+
			"otherwise replace the volume (terraform apply -replace=...) to get the new value.", done.Status.Phase(), strings.Join(kept, "; "), done.Spec.Type))
	}
}

func (r *storageResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	if r.pd == nil {
		// Returning quietly would drop the resource from the state while the volume is still there.
		resp.Diagnostics.AddError("Provider not configured", "Provider data is nil, so the volume cannot be deleted; check the provider configuration. The volume stays in the state.")
		return
	}
	var state storageResourceModel
	resp.Diagnostics.Append(decodeState(ctx, req.State, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	timeout := state.Timeouts.get("delete", defaultStorageDeleteTimeout, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	project, workspace, name := state.Metadata.Project.ValueString(), stringOr(state.Metadata.Workspace, ""), state.Metadata.Name.ValueString()
	vols := r.volumes(project, workspace)
	if err := vols.Delete(ctx, name, gpupaas.DeleteOptions{IgnoreNotFound: true}); err != nil {
		var apiErr *gpupaas.APIError
		if errors.As(err, &apiErr) && apiErr.StatusCode == 412 {
			// The platform parked an earlier delete for the operator (delete_pending) and refuses further ones
			// from end users.
			resp.Diagnostics.AddError("Volume delete refused", err.Error()+"\n\nThe platform parked this volume's delete for an administrator (delete_pending); end users cannot delete it again. "+
				"It stays in the Terraform state, and its capacity stays allocated, until an administrator removes it.")
			return
		}
		handleAPIError(&resp.Diagnostics, err, "Delete volume", nil)
		return
	}
	_, err := typed.WaitForStorage(ctx, vols, name, typed.StorageWaitOptions{Interval: pollInterval, Done: storageGone(storageStaleFailed), GoneIsDone: true})
	if err != nil {
		addStorageFailure(&resp.Diagnostics, "Volume teardown failed", err)
		var failed *typed.StorageFailedError
		if errors.As(err, &failed) {
			if failed.Phase == apiv1.StoragePhaseDeletePending {
				resp.Diagnostics.AddWarning("Teardown needs an administrator", "The platform parked the delete for the operator (delete_pending); end users cannot delete the volume again. The volume is kept in state until it is gone.")
			} else {
				resp.Diagnostics.AddWarning("Teardown can be retried", "Run terraform destroy again to retry the teardown; the volume is kept in state until it is gone.")
			}
		}
	}
}

func (r *storageResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	project, workspace, name, err := ParseFlexibleScopedImportID(req.ID)
	if err != nil {
		resp.Diagnostics.AddError("Invalid import ID", "Use <project>/<workspace>/<name> or <project>/<name>: "+err.Error())
		return
	}
	if r.pd != nil {
		if inOwn, inOther := storageFamily(ctx, r.pd, r.kind, project, workspace, name); inOther && !inOwn {
			resp.Diagnostics.AddError("Wrong storage resource", fmt.Sprintf("%q is a volume of the other storage family: import it as %s.", name, "gpupaas_"+r.kind.other.suffix))
			return
		}
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("metadata").AtName("project"), project)...)
	if workspace != "" {
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("metadata").AtName("workspace"), workspace)...)
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("metadata").AtName("name"), name)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), scopedID(project, workspace, name))...)
}

// keptStorageAfterFailedCreate returns the volume the platform stored although the create call failed, or nil.
// A name that was taken before (409) is handled by the caller and never adopted.
func keptStorageAfterFailedCreate(ctx context.Context, vols typed.StorageInterface, name string) *apiv1.Storage {
	kept, err := vols.Get(ctx, name, gpupaas.GetOptions{})
	if err != nil {
		return nil
	}
	return kept
}

// storageFamily reports whether a volume of this name and scope is in the list of its own family and of the other
// one. The platform stamps the family from the profile when the volume is created, and the list takes it as a
// filter. A volume that is in neither (a custom profile) or whose lookup failed is not reported as a mismatch, and
// neither is a controller that ignores the filter: then the name is in both lists.
func storageFamily(ctx context.Context, pd *client.ProviderData, kind storageKind, project, workspace, name string) (inOwn, inOther bool) {
	has := func(serviceType string) bool {
		items, err := listAll(func(opts gpupaas.ListOptions) ([]apiv1.Storage, string, error) {
			opts.ServiceType = serviceType
			out, err := storages(pd, project, workspace).List(ctx, opts)
			if err != nil {
				return nil, "", err
			}
			return out.Items, out.Metadata.Continue, nil
		})
		if err != nil {
			// The check is advisory: a lookup that fails (no permission to list, an outage) must not block the
			// volume, but it is recorded.
			tflog.Warn(ctx, "could not check the storage family of a volume", map[string]any{"name": name, "service_type": serviceType, "error": err.Error()})
			return false
		}
		for _, it := range items {
			if it.Metadata.Name == name && it.Metadata.Workspace == workspace {
				return true
			}
		}
		return false
	}
	inOwn = has(kind.serviceType)
	inOther = has(kind.other.serviceType)
	return inOwn, inOther
}

func wrongFamilyMessage(kind storageKind, profile, name string) string {
	return fmt.Sprintf("Profile %q created the volume %q as %s of the other family: declare it with gpupaas_%s instead of gpupaas_%s.",
		profile, name, kind.other.title, kind.other.suffix, kind.suffix)
}

// storageGone is the wait predicate for a delete. A failed record that has not started its teardown still reads
// "failed" with its deploy action for a while after the DELETE, so a few such polls are tolerated; a failed teardown
// (destroy action, destroyfailed, delete_pending) ends the wait at once.
func storageGone(maxStale int) func(*apiv1.Storage) (bool, error) {
	stale := 0
	return func(obj *apiv1.Storage) (bool, error) {
		if !obj.Status.Failed() {
			stale = 0
			return false, nil
		}
		if obj.Status.Phase() == apiv1.StoragePhaseFailed && !strings.EqualFold(obj.Status.Action, "destroy") {
			if stale++; stale <= maxStale {
				return false, nil
			}
		}
		return typed.StorageGone(obj)
	}
}

// buildStorage turns the plan into the request. An unset optional field is sent empty, which leaves it to the profile.
func buildStorage(ctx context.Context, plan storageResourceModel, diags *diag.Diagnostics) *apiv1.Storage {
	return &apiv1.Storage{
		TypeMeta: apiv1.TypeMeta{APIVersion: apiv1.APIVersion, Kind: apiv1.KindStorage},
		Metadata: apiv1.ObjectMeta{Name: plan.Metadata.Name.ValueString(), Project: plan.Metadata.Project.ValueString(), Workspace: stringOr(plan.Metadata.Workspace, "")},
		Spec: apiv1.StorageSpec{
			Type:                      plan.Spec.Type.ValueString(),
			Size:                      stringOr(plan.Spec.Size, ""),
			Datacenter:                stringOr(plan.Spec.Datacenter, ""),
			StorageType:               stringOr(plan.Spec.StorageType, ""),
			AccessPolicy:              stringOr(plan.Spec.AccessPolicy, ""),
			ContractTerm:              stringOr(plan.Spec.ContractTerm, ""),
			EnableEncryptionAtRest:    wireBool(plan.Spec.EnableEncryptionAtRest),
			EnableEncryptionInTransit: wireBool(plan.Spec.EnableEncryptionInTransit),
			Sharing:                   sharingToSDK(ctx, plan.Spec.Sharing, diags),
		},
	}
}

// wireBool is how the platform carries a flag: "true", "false", or empty when it is unset.
func wireBool(b types.Bool) string {
	if b.IsNull() || b.IsUnknown() {
		return ""
	}
	if b.ValueBool() {
		return "true"
	}
	return "false"
}

// boolFromWire reads a flag; anything that is not true or false is unset.
func boolFromWire(s string) types.Bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true":
		return types.BoolValue(true)
	case "false":
		return types.BoolValue(false)
	}
	return types.BoolNull()
}

// storageNeedsApply reports whether the request differs from the volume in a field the provider applies in place.
func storageNeedsApply(want, have *apiv1.Storage) bool {
	return len(storageNotApplied(want, have)) > 0
}

// storageNotApplied names the in-place fields whose requested value the volume does not have. An unset size or
// access policy leaves the field to the platform. Sharing is always set by Update (a removed block is sent as
// "None"), so the nil check below only matters to callers that build a request without that default.
func storageNotApplied(want, have *apiv1.Storage) []string {
	var out []string
	if want.Spec.Size != "" && want.Spec.Size != have.Spec.Size {
		out = append(out, fmt.Sprintf("size is %q, not the requested %q", have.Spec.Size, want.Spec.Size))
	}
	if want.Spec.AccessPolicy != "" && want.Spec.AccessPolicy != have.Spec.AccessPolicy {
		out = append(out, fmt.Sprintf("access_policy is %q, not the requested %q", have.Spec.AccessPolicy, want.Spec.AccessPolicy))
	}
	if want.Spec.Sharing != nil && !sameSharing(want.Spec.Sharing, have.Spec.Sharing) {
		out = append(out, "sharing differs from the requested one")
	}
	return out
}

// sameSharing compares two sharing specs; an unset spec and "None" are the same.
func sameSharing(a, b *apiv1.DevSharingSpec) bool {
	if isDefaultSharing(a) && isDefaultSharing(b) {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	if a.ShareMode != b.ShareMode || !sameSet(a.Workspaces, b.Workspaces) || len(a.Projects) != len(b.Projects) {
		return false
	}
	// entries are matched by project name, in any order
	byName := map[string][]string{}
	for _, p := range b.Projects {
		byName[p.Name] = p.Workspaces
	}
	for _, p := range a.Projects {
		ws, ok := byName[p.Name]
		if !ok || !sameSet(p.Workspaces, ws) {
			return false
		}
	}
	return true
}

// sameSet compares two lists as sets: the platform may return the same entries in another order.
func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[string]int{}
	for _, x := range a {
		seen[x]++
	}
	for _, x := range b {
		if seen[x]--; seen[x] < 0 {
			return false
		}
	}
	return true
}

// isClientError reports a 4xx answer: the platform rejected the request, so nothing was stored.
func isClientError(err error) bool {
	var apiErr *gpupaas.APIError
	return errors.As(err, &apiErr) && apiErr.StatusCode >= 400 && apiErr.StatusCode < 500
}

// storageUpdateDone is the wait predicate for an update. prev is the status before the update was sent. Right after the
// upsert the platform can still show the previous run: a settled state counts only after the update was seen in
// progress, or after storageUnseenPolls settled polls (a run that finished between two polls); a failed state that is
// the previous run's counts as the update's only after the same number of polls.
func storageUpdateDone(prev apiv1.StorageStatus) func(*apiv1.Storage) (bool, error) {
	sawActive, settled, failed := false, 0, 0
	return func(obj *apiv1.Storage) (bool, error) {
		if obj.Status.InProgress() {
			sawActive = true
			return false, nil
		}
		if obj.Status.Failed() {
			samePast := !sawActive && prev.Failed() && obj.Status.Phase() == prev.Phase() && obj.Status.Reason == prev.Reason
			if samePast {
				if failed++; failed < storageUnseenPolls {
					return false, nil
				}
			}
			return typed.StorageReady(obj)
		}
		if obj.Status.Ready() {
			if !sawActive {
				if settled++; settled < storageUnseenPolls {
					return false, nil
				}
			}
			return true, nil
		}
		return false, nil
	}
}

// keepIfAbsent is the platform's value, or the configured one when the platform does not echo the field back (it
// leaves some fields out of a volume it provisioned from a profile that fixes them).
func keepIfAbsent(base types.String, server string) types.String {
	if server == "" && set(base) {
		return base
	}
	return nullableString(server)
}

func keepBoolIfAbsent(base types.Bool, server string) types.Bool {
	if b := boolFromWire(server); !b.IsNull() {
		return b
	}
	if !base.IsNull() && !base.IsUnknown() {
		return base
	}
	return types.BoolNull()
}

// storageModelFromServer builds the state from what the platform returned. base holds the configured values (plan or
// prior state): a field the platform does not echo keeps the configured value, so it does not look like drift.
func storageModelFromServer(ctx context.Context, base storageResourceModel, srv *apiv1.Storage, diags *diag.Diagnostics) storageResourceModel {
	out := base
	project, workspace := base.Metadata.Project.ValueString(), stringOr(base.Metadata.Workspace, "")
	name := firstNonEmpty(srv.Metadata.Name, base.Metadata.Name.ValueString())
	out.ID = types.StringValue(scopedID(project, workspace, name))
	out.Metadata.Name = types.StringValue(name)
	out.Spec = storageSpecModel{
		Type:                      types.StringValue(firstNonEmpty(srv.Spec.Type, base.Spec.Type.ValueString())),
		Size:                      keepIfAbsent(base.Spec.Size, srv.Spec.Size),
		Datacenter:                keepIfAbsent(base.Spec.Datacenter, srv.Spec.Datacenter),
		StorageType:               keepIfAbsent(base.Spec.StorageType, srv.Spec.StorageType),
		AccessPolicy:              keepIfAbsent(base.Spec.AccessPolicy, srv.Spec.AccessPolicy),
		ContractTerm:              keepIfAbsent(base.Spec.ContractTerm, srv.Spec.ContractTerm),
		EnableEncryptionAtRest:    keepBoolIfAbsent(base.Spec.EnableEncryptionAtRest, srv.Spec.EnableEncryptionAtRest),
		EnableEncryptionInTransit: keepBoolIfAbsent(base.Spec.EnableEncryptionInTransit, srv.Spec.EnableEncryptionInTransit),
	}
	if isDefaultSharing(srv.Spec.Sharing) && !sharingConfigured(base.Spec.Sharing) {
		out.Spec.Sharing = nil
	} else {
		out.Spec.Sharing = sharingFromSDK(ctx, srv.Spec.Sharing, base.Spec.Sharing, diags)
	}
	out.Status = storageStatusFromServer(srv)
	return out
}

func storageStatusFromServer(srv *apiv1.Storage) storageStatusModel {
	output := types.MapNull(types.StringType)
	if len(srv.Status.Output) > 0 {
		output = mapFromStringMap(srv.Status.Output)
	}
	return storageStatusModel{
		Status: nullableString(srv.Status.Status),
		Reason: nullableString(srv.Status.Reason),
		Action: nullableString(srv.Status.Action),
		Output: output,
	}
}
