package resources

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	gpupaas "github.com/gpupaas-ai/gpupaas-go"
	apiv1 "github.com/gpupaas-ai/gpupaas-go/apis/v1alpha1"
	typed "github.com/gpupaas-ai/gpupaas-go/clientset/typed/v1alpha1"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/objectplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/gpupaas-ai/terraform-provider-gpupaas/internal/client"
)

var (
	_ resource.Resource                = (*mksClusterResource)(nil)
	_ resource.ResourceWithConfigure   = (*mksClusterResource)(nil)
	_ resource.ResourceWithImportState = (*mksClusterResource)(nil)
	_ resource.ResourceWithModifyPlan  = (*mksClusterResource)(nil)
)

// Default time limits. A one-node cluster on bare metal comes up in 15-25 minutes; upgrades
// roll every node, so updates get as long as creation.
const (
	defaultMKSCreateTimeout = 60 * time.Minute
	defaultMKSUpdateTimeout = 60 * time.Minute
	defaultMKSDeleteTimeout = 30 * time.Minute
)

// mksStableRunning is how many consecutive polls must show the cluster RUNNING with the
// requested spec before an update counts as finished. The platform can report RUNNING, flip to
// UPDATING and come back while it finishes a write.
const mksStableRunning = 3

// NewMKSClusterResource constructs the gpupaas_kubernetes_cluster resource.
func NewMKSClusterResource() resource.Resource { return &mksClusterResource{} }

type mksClusterResource struct{ pd *client.ProviderData }

func (r *mksClusterResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_kubernetes_cluster"
}

func (r *mksClusterResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	md := MetadataResourceAttribute(true, true)
	for _, k := range []string{"name", "project", "workspace"} {
		sa := md.Attributes[k].(schema.StringAttribute)
		sa.PlanModifiers = append(sa.PlanModifiers, stringplanmodifier.RequiresReplace())
		md.Attributes[k] = sa
	}
	useState := []planmodifier.String{stringplanmodifier.UseStateForUnknown()}
	groupAttrs := func(isControlPlane bool) map[string]schema.Attribute {
		attrs := map[string]schema.Attribute{
			"datacenter": schema.StringAttribute{Optional: true, MarkdownDescription: "Datacenter of the nodes. Nodes are always placed in the cluster's `location`, so this must be left out or equal to it: a per-pool datacenter is not supported."},
			"os_image":   schema.StringAttribute{Optional: true, MarkdownDescription: "Operating system image, one of the profile's images. Defaults to the profile's default image. Changing it on an existing pool re-images the pool's nodes (a rolling replacement)."},
			"sku_name":   schema.StringAttribute{Optional: true, MarkdownDescription: "Compute SKU for the nodes (a bare-metal or VM profile name), one of the profile's SKUs. Defaults to the profile's default SKU. The GPU model and the GPU count per node come from the SKU (for a VM SKU from the preset whose name is the `sku_name`); a pool cannot choose them."},
			"sku_type":   schema.StringAttribute{Optional: true, MarkdownDescription: "`BMAAS` (bare metal) or `VMAAS` (virtual machines). The platform stamps it from the SKU."},
			"ssh_key": schema.StringAttribute{
				Optional: true,
				MarkdownDescription: "Name of an existing SSH key resource of the workspace to install on the nodes; an inline public key is not accepted. " +
					"The platform installs one key per cluster: when pools name different keys, the control plane's key is used.",
			},
			"network": schema.SingleNestedAttribute{
				Optional: true,
				MarkdownDescription: "VPC, subnet and security group of the nodes. Controllers that place nodes per pool require `vpc` and `subnet` for every new pool. " +
					"The network is fixed once the pool exists: to use another one, add a new pool and remove the old one.",
				Attributes: map[string]schema.Attribute{
					"vpc":            schema.StringAttribute{Required: true, MarkdownDescription: "Name of the VPC."},
					"subnet":         schema.StringAttribute{Required: true, MarkdownDescription: "Subnet in that VPC."},
					"security_group": schema.StringAttribute{Optional: true, MarkdownDescription: "Name of a security group for the nodes. The platform does not check that it exists."},
				},
			},
			"boot_disk_size_gb": schema.Int64Attribute{
				Optional: true,
				MarkdownDescription: "Boot disk of each node in GB, for pools of virtual machines only (bare-metal pools refuse it). It must lie inside the range of the compute SKU. " +
					"Fixed once the pool exists.",
			},
			"assign_public_ip": schema.BoolAttribute{
				Optional:            true,
				MarkdownDescription: "Give the nodes a public IP. The platform assigns public IPs to all worker nodes or to none, so every worker pool must carry the same value.",
			},
			"user_password": schema.StringAttribute{
				Optional:  true,
				Sensitive: true,
				MarkdownDescription: "Login password of the nodes (alternative to `ssh_key`). The platform never returns it, so Terraform cannot detect a change made elsewhere. " +
					"After an `import`, a configured password counts as a change and is sent on the next apply, which redeploys the cluster.",
			},
		}
		if isControlPlane {
			attrs["node_count"] = schema.Int64Attribute{Required: true, MarkdownDescription: "Number of control-plane nodes: `1`, or `3` for high availability. It cannot change once the cluster exists."}
			attrs["name"] = schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString("control-plane"), MarkdownDescription: "Name of the control-plane group."}
			attrs["scaling_mode"] = schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString("manual"), MarkdownDescription: "Scaling mode; the control plane is always `manual`."}
			attrs["compute_type"] = schema.StringAttribute{Computed: true, PlanModifiers: useState, MarkdownDescription: "`CPU`, `GPU` or `CUSTOM` (a bare-metal SKU without a catalog GPU), derived by the platform from the SKU."}
		} else {
			attrs["node_count"] = schema.Int64Attribute{
				Required: true,
				MarkdownDescription: "Number of nodes. With `scaling_mode = \"automatic\"` it is the size the pool starts with: the autoscaler then moves the size between " +
					"`min_nodes` and `max_nodes`, and Terraform does not set it back.",
			}
			attrs["name"] = schema.StringAttribute{Required: true, MarkdownDescription: "Name of the worker pool. It identifies the pool: renaming one removes it and adds a new one."}
			attrs["scaling_mode"] = schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString("manual"), MarkdownDescription: "`manual` (a fixed `node_count`, the default) or `automatic` (between `min_nodes` and `max_nodes`)."}
			attrs["min_nodes"] = schema.Int64Attribute{Optional: true, MarkdownDescription: "Smallest size of an automatic pool. Only with `scaling_mode = \"automatic\"`."}
			attrs["max_nodes"] = schema.Int64Attribute{Optional: true, MarkdownDescription: "Largest size of an automatic pool. Only with `scaling_mode = \"automatic\"`."}
			attrs["labels"] = schema.MapAttribute{Optional: true, ElementType: types.StringType, MarkdownDescription: "Kubernetes labels of the pool's nodes. The map replaces the pool's labels."}
			attrs["annotations"] = schema.MapAttribute{Optional: true, ElementType: types.StringType, MarkdownDescription: "Kubernetes annotations of the pool's nodes. The map replaces the pool's annotations."}
			attrs["taints"] = schema.ListNestedAttribute{
				Optional:            true,
				MarkdownDescription: "Kubernetes taints of the pool's nodes. The list replaces the pool's taints.",
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"key":    schema.StringAttribute{Required: true},
					"value":  schema.StringAttribute{Optional: true},
					"effect": schema.StringAttribute{Required: true, MarkdownDescription: "`NoSchedule`, `PreferNoSchedule` or `NoExecute`."},
				}},
			}
		}
		return attrs
	}

	resp.Schema = schema.Schema{
		MarkdownDescription: "A first-class managed Kubernetes cluster (MKS), created from a cluster profile (Kubernetes SKU). " +
			"Creating the resource provisions the cluster and waits until it is running, which takes many minutes. " +
			"The cluster is declarative: change `kubernetes_version` to upgrade it, and `worker_pools` (or their `node_count`) to resize it. " +
			"Use the `gpupaas_kubernetes_sku` data source to see which versions, SKUs, images and datacenters a profile allows; " +
			"Terraform checks your values against the profile at plan time.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "`<project>/<workspace>/<name>`, or `<project>/<name>` for a project-scoped cluster.",
				PlanModifiers:       useState,
			},
			"metadata": md,
			"spec": schema.SingleNestedAttribute{
				Required: true,
				Attributes: map[string]schema.Attribute{
					"profile_name": schema.StringAttribute{
						Required:            true,
						MarkdownDescription: "The cluster profile (Kubernetes SKU) to create the cluster from. Changing it replaces the cluster.",
						PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
					},
					"kubernetes_version": schema.StringAttribute{
						Required:            true,
						MarkdownDescription: "Kubernetes version, for example `1.36` (the platform resolves the latest patch) or `v1.36.4`. Changing it upgrades the cluster.",
					},
					"location": schema.StringAttribute{
						Optional:            true,
						Computed:            true,
						PlanModifiers:       useState,
						MarkdownDescription: "Datacenter the cluster is placed in. Defaults to the profile's datacenter.",
					},
					"blueprint": schema.SingleNestedAttribute{
						Optional:      true,
						Computed:      true,
						PlanModifiers: []planmodifier.Object{objectplanmodifier.UseStateForUnknown()},
						Attributes: map[string]schema.Attribute{
							"name":    schema.StringAttribute{Optional: true, Computed: true, PlanModifiers: useState},
							"version": schema.StringAttribute{Optional: true, Computed: true, PlanModifiers: useState},
						},
						MarkdownDescription: "Blueprint applied to the cluster. Defaults to the profile's blueprint.",
					},
					"networking": schema.SingleNestedAttribute{
						Optional:      true,
						Computed:      true,
						PlanModifiers: []planmodifier.Object{objectplanmodifier.UseStateForUnknown()},
						Attributes: map[string]schema.Attribute{
							"vpc":            schema.StringAttribute{Optional: true, Computed: true, PlanModifiers: useState},
							"subnet":         schema.StringAttribute{Optional: true, Computed: true, PlanModifiers: useState},
							"pod_subnet":     schema.StringAttribute{Optional: true, Computed: true, PlanModifiers: useState, MarkdownDescription: "Pod CIDR, for example `10.244.0.0/16`."},
							"service_subnet": schema.StringAttribute{Optional: true, Computed: true, PlanModifiers: useState, MarkdownDescription: "Service CIDR, for example `10.96.0.0/12`."},
							"cni_name":       schema.StringAttribute{Optional: true, Computed: true, PlanModifiers: useState, MarkdownDescription: "CNI plug-in, for example `Calico`."},
							"cni_version":    schema.StringAttribute{Optional: true, Computed: true, PlanModifiers: useState},
						},
						MarkdownDescription: "Network settings. Defaults come from the profile. Controllers of release v3.1.41 and later place nodes per pool and clear the cluster-level `vpc` and `subnet`: set `network` on each pool instead.",
					},
					"control_plane": schema.SingleNestedAttribute{
						Required:            true,
						Attributes:          groupAttrs(true),
						MarkdownDescription: "The control-plane nodes: 1 node, or 3 for high availability. The size is fixed once the cluster exists (the platform cannot convert a cluster to high availability), so create it with 3 nodes if you need that.",
					},
					"worker_pools": schema.ListNestedAttribute{
						Optional:            true,
						MarkdownDescription: "Worker node pools. Add, remove or resize pools by editing this list; pools are identified by `name`.",
						NestedObject:        schema.NestedAttributeObject{Attributes: groupAttrs(false)},
					},
					"tags": schema.MapAttribute{Optional: true, ElementType: types.StringType, MarkdownDescription: "Tags for the cluster."},
					"contract_term": schema.StringAttribute{
						Optional:            true,
						Computed:            true,
						PlanModifiers:       useState,
						MarkdownDescription: "Billing contract term in months (`0` is on demand). Must be one the profile allows.",
					},
				},
			},
			"force_destroy": schema.BoolAttribute{
				Optional: true,
				MarkdownDescription: "Remove the cluster record even if the platform cannot tear down its infrastructure (`DELETE ...?force=true`). " +
					"Leave unset normally: a cluster that failed before anything was provisioned (status `error`, no nodes) is removed this way automatically. " +
					"Set it only after checking that no machines are left behind.",
			},
			"timeouts": schema.SingleNestedAttribute{
				Optional:            true,
				MarkdownDescription: "Time limits as durations such as `45m`. Defaults: create 60m, update 60m, delete 30m.",
				Attributes: map[string]schema.Attribute{
					"create": schema.StringAttribute{Optional: true},
					"update": schema.StringAttribute{Optional: true},
					"delete": schema.StringAttribute{Optional: true},
				},
			},
			"status": schema.SingleNestedAttribute{
				Computed: true,
				Attributes: map[string]schema.Attribute{
					"phase":              schema.StringAttribute{Computed: true, MarkdownDescription: "Normalized status: `provisioning`, `running`, `updating`, `upgrading`, `degraded`, `error`, `destroying`, ..."},
					"status":             schema.StringAttribute{Computed: true, MarkdownDescription: "Raw status as reported by the API, for example `MKS_CLUSTER_STATUS_RUNNING`."},
					"reason":             schema.StringAttribute{Computed: true},
					"action":             schema.StringAttribute{Computed: true},
					"cluster_id":         schema.StringAttribute{Computed: true, MarkdownDescription: "Identifier of the cluster in the Kubernetes management plane."},
					"kubernetes_version": schema.StringAttribute{Computed: true, MarkdownDescription: "Exact Kubernetes version the cluster runs, for example `v1.36.4`."},
					"node_count":         schema.Int64Attribute{Computed: true, MarkdownDescription: "Number of nodes registered with the cluster."},
					"available_kubernetes_versions": schema.ListAttribute{
						Computed: true, ElementType: types.StringType,
						MarkdownDescription: "Versions the cluster can be upgraded to: one minor version at a time, never a downgrade.",
					},
					"nodes": schema.ListNestedAttribute{
						Computed: true,
						NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
							"hostname":           schema.StringAttribute{Computed: true},
							"node_pool":          schema.StringAttribute{Computed: true, MarkdownDescription: "`master` for control-plane nodes, otherwise the worker pool name."},
							"kubernetes_version": schema.StringAttribute{Computed: true},
							"private_ip":         schema.StringAttribute{Computed: true, MarkdownDescription: "Private IP of the node. Null on controllers older than release v3.1.41."},
							"roles":              schema.ListAttribute{Computed: true, ElementType: types.StringType, MarkdownDescription: "`Master` and/or `Worker`."},
							"operating_system":   schema.StringAttribute{Computed: true},
							"arch":               schema.StringAttribute{Computed: true},
							"status":             schema.StringAttribute{Computed: true, MarkdownDescription: "The platform's own status word for the node, for example `READY`."},
						}},
					},
				},
			},
		},
	}
}

func (r *mksClusterResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.pd = providerData(req.ProviderData, &resp.Diagnostics)
}

func (r *mksClusterResource) clusters(project, workspace string) typed.MKSClusterInterface {
	if workspace == "" {
		return r.pd.Clientset.V1alpha1().MKSClusters(project)
	}
	return r.pd.Clientset.V1alpha1().Workspaces(project).MKSClusters(workspace)
}

// nodesOf reads the registered nodes of a cluster; it returns nil when they cannot be read,
// because a missing node list must not fail a refresh.
func (r *mksClusterResource) nodesOf(ctx context.Context, project, cluster string) *apiv1.MKSNodeList {
	list, err := r.pd.Clientset.V1alpha1().MKSNodes(project).ListForCluster(ctx, cluster)
	if err != nil {
		return nil
	}
	return list
}

func (t *mksTimeoutsModel) get(kind string, def time.Duration, diags *diag.Diagnostics) time.Duration {
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

// addMKSFailure turns a failed run into a Terraform error with the status and the reason. The
// cluster's own reason is generic ("a node failed to provision"), so for a failed provision or
// update the real cause is taken from the failed node instances.
func (r *mksClusterResource) addMKSFailure(ctx context.Context, diags *diag.Diagnostics, summary, project, workspace, cluster string, err error) {
	var failed *typed.MKSClusterFailedError
	if !errors.As(err, &failed) {
		diags.AddError(summary, err.Error())
		return
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Status %q after %q: %s", failed.Phase, failed.Action, failed.Reason)
	// A failed update can leave the cluster RUNNING, so the causes are listed for any failure.
	b.WriteString(r.nodeFailureDetails(ctx, project, workspace, cluster))
	diags.AddError(summary, b.String())
}

// updateDone completes an update when the cluster is RUNNING with the requested spec and the
// workflow behind the update has finished. The live platform keeps a cluster RUNNING while an update
// runs and after it fails (only the reason changes), so status and spec alone cannot tell success
// from failure; the compute instance of the cluster itself can. priorFailed says that instance was
// already failed before the update: it then counts as a new failure only after the update's workflow
// has been seen running, because until then the failed state is the previous run's.
func updateDone(ctx context.Context, ci typed.ComputeInstanceInterface, name string, want apiv1.MKSClusterSpec, priorFailed bool, priorTrigger string) func(*apiv1.MKSCluster) (bool, error) {
	matches := typed.MKSClusterMatches(want)
	sawActive := !priorFailed
	unreadable := 0
	return func(obj *apiv1.MKSCluster) (bool, error) {
		ok, err := matches(obj)
		if err != nil || !ok {
			return ok, err
		}
		inst, gerr := ci.Get(ctx, name, gpupaas.GetOptions{})
		switch {
		case gpupaas.IsNotFound(gerr):
			return true, nil // no backing instance: the cluster status is all there is
		case gerr != nil:
			// A failed read says nothing about the update workflow: keep waiting, and give up after a
			// run of failures instead of calling the update done.
			if unreadable++; unreadable >= maxHiddenReadErrors {
				return false, fmt.Errorf("cannot read the compute instance of cluster %q to confirm the update: %w", name, gerr)
			}
			return false, nil
		case inst.Status.InProgress():
			unreadable = 0
			sawActive = true
			return false, nil
		case inst.Status.Failed():
			unreadable = 0
			// A new run has a new trigger id: a failure of it is this update's even when the run
			// was never seen in progress (it ran between two polls).
			if t := inst.Status.TriggerID(); t != "" && t != priorTrigger {
				sawActive = true
			}
			if !sawActive {
				return false, nil
			}
			return false, &typed.MKSClusterFailedError{Name: name, Phase: obj.Status.Phase(), Action: "update", Reason: obj.Status.Reason, Cluster: obj}
		}
		return true, nil
	}
}

// hiddenInstanceRun reports whether the compute instance behind a first-class object is failed and
// the id of its latest run. An unreadable instance gives (false, "").
func hiddenInstanceRun(ctx context.Context, ci typed.ComputeInstanceInterface, name string) (failed bool, trigger string) {
	inst, err := ci.Get(ctx, name, gpupaas.GetOptions{})
	if err != nil {
		return false, ""
	}
	return inst.Status.Failed(), inst.Status.TriggerID()
}

// maxNodeFailures bounds how many failed nodes an error message describes.
const maxNodeFailures = 3

// clusterNames lists the cluster names of a project or workspace, always including cluster, so that
// a node instance can be attributed to the cluster that owns it (see clusterOwning).
func (r *mksClusterResource) clusterNames(ctx context.Context, project, workspace, cluster string) []string {
	names := []string{cluster}
	clusters, err := listPages(func(opts gpupaas.ListOptions) ([]apiv1.MKSCluster, string, error) {
		list, err := r.clusters(project, workspace).List(ctx, opts)
		if err != nil {
			return nil, "", err
		}
		return list.Items, list.Metadata.Continue, nil
	})
	if err != nil {
		return names
	}
	for _, c := range clusters {
		names = append(names, c.Metadata.Name)
	}
	return names
}

// nodeFailureDetails describes why a cluster or its nodes failed: the compute instances that back
// first-class clusters and nodes carry the workflow error and the provisioning log. The cluster's
// own instance explains failures before any node exists (for example "no healthy agents found").
// Best effort: it returns "" when they cannot be read.
func (r *mksClusterResource) nodeFailureDetails(ctx context.Context, project, workspace, cluster string) string {
	var ci typed.ComputeInstanceInterface
	if workspace == "" {
		ci = r.pd.Clientset.V1alpha1().ComputeInstances(project)
	} else {
		ci = r.pd.Clientset.V1alpha1().Workspaces(project).ComputeInstances(workspace)
	}
	items, err := listAllInstances(ctx, ci)
	if err != nil {
		return ""
	}
	owners := r.clusterNames(ctx, project, workspace, cluster)
	var b strings.Builder
	shown := 0
	for i := range items {
		inst := &items[i]
		isCluster := inst.Metadata.Name == cluster
		if clusterOwning(inst.Metadata.Name, owners) != cluster || inst.Status.Phase() != apiv1.ComputeStatusFailed || shown >= maxNodeFailures {
			continue
		}
		shown++
		kind := "Node"
		if isCluster {
			kind = "Cluster workflow"
		}
		fmt.Fprintf(&b, "\n\n%s %s failed:", kind, inst.Metadata.Name)
		errs := workflowErrors(inst.Status.WorkflowStatus)
		if len(errs) == 0 {
			errs = []string{inst.Status.Reason}
		}
		for _, e := range errs {
			fmt.Fprintf(&b, "\n  %s", truncate(e, 700))
		}
		if lg, lerr := ci.FailureLog(ctx, inst.Metadata.Name, 12); lerr == nil && len(lg.Lines) > 0 {
			b.WriteString("\n  Log excerpt:")
			for _, line := range lg.Lines {
				fmt.Fprintf(&b, "\n    %s", truncate(line, 300))
			}
		}
	}
	return b.String()
}

func truncate(s string, n int) string {
	// cut by characters, not bytes: a multi-byte character split in two is invalid UTF-8, which the protocol refuses
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "..."
	}
	return s
}

// errNodeListUnreadable reports that the node list could not be read, so the node check was skipped.
var errNodeListUnreadable = errors.New("the node list could not be read")

// waitForNodes polls until the cluster has registered at least want nodes. The cluster status
// can return to RUNNING before a new node has joined, so node count is the completion signal
// for resizes. It returns the last node list read.
func (r *mksClusterResource) waitForNodes(ctx context.Context, project, cluster string, want int) (*apiv1.MKSNodeList, error) {
	return waitForNodeList(ctx, func() *apiv1.MKSNodeList { return r.nodesOf(ctx, project, cluster) }, cluster, want)
}

// waitForNodeList is waitForNodes with the node reader passed in. After a run of unreadable lists
// it returns the last list read with errNodeListUnreadable: the cluster status has already settled,
// so the apply is not held until the timeout for a check that cannot succeed, but the caller must
// say that the check was skipped.
func waitForNodeList(ctx context.Context, read func() *apiv1.MKSNodeList, cluster string, want int) (*apiv1.MKSNodeList, error) {
	var last *apiv1.MKSNodeList
	unreadable := 0
	for {
		if list := read(); list != nil {
			unreadable = 0
			last = list
			if len(list.Items) >= want {
				return last, nil
			}
		} else if unreadable++; unreadable >= 5 {
			return last, fmt.Errorf("%w after %d attempts: cannot confirm that %d nodes of cluster %q joined", errNodeListUnreadable, unreadable, want, cluster)
		}
		select {
		case <-ctx.Done():
			n := 0
			if last != nil {
				n = len(last.Items)
			}
			return last, fmt.Errorf("waiting for %d nodes of cluster %q: %w (%d registered)", want, cluster, ctx.Err(), n)
		case <-time.After(pollInterval):
		}
	}
}

func (r *mksClusterResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	if r.pd == nil {
		resp.Diagnostics.AddError("Provider not configured", "Provider data is nil; check provider configuration.")
		return
	}
	var plan mksResourceModel
	resp.Diagnostics.Append(decodePlan(ctx, req.Plan, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	timeout := plan.Timeouts.get("create", defaultMKSCreateTimeout, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	project, workspace, name := plan.Metadata.Project.ValueString(), plan.Metadata.Workspace.ValueString(), plan.Metadata.Name.ValueString()
	clusters := r.clusters(project, workspace)
	obj := buildMKSCluster(plan, nil)
	// The API needs every field; take the unset ones from the profile, as the Developer Hub does.
	fillProfileDefaults(&obj.Spec, r.readClusterProfile(ctx, project, plan.Spec.ProfileName.ValueString()), r.sshKeyExists(ctx, project, workspace), nil)
	stored, err := clusters.Create(ctx, obj, gpupaas.CreateOptions{})
	if err != nil {
		if kept := r.keptAfterFailedCreate(ctx, clusters, name, err); kept != nil {
			// The platform stores the cluster (in ERROR) even when starting its provisioning
			// fails. Keep it in state so that a destroy removes it instead of leaving it behind.
			resp.Diagnostics.Append(resp.State.Set(ctx, mksClusterModelFromServer(plan, kept, nil, false))...)
			resp.Diagnostics.AddError("Create mks cluster failed", fmt.Sprintf("%s\n\nThe platform kept the cluster in status %q (%s). It is tracked in the Terraform state as tainted: fix the cause and apply again, or run terraform destroy to remove it.",
				err.Error(), kept.Status.Phase(), kept.Status.Reason))
			return
		}
		handleAPIError(&resp.Diagnostics, err, "Create mks cluster", nil)
		return
	}
	// The cluster exists from here on: save state now so that a failure below leaves a tainted
	// resource that a later apply or destroy cleans up, instead of an orphan.
	resp.Diagnostics.Append(resp.State.Set(ctx, mksClusterModelFromServer(plan, stored, nil, false))...)

	done, err := typed.WaitForMKSCluster(ctx, clusters, name, typed.MKSWaitOptions{Interval: pollInterval, Done: typed.MKSClusterRunning, Stable: 2})
	if done != nil {
		resp.Diagnostics.Append(resp.State.Set(ctx, mksClusterModelFromServer(plan, done, nil, false))...)
	}
	if err != nil {
		r.addMKSFailure(ctx, &resp.Diagnostics, "Cluster provisioning failed", project, workspace, name, err)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, mksClusterModelFromServer(plan, done, r.nodesOf(ctx, project, name), false))...)
}

func (r *mksClusterResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	if r.pd == nil {
		return
	}
	var state mksResourceModel
	resp.Diagnostics.Append(decodeState(ctx, req.State, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	project, workspace, name := state.Metadata.Project.ValueString(), state.Metadata.Workspace.ValueString(), state.Metadata.Name.ValueString()
	srv, err := r.clusters(project, workspace).Get(ctx, name, gpupaas.GetOptions{})
	if err != nil {
		handleAPIError(&resp.Diagnostics, err, "Read mks cluster", func() { resp.State.RemoveResource(ctx) })
		return
	}
	model := mksClusterModelFromServer(state, srv, r.nodesOf(ctx, project, name), true)
	resp.Diagnostics.Append(resp.State.Set(ctx, model)...)
}

func (r *mksClusterResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	if r.pd == nil {
		return
	}
	var plan, state mksResourceModel
	resp.Diagnostics.Append(decodePlan(ctx, req.Plan, &plan)...)
	resp.Diagnostics.Append(decodeState(ctx, req.State, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	timeout := plan.Timeouts.get("update", defaultMKSUpdateTimeout, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	project, workspace, name := plan.Metadata.Project.ValueString(), plan.Metadata.Workspace.ValueString(), plan.Metadata.Name.ValueString()
	clusters := r.clusters(project, workspace)
	// Start from what the API holds now: apply replaces the whole spec, so everything the
	// provider does not manage must be sent back, and the revision lets the API reject a
	// concurrent change instead of overwriting it.
	current, err := clusters.Get(ctx, name, gpupaas.GetOptions{})
	if err != nil {
		handleAPIError(&resp.Diagnostics, err, "Read mks cluster", nil)
		return
	}
	priorFailed, priorTrigger := hiddenInstanceRun(ctx, hiddenInstances(r.pd, project, workspace), name)
	obj := buildMKSClusterAfter(plan, current, &state)
	// A pool added in this update needs the same defaults a new cluster gets; groups the cluster
	// already has keep what it holds.
	fillProfileDefaults(&obj.Spec, r.readClusterProfile(ctx, project, plan.Spec.ProfileName.ValueString()), r.sshKeyExists(ctx, project, workspace), &current.Spec)
	// Tags have a route of their own that edits them in place, without a redeploy. A controller without it
	// (404, 405, 501) gets the full update below.
	if patch, ok := tagsOnlyPatch(obj, current); ok {
		stored, err := clusters.UpdateMetadata(ctx, name, patch)
		switch {
		case err == nil:
			// The answer to the patch is not guaranteed to carry the status: read the cluster again.
			if fresh, gerr := clusters.Get(ctx, name, gpupaas.GetOptions{}); gerr == nil {
				stored = fresh
			} else if stored.Status.Status == "" {
				stored.Status = current.Status
			}
			// The live controller starts a node reconcile after a metadata change: the cluster reads UPDATING
			// ("Compute allocation in progress") for a minute or so, then RUNNING again. Wait for that, so the
			// state does not keep the transient phase and a change made right after does not meet a busy
			// cluster. A cluster that was not running before is left as it is.
			var waitErr error
			if current.Status.Running() {
				done, werr := typed.WaitForMKSCluster(ctx, clusters, name, typed.MKSWaitOptions{Interval: pollInterval, Done: typed.MKSClusterRunning, Stable: mksStableRunning})
				if done != nil {
					stored = done
				}
				waitErr = werr
			}
			resp.Diagnostics.Append(resp.State.Set(ctx, mksClusterModelFromServer(plan, stored, r.nodesOf(ctx, project, name), true))...)
			if waitErr != nil {
				r.addMKSFailure(ctx, &resp.Diagnostics, "Cluster update failed", project, workspace, name, waitErr)
			}
			return
		case !metadataRouteMissing(err):
			handleAPIError(&resp.Diagnostics, err, "Update mks cluster metadata", nil)
			return
		}
	}
	if sameRequest(obj, current) {
		// Only Terraform-side settings changed (timeouts, force_destroy): nothing to send, and
		// re-applying the cluster would start a redeploy.
		resp.Diagnostics.Append(resp.State.Set(ctx, mksClusterModelFromServer(plan, current, r.nodesOf(ctx, project, name), true))...)
		return
	}
	stored, err := clusters.Update(ctx, obj, gpupaas.UpdateOptions{})
	if err != nil {
		handleAPIError(&resp.Diagnostics, err, "Update mks cluster", nil)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, mksClusterModelFromServer(plan, stored, nil, false))...)

	done, err := typed.WaitForMKSCluster(ctx, clusters, name, typed.MKSWaitOptions{Interval: pollInterval, Done: updateDone(ctx, hiddenInstances(r.pd, project, workspace), name, obj.Spec, priorFailed, priorTrigger), Stable: mksStableRunning})
	if done != nil {
		resp.Diagnostics.Append(resp.State.Set(ctx, mksClusterModelFromServer(plan, done, nil, false))...)
	}
	if err != nil {
		r.addMKSFailure(ctx, &resp.Diagnostics, "Cluster update failed", project, workspace, name, err)
		return
	}
	nodes, err := r.waitForNodes(ctx, project, name, expectedNodes(obj.Spec))
	switch {
	case errors.Is(err, errNodeListUnreadable):
		resp.Diagnostics.AddWarning("Node check skipped", err.Error()+". The cluster itself reports RUNNING and the update completed.")
	case err != nil:
		resp.Diagnostics.AddError("Cluster nodes did not join", err.Error())
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, mksClusterModelFromServer(plan, done, nodes, false))...)
}

func (r *mksClusterResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	if r.pd == nil {
		return
	}
	var state mksResourceModel
	resp.Diagnostics.Append(decodeState(ctx, req.State, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	timeout := state.Timeouts.get("delete", defaultMKSDeleteTimeout, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	name := state.Metadata.Name.ValueString()
	clusters := r.clusters(state.Metadata.Project.ValueString(), state.Metadata.Workspace.ValueString())
	force := state.ForceDestroy.ValueBool()
	err := clusters.Delete(ctx, name, gpupaas.DeleteOptions{IgnoreNotFound: true, Force: force})
	if err != nil && !force && strings.Contains(err.Error(), "force=true") {
		// The platform refused to start a teardown and offers to remove just the record. That is
		// safe only for a cluster that failed before provisioning anything.
		project := state.Metadata.Project.ValueString()
		cur, gerr := clusters.Get(ctx, name, gpupaas.GetOptions{})
		nodes := r.nodesOf(ctx, project, name)
		if gerr == nil && cur.Status.Phase() == apiv1.MKSPhaseError && nodes != nil && len(nodes.Items) == 0 {
			err = clusters.Delete(ctx, name, gpupaas.DeleteOptions{IgnoreNotFound: true, Force: true})
		} else {
			resp.Diagnostics.AddError("Delete mks cluster failed", err.Error()+"\n\nIf no machines are left behind, set force_destroy = true on the resource, apply, and destroy again.")
			return
		}
	}
	if err != nil {
		handleAPIError(&resp.Diagnostics, err, "Delete mks cluster", nil)
		return
	}
	_, err = typed.WaitForMKSCluster(ctx, clusters, name, typed.MKSWaitOptions{Interval: pollInterval, Done: typed.MKSClusterGone, GoneIsDone: true})
	if err == nil {
		err = r.waitForBackingInstance(ctx, state.Metadata.Project.ValueString(), state.Metadata.Workspace.ValueString(), name)
	}
	if err != nil {
		r.addMKSFailure(ctx, &resp.Diagnostics, "Cluster teardown failed", state.Metadata.Project.ValueString(), state.Metadata.Workspace.ValueString(), name, err)
		var failed *typed.MKSClusterFailedError
		if errors.As(err, &failed) {
			resp.Diagnostics.AddWarning("Teardown can be retried", "Run terraform destroy again to retry the teardown; the cluster is kept in state until it is gone.")
		}
	}
}

func (r *mksClusterResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
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
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), mksClusterID(project, workspace, name))...)
}

// keptAfterFailedCreate returns the cluster the platform stored although the create call failed,
// or nil. A 409 means the name belonged to an existing cluster, which is never adopted.
func (r *mksClusterResource) keptAfterFailedCreate(ctx context.Context, clusters typed.MKSClusterInterface, name string, createErr error) *apiv1.MKSCluster {
	var apiErr *gpupaas.APIError
	if errors.As(createErr, &apiErr) && apiErr.StatusCode == 409 {
		return nil
	}
	kept, err := clusters.Get(ctx, name, gpupaas.GetOptions{})
	if err != nil {
		return nil
	}
	return kept
}

// sshKeyExists returns a lookup for SSH keys in the cluster's workspace (or project).
func (r *mksClusterResource) sshKeyExists(ctx context.Context, project, workspace string) func(string) bool {
	return func(name string) bool {
		var err error
		if workspace == "" {
			_, err = r.pd.Clientset.V1alpha1().SshKeys(project).Get(ctx, name, gpupaas.GetOptions{})
		} else {
			_, err = r.pd.Clientset.V1alpha1().Workspaces(project).SshKeys(workspace).Get(ctx, name, gpupaas.GetOptions{})
		}
		return keyMayExist(err)
	}
}

// keyMayExist turns the result of an SSH key lookup into "keep the key": only a definite not found
// means it is missing. On any other error the key is kept and the API stays the authority, instead
// of silently creating nodes without a key.
func keyMayExist(err error) bool {
	return err == nil || !gpupaas.IsNotFound(err)
}

// waitForBackingInstance waits until the compute instance behind a deleted cluster is gone. The
// cluster record disappears first; the instance keeps "destroying" for a while and the platform
// refuses to delete the workspace until it is done, so a dependent workspace would fail to delete.
func (r *mksClusterResource) waitForBackingInstance(ctx context.Context, project, workspace, cluster string) error {
	return waitForHiddenInstanceGone(ctx, r.pd, project, workspace, cluster)
}

// tagsOnlyPatch returns the metadata patch that makes the cluster's tags the wanted ones, when the tags are all that
// differ. The tags are sent whole and replace the stored ones; want already holds the tags the platform has from
// elsewhere (see buildMKSClusterAfter). Labels and annotations are not part of it: the platform adds labels of its
// own, and a change to them goes through the full update.
func tagsOnlyPatch(want, current *apiv1.MKSCluster) (apiv1.MKSMetadataPatch, bool) {
	same := want.DeepCopyObject().(*apiv1.MKSCluster)
	same.Spec.Tags = current.Spec.Tags
	if !sameRequest(same, current) || sameStringMap(want.Spec.Tags, current.Spec.Tags) {
		return apiv1.MKSMetadataPatch{}, false
	}
	tags := want.Spec.Tags
	if tags == nil {
		tags = map[string]string{} // an empty map clears the tags; nil would leave them alone
	}
	return apiv1.MKSMetadataPatch{Tags: tags, ReplaceTags: true}, true
}

// metadataRouteMissing reports an answer that means the controller has no metadata route.
func metadataRouteMissing(err error) bool {
	var apiErr *gpupaas.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	switch apiErr.StatusCode {
	case http.StatusNotFound, http.StatusMethodNotAllowed, http.StatusNotImplemented:
		return true
	}
	return false
}
