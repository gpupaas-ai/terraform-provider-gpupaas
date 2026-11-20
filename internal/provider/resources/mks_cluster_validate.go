package resources

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	gpupaas "github.com/gpupaas-ai/gpupaas-go"
	apiv1 "github.com/gpupaas-ai/gpupaas-go/apis/v1alpha1"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// readClusterProfile looks the profile up in the cluster's project and then in the system
// catalog. It returns nil when it cannot be read: validation is then skipped and the API
// remains the authority.
func (r *mksClusterResource) readClusterProfile(ctx context.Context, project, name string) *apiv1.MKSClusterProfile {
	if name == "" {
		return nil
	}
	projects := []string{project}
	if project != systemCatalogProject {
		projects = append(projects, systemCatalogProject)
	}
	for _, p := range projects {
		if p == "" {
			continue
		}
		if prof, err := r.pd.Clientset.V1alpha1().MKSClusterProfiles(p).Get(ctx, name, gpupaas.GetOptions{}); err == nil {
			return prof
		}
	}
	return nil
}

// ModifyPlan checks the planned cluster against its profile, so a wrong SKU, image or version
// fails at plan time with the allowed values instead of minutes into an apply.
func (r *mksClusterResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() || r.pd == nil {
		return
	}
	var plan mksResourceModel
	resp.Diagnostics.Append(decodePlanKeepingUnknowns(ctx, req.Plan, &plan)...)
	var prior *mksResourceModel
	if !req.State.Raw.IsNull() {
		var state mksResourceModel
		resp.Diagnostics.Append(decodeState(ctx, req.State, &state)...)
		prior = &state
	}
	if resp.Diagnostics.HasError() {
		return
	}
	// A problem the cluster already has is only a warning: it must not block an unrelated update.
	var now, was diag.Diagnostics
	validateMKSShape(plan, &now)
	if prior == nil {
		validateMKSPoolNames(plan, nil, &now)
	} else {
		validateMKSShape(*prior, &was)
	}
	demoteUnchanged(&resp.Diagnostics, now, was)
	if resp.Diagnostics.HasError() {
		return
	}
	if prior != nil {
		validateMKSFixedAfterCreate(plan, *prior, &resp.Diagnostics)
		warnMKSReimage(plan, *prior, &resp.Diagnostics)
		validateMKSPoolNames(plan, prior, &resp.Diagnostics)
		validateMKSUpgrade(plan, *prior, &resp.Diagnostics)
		if resp.Diagnostics.HasError() {
			return
		}
	}
	if profile := r.readClusterProfile(ctx, plan.Metadata.Project.ValueString(), plan.Spec.ProfileName.ValueString()); profile != nil {
		validateMKSAgainstProfileWithPrior(plan, prior, profile, &resp.Diagnostics)
		// The pools may carry the login and are decoded as null when the list is only known after apply: do not
		// judge access from a list that is not known yet.
		if !planListPending(ctx, req.Plan, "spec", "worker_pools") {
			validateMKSNodeAccess(plan, prior, profile, &resp.Diagnostics)
		}
		r.validateMKSBootDiskRanges(ctx, plan, prior, profile, &resp.Diagnostics)
	}
}

// validateMKSNodeAccess requires a node password or an SSH key for a new cluster when the profile asks the user to
// choose one (exposeSshKeyChoice) and has no default of its own. The platform installs one key per cluster, so the
// control plane or any pool having one is enough. An existing cluster is not checked: its access was settled when it
// was created, and the platform never returns the password.
func validateMKSNodeAccess(plan mksResourceModel, prior *mksResourceModel, p *apiv1.MKSClusterProfile, diags *diag.Diagnostics) {
	if prior != nil || !p.Spec.ExposeSSHKeyChoice || p.Spec.DefaultSSHKey != "" || p.Spec.DefaultUserPassword != "" {
		return
	}
	cp := plan.Spec.ControlPlane
	if cp == nil || cp.Name.IsNull() {
		return // the control plane is known only after apply: nothing to judge
	}
	has := func(key, password types.String) bool {
		return key.IsUnknown() || password.IsUnknown() || set(key) || set(password)
	}
	if has(cp.SSHKey, cp.UserPassword) {
		return
	}
	for _, wp := range plan.Spec.WorkerPools {
		if has(wp.SSHKey, wp.UserPassword) {
			return
		}
	}
	diags.AddAttributeError(path.Root("spec").AtName("control_plane"), "Node access is required",
		fmt.Sprintf("Profile %q asks you to choose how to log in to the nodes and has no default. Set ssh_key (the name of an SSH key) or user_password on the control plane or on a worker pool.", p.Metadata.Name))
}

// readVMProfile looks a VM SKU up in the cluster's project and then in the system catalog; nil when it cannot be
// read (a bare-metal SKU, or a failed lookup).
func (r *mksClusterResource) readVMProfile(ctx context.Context, project, name string) *apiv1.VMProfile {
	projects := []string{project}
	if project != systemCatalogProject {
		projects = append(projects, systemCatalogProject)
	}
	for _, pr := range projects {
		if pr == "" {
			continue
		}
		if vp, err := r.pd.Clientset.V1alpha1().VMProfiles(pr).Get(ctx, name, gpupaas.GetOptions{}); err == nil {
			return vp
		}
	}
	return nil
}

// validateMKSBootDiskRanges checks a new or changed boot_disk_size_gb against the boot disk range of the pool's VM
// SKU: inside the minimum and maximum, in steps of the increment. A SKU that cannot be read as a VM SKU is skipped.
func (r *mksClusterResource) validateMKSBootDiskRanges(ctx context.Context, plan mksResourceModel, prior *mksResourceModel, p *apiv1.MKSClusterProfile, diags *diag.Diagnostics) {
	root := path.Root("spec")
	project := plan.Metadata.Project.ValueString()
	check := func(at path.Path, size types.Int64, was types.Int64, sku types.String, skuType types.String, allowed *apiv1.MKSGroupProfile) {
		if size.IsNull() || size.IsUnknown() || (!was.IsNull() && !was.IsUnknown() && was.ValueInt64() == size.ValueInt64()) {
			return
		}
		if sku.IsUnknown() || set(skuType) && !strings.EqualFold(skuType.ValueString(), apiv1.MKSSKUTypeVM) {
			return // a SKU known only after apply cannot be looked up
		}
		name := sku.ValueString()
		if !set(sku) && allowed != nil {
			name = allowed.DefaultSKUName
		}
		if name == "" {
			return
		}
		vp := r.readVMProfile(ctx, project, name)
		if vp == nil {
			return
		}
		if msg := bootDiskRangeProblem(size.ValueInt64(), name, vp.Spec.BootDisk); msg != "" {
			diags.AddAttributeError(at.AtName("boot_disk_size_gb"), "Boot disk outside the SKU's range", msg)
		}
	}
	var wasCP types.Int64
	if prior != nil && prior.Spec.ControlPlane != nil {
		wasCP = prior.Spec.ControlPlane.BootDiskSizeGB
	}
	if cp := plan.Spec.ControlPlane; cp != nil {
		check(root.AtName("control_plane"), cp.BootDiskSizeGB, wasCP, cp.SKUName, cp.SKUType, p.Spec.ControlPlane)
	}
	held := map[string]types.Int64{}
	if prior != nil {
		for _, wp := range prior.Spec.WorkerPools {
			held[wp.Name.ValueString()] = wp.BootDiskSizeGB
		}
	}
	for i, wp := range plan.Spec.WorkerPools {
		check(root.AtName("worker_pools").AtListIndex(i), wp.BootDiskSizeGB, held[wp.Name.ValueString()], wp.SKUName, wp.SKUType, p.Spec.Workers)
	}
}

// bootDiskRangeProblem returns why size (GB) is not a boot disk the VM SKU offers, or "". A maximum of 0 is no maximum
// and a step below 1 GB is a fraction, as for the virtual-machine resource.
func bootDiskRangeProblem(size int64, sku string, bd *apiv1.VMBootDisk) string {
	if bd == nil {
		return ""
	}
	lo, okLo := numeric(bd.MinSize)
	hi, okHi := numeric(bd.MaxSize)
	step, okStep := numeric(bd.Increment)
	v := float64(size)
	if okLo && v < lo || okHi && hi > 0 && v > hi {
		return fmt.Sprintf("%d GB is outside the boot disk range of SKU %q (%s-%s GB).", size, sku, trimNumber(lo, okLo), trimNumber(hi, okHi && hi > 0))
	}
	if okStep && okLo && step > 0 {
		if rem := math.Mod(v-lo, step); rem > 1e-9 && step-rem > 1e-9 {
			return fmt.Sprintf("%d GB is not a boot disk size SKU %q offers: sizes step by %s GB from %s GB.", size, sku, trimNumber(step, true), trimNumber(lo, true))
		}
	}
	return ""
}

func trimNumber(v float64, ok bool) string {
	if !ok {
		return "?"
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// validateMKSAgainstProfileWithPrior reports what the profile does not allow. A value that is new or
// changed is an error. A value the cluster already holds (the same problem in the prior state) is
// only a warning: a profile edited after the cluster was created must not block unrelated updates
// such as tags or pool sizes. prior is nil on create.
func validateMKSAgainstProfileWithPrior(plan mksResourceModel, prior *mksResourceModel, p *apiv1.MKSClusterProfile, diags *diag.Diagnostics) {
	var now, was diag.Diagnostics
	validateMKSAgainstProfile(plan, p, &now)
	if prior != nil {
		validateMKSAgainstProfile(*prior, p, &was)
	}
	demoteUnchanged(diags, now, was)
}

// demoteUnchanged appends the diagnostics of now. An error that was already there (the same attribute, summary and
// detail in was) becomes a warning.
func demoteUnchanged(diags *diag.Diagnostics, now, was diag.Diagnostics) {
	unchanged := map[string]bool{}
	for _, d := range was.Errors() {
		unchanged[diagnosticKey(d)] = true
	}
	const note = " The value is unchanged, so it is not blocking this plan."
	for _, d := range now {
		if d.Severity() != diag.SeverityError || !unchanged[diagnosticKey(d)] {
			diags.Append(d)
			continue
		}
		if dp, ok := d.(diag.DiagnosticWithPath); ok {
			diags.AddAttributeWarning(dp.Path(), d.Summary(), d.Detail()+note)
		} else {
			diags.AddWarning(d.Summary(), d.Detail()+note)
		}
	}
}

// diagnosticKey identifies a diagnostic by its attribute path, summary and detail. The detail names
// the offending value, so equal keys mean the same problem with the same value.
func diagnosticKey(d diag.Diagnostic) string {
	at := ""
	if dp, ok := d.(diag.DiagnosticWithPath); ok {
		at = dp.Path().String()
	}
	return at + "|" + d.Summary() + "|" + d.Detail()
}

// validateMKSShape checks what needs no profile: node counts and unique pool names.
func validateMKSShape(plan mksResourceModel, diags *diag.Diagnostics) {
	root := path.Root("spec")
	if cp := plan.Spec.ControlPlane; cp != nil && !cp.NodeCount.IsNull() && !cp.NodeCount.IsUnknown() {
		n := cp.NodeCount.ValueInt64()
		switch {
		case n < 1:
			diags.AddAttributeError(root.AtName("control_plane").AtName("node_count"), "Invalid control plane size", "A cluster needs at least one control-plane node.")
		case n != 1 && n != 3:
			diags.AddAttributeError(root.AtName("control_plane").AtName("node_count"), "Unsupported control plane size",
				fmt.Sprintf("node_count is %d; the control plane has 1 node, or 3 for high availability.", n))
		}
	}
	seen := map[string]bool{}
	for i, wp := range plan.Spec.WorkerPools {
		at := root.AtName("worker_pools").AtListIndex(i)
		if set(wp.Name) {
			if seen[wp.Name.ValueString()] {
				diags.AddAttributeError(at.AtName("name"), "Duplicate worker pool", fmt.Sprintf("Worker pool %q is defined twice; pool names must be unique.", wp.Name.ValueString()))
			}
			seen[wp.Name.ValueString()] = true
		}
		if !wp.NodeCount.IsNull() && !wp.NodeCount.IsUnknown() && wp.NodeCount.ValueInt64() < 0 {
			diags.AddAttributeError(at.AtName("node_count"), "Invalid pool size", "node_count cannot be negative.")
		}
		validateMKSScaling(at, wp, diags)
		validateMKSTaints(at, wp.Taints, diags)
		validateMKSNetwork(at, wp.Network, diags)
		validateMKSBootDisk(at, wp.BootDiskSizeGB, wp.SKUType, diags)
	}
	validateMKSPublicIPs(plan.Spec.WorkerPools, diags)
	if cp := plan.Spec.ControlPlane; cp != nil {
		at := root.AtName("control_plane")
		if set(cp.ScalingMode) && !strings.EqualFold(cp.ScalingMode.ValueString(), apiv1.MKSScalingManual) {
			diags.AddAttributeError(at.AtName("scaling_mode"), "Unsupported scaling mode", "The control plane is always \"manual\".")
		}
		validateMKSNetwork(at, cp.Network, diags)
		validateMKSBootDisk(at, cp.BootDiskSizeGB, cp.SKUType, diags)
	}
	validateMKSDatacenters(plan, diags)
	validateMKSSSHKeys(plan, diags)
}

// validateMKSDatacenters rejects a pool datacenter other than the cluster's location: the platform places
// every node in the location, and its driver refuses a per-pool datacenter.
func validateMKSDatacenters(plan mksResourceModel, diags *diag.Diagnostics) {
	loc := plan.Spec.Location
	if !set(loc) {
		return
	}
	check := func(at path.Path, dc types.String) {
		if set(dc) && !strings.EqualFold(dc.ValueString(), loc.ValueString()) {
			diags.AddAttributeError(at.AtName("datacenter"), "A pool can't use another datacenter",
				fmt.Sprintf("Nodes are placed in the cluster's location (%q); a per-pool datacenter (%q) is not supported. Leave datacenter out, or set it to the location.", loc.ValueString(), dc.ValueString()))
		}
	}
	root := path.Root("spec")
	if cp := plan.Spec.ControlPlane; cp != nil {
		check(root.AtName("control_plane"), cp.Datacenter)
	}
	for i, wp := range plan.Spec.WorkerPools {
		check(root.AtName("worker_pools").AtListIndex(i), wp.Datacenter)
	}
}

// inlineKey reports a value that is key material rather than the name of an SSH key resource. A public key
// is "<type> <base64> [comment]" and a private key is a PEM block, so both hold white space; a name never does.
// A prefix such as "ssh-" is not a sign: a key may well be called "ssh-key-prod".
func inlineKey(v string) bool {
	return strings.ContainsAny(v, " \t\r\n") || strings.HasPrefix(v, "-----BEGIN")
}

// validateMKSSSHKeys rejects an inline public key and warns about pools that name different keys: the platform
// installs one key per cluster and uses the control plane's.
func validateMKSSSHKeys(plan mksResourceModel, diags *diag.Diagnostics) {
	root := path.Root("spec")
	check := func(at path.Path, key types.String) {
		if set(key) && inlineKey(key.ValueString()) {
			diags.AddAttributeError(at.AtName("ssh_key"), "ssh_key must be the name of an SSH key",
				"ssh_key takes the name of an SSH key resource of the workspace; inline public keys are not accepted. Create the key with gpupaas_ssh_key and reference its name.")
		}
	}
	first := ""
	if cp := plan.Spec.ControlPlane; cp != nil {
		check(root.AtName("control_plane"), cp.SSHKey)
		if set(cp.SSHKey) {
			first = cp.SSHKey.ValueString()
		}
	}
	for i, wp := range plan.Spec.WorkerPools {
		at := root.AtName("worker_pools").AtListIndex(i)
		check(at, wp.SSHKey)
		if !set(wp.SSHKey) || inlineKey(wp.SSHKey.ValueString()) {
			continue
		}
		if first == "" {
			first = wp.SSHKey.ValueString()
		} else if wp.SSHKey.ValueString() != first {
			diags.AddAttributeWarning(at.AtName("ssh_key"), "Pools name different SSH keys",
				fmt.Sprintf("The platform installs one SSH key per cluster, and the control plane's key wins: pool %q names %q but %q is used.", wp.Name.ValueString(), wp.SSHKey.ValueString(), first))
		}
	}
}

// validateMKSScaling checks the scaling mode of a worker pool: manual takes a node_count only, automatic
// takes min_nodes and max_nodes with the starting node_count between them.
func validateMKSScaling(at path.Path, wp mksWorkerPoolModel, diags *diag.Diagnostics) {
	known := func(v types.Int64) bool { return !v.IsNull() && !v.IsUnknown() }
	if wp.ScalingMode.IsUnknown() {
		return // the mode is only known after apply: nothing to judge yet
	}
	mode := strings.ToLower(wp.ScalingMode.ValueString())
	switch mode {
	case "", apiv1.MKSScalingManual:
		if known(wp.MinNodes) || known(wp.MaxNodes) || wp.MinNodes.IsUnknown() || wp.MaxNodes.IsUnknown() {
			diags.AddAttributeError(at.AtName("scaling_mode"), "min_nodes and max_nodes need automatic scaling",
				"min_nodes and max_nodes only apply with scaling_mode = \"automatic\". Set the mode, or remove them and use node_count.")
		}
	case apiv1.MKSScalingAutomatic:
		if !known(wp.MinNodes) || !known(wp.MaxNodes) {
			if !wp.MinNodes.IsUnknown() && !wp.MaxNodes.IsUnknown() {
				diags.AddAttributeError(at.AtName("scaling_mode"), "Automatic scaling needs bounds", "Set min_nodes and max_nodes for a pool with scaling_mode = \"automatic\".")
			}
			return
		}
		if wp.NodeCount.IsUnknown() {
			return
		}
		lo, hi := wp.MinNodes.ValueInt64(), wp.MaxNodes.ValueInt64()
		switch {
		case lo < 0 || hi < 1:
			diags.AddAttributeError(at.AtName("min_nodes"), "Invalid bounds", "Automatic scaling needs min_nodes >= 0 and max_nodes >= 1.")
		case lo > hi:
			diags.AddAttributeError(at.AtName("min_nodes"), "Invalid bounds", fmt.Sprintf("min_nodes (%d) is larger than max_nodes (%d).", lo, hi))
		case known(wp.NodeCount) && (wp.NodeCount.ValueInt64() < lo || wp.NodeCount.ValueInt64() > hi):
			diags.AddAttributeError(at.AtName("node_count"), "Starting size outside the bounds",
				fmt.Sprintf("node_count (%d) must lie between min_nodes (%d) and max_nodes (%d).", wp.NodeCount.ValueInt64(), lo, hi))
		}
	default:
		diags.AddAttributeError(at.AtName("scaling_mode"), "Unsupported scaling mode", fmt.Sprintf("scaling_mode must be \"manual\" or \"automatic\"; got %q.", wp.ScalingMode.ValueString()))
	}
}

// validateMKSTaints checks the key and the effect of every taint.
func validateMKSTaints(at path.Path, taints []mksTaintModel, diags *diag.Diagnostics) {
	for i, t := range taints {
		ta := at.AtName("taints").AtListIndex(i)
		if !t.Key.IsUnknown() && t.Key.ValueString() == "" {
			diags.AddAttributeError(ta.AtName("key"), "Empty taint key", "A taint needs a key.")
		}
		if t.Effect.IsNull() || t.Effect.IsUnknown() {
			continue
		}
		switch t.Effect.ValueString() {
		case apiv1.MKSTaintNoSchedule, apiv1.MKSTaintPreferNoSchedule, apiv1.MKSTaintNoExecute:
		default:
			diags.AddAttributeError(ta.AtName("effect"), "Unsupported taint effect",
				fmt.Sprintf("The effect must be NoSchedule, PreferNoSchedule or NoExecute; got %q.", t.Effect.ValueString()))
		}
	}
}

// validateMKSNetwork rejects an empty VPC or subnet: a pool network names both.
func validateMKSNetwork(at path.Path, n *mksPoolNetworkModel, diags *diag.Diagnostics) {
	if !networkConfigured(n) {
		return
	}
	for name, v := range map[string]types.String{"vpc": n.VPC, "subnet": n.Subnet} {
		if !v.IsUnknown() && !set(v) {
			diags.AddAttributeError(at.AtName("network").AtName(name), "Incomplete pool network", "A pool network names both a VPC and a subnet in that VPC.")
		}
	}
}

func validateMKSBootDisk(at path.Path, v types.Int64, skuType types.String, diags *diag.Diagnostics) {
	if v.IsNull() || v.IsUnknown() {
		return
	}
	if v.ValueInt64() < 1 {
		diags.AddAttributeError(at.AtName("boot_disk_size_gb"), "Invalid boot disk size", "boot_disk_size_gb must be at least 1; leave it out to use the compute SKU's default.")
	}
	if set(skuType) && !strings.EqualFold(skuType.ValueString(), "VMAAS") {
		diags.AddAttributeError(at.AtName("boot_disk_size_gb"), "Boot disk on a bare-metal pool", "Only pools of virtual machines (sku_type = \"VMAAS\") take a boot disk size; bare-metal pools refuse it.")
	}
}

// validateMKSPublicIPs requires every worker pool that sets assign_public_ip to set the same value: the
// platform assigns public IPs to all worker nodes or to none.
func validateMKSPublicIPs(pools []mksWorkerPoolModel, diags *diag.Diagnostics) {
	first, unset := -1, ""
	for i, wp := range pools {
		if wp.AssignPublicIP.IsNull() {
			unset = wp.Name.ValueString()
			continue
		}
		if wp.AssignPublicIP.IsUnknown() {
			continue
		}
		if first < 0 {
			first = i
			continue
		}
		if wp.AssignPublicIP.ValueBool() != pools[first].AssignPublicIP.ValueBool() {
			diags.AddAttributeError(path.Root("spec").AtName("worker_pools").AtListIndex(i).AtName("assign_public_ip"), "Worker pools disagree on public IPs",
				fmt.Sprintf("Worker pool %q has assign_public_ip = %t and pool %q has %t. The platform assigns public IPs to all worker nodes or to none, so every worker pool must carry the same value.",
					pools[first].Name.ValueString(), pools[first].AssignPublicIP.ValueBool(), wp.Name.ValueString(), wp.AssignPublicIP.ValueBool()))
		}
	}
	if first >= 0 && unset != "" {
		diags.AddAttributeWarning(path.Root("spec").AtName("worker_pools"), "assign_public_ip is set on some worker pools only",
			fmt.Sprintf("Worker pool %q leaves assign_public_ip out and keeps what the platform holds for it. Every worker pool must carry the same value, so set it on all of them.", unset))
	}
}

// validateMKSFixedAfterCreate rejects a change to what a pool fixes at creation: its network and its boot
// disk. The platform refuses it too, but only after the apply has started; the way out is a new pool.
func validateMKSFixedAfterCreate(plan, state mksResourceModel, diags *diag.Diagnostics) {
	root := path.Root("spec")
	check := func(at path.Path, label string, was, now mksGroupInput) {
		if networkConfigured(was.Network) && networkConfigured(now.Network) && !networkUnknown(now.Network) && !sameNetworkModel(was.Network, now.Network) {
			diags.AddAttributeError(at.AtName("network"), "A pool's network can't be changed",
				fmt.Sprintf("The network of %s can't change after the pool is created (its nodes are already placed in it). Add a new pool with the other network, move the workloads, then remove this one.", label))
		}
		if !was.BootDiskSizeGB.IsNull() && !now.BootDiskSizeGB.IsNull() && !now.BootDiskSizeGB.IsUnknown() && was.BootDiskSizeGB.ValueInt64() != now.BootDiskSizeGB.ValueInt64() {
			diags.AddAttributeError(at.AtName("boot_disk_size_gb"), "A pool's boot disk can't be changed",
				fmt.Sprintf("The boot disk size of %s can't change after the pool is created. Add a new pool with the other size, then remove this one.", label))
		}
	}
	if plan.Spec.ControlPlane != nil && state.Spec.ControlPlane != nil {
		check(root.AtName("control_plane"), "the control plane", state.Spec.ControlPlane.input(), plan.Spec.ControlPlane.input())
		was, now := state.Spec.ControlPlane.NodeCount, plan.Spec.ControlPlane.NodeCount
		if !was.IsNull() && !was.IsUnknown() && !now.IsNull() && !now.IsUnknown() && was.ValueInt64() != now.ValueInt64() {
			diags.AddAttributeError(root.AtName("control_plane").AtName("node_count"), "The control plane size can't be changed",
				fmt.Sprintf("The control plane has %d node(s) and cannot be resized: the platform cannot convert a cluster to high availability (it answers \"not implemented\"). Create a new cluster with %d control-plane nodes instead.", was.ValueInt64(), now.ValueInt64()))
		}
	}
	was := map[string]mksWorkerPoolModel{}
	for _, wp := range state.Spec.WorkerPools {
		was[wp.Name.ValueString()] = wp
	}
	for i, wp := range plan.Spec.WorkerPools {
		if old, ok := was[wp.Name.ValueString()]; ok {
			check(root.AtName("worker_pools").AtListIndex(i), fmt.Sprintf("worker pool %q", wp.Name.ValueString()), old.input(), wp.input())
		}
	}
}

// warnMKSReimage warns that a changed os_image re-images the pool: the platform replaces its nodes in a rolling
// update.
func warnMKSReimage(plan, state mksResourceModel, diags *diag.Diagnostics) {
	root := path.Root("spec")
	warn := func(at path.Path, label string, was, now types.String) {
		if set(was) && set(now) && was.ValueString() != now.ValueString() {
			diags.AddAttributeWarning(at.AtName("os_image"), "Changing the OS image re-images the pool",
				fmt.Sprintf("The OS image of %s changes from %q to %q. The platform re-images the pool's nodes (a rolling replacement); workloads on them are evicted.", label, was.ValueString(), now.ValueString()))
		}
	}
	if plan.Spec.ControlPlane != nil && state.Spec.ControlPlane != nil {
		warn(root.AtName("control_plane"), "the control plane", state.Spec.ControlPlane.OSImage, plan.Spec.ControlPlane.OSImage)
	}
	was := map[string]mksWorkerPoolModel{}
	for _, wp := range state.Spec.WorkerPools {
		was[wp.Name.ValueString()] = wp
	}
	for i, wp := range plan.Spec.WorkerPools {
		if old, ok := was[wp.Name.ValueString()]; ok {
			warn(root.AtName("worker_pools").AtListIndex(i), fmt.Sprintf("worker pool %q", wp.Name.ValueString()), old.OSImage, wp.OSImage)
		}
	}
}

func networkUnknown(n *mksPoolNetworkModel) bool {
	return n != nil && (n.VPC.IsUnknown() || n.Subnet.IsUnknown() || n.SecurityGroup.IsUnknown())
}

func sameNetworkModel(a, b *mksPoolNetworkModel) bool {
	// A security group left out of the configuration keeps the one the pool holds, so it is not compared then.
	if set(b.SecurityGroup) && set(a.SecurityGroup) && a.SecurityGroup.ValueString() != b.SecurityGroup.ValueString() {
		return false
	}
	return a.VPC.ValueString() == b.VPC.ValueString() && a.Subnet.ValueString() == b.Subnet.ValueString()
}

func validateMKSAgainstProfile(plan mksResourceModel, p *apiv1.MKSClusterProfile, diags *diag.Diagnostics) {
	root := path.Root("spec")
	profile := p.Metadata.Name
	s := plan.Spec

	if v := s.KubernetesVersion; set(v) && len(p.Spec.SupportedKubernetesVersions) > 0 {
		ok := false
		for _, sv := range p.Spec.SupportedKubernetesVersions {
			if sameK8sVersion(sv, v.ValueString()) || sameK8sVersion(v.ValueString(), sv) {
				ok = true
			}
		}
		if !ok {
			diags.AddAttributeError(root.AtName("kubernetes_version"), "Unsupported Kubernetes version",
				fmt.Sprintf("Profile %q supports %s; got %q.%s", profile, strings.Join(p.Spec.SupportedKubernetesVersions, ", "), v.ValueString(), didYouMean(trimV(v.ValueString()), p.Spec.SupportedKubernetesVersions)))
		}
	}
	checkIn(diags, root.AtName("location"), "datacenter", s.Location, p.Spec.Datacenters, profile)
	if n := s.Networking; n != nil && (set(n.CNIName) || set(n.CNIVersion)) && len(p.Spec.SupportedCNIs) > 0 {
		ok := false
		var names []string
		for _, c := range p.Spec.SupportedCNIs {
			names = append(names, c.Name+" "+c.Version)
			if (!set(n.CNIName) || strings.EqualFold(c.Name, n.CNIName.ValueString())) && (!set(n.CNIVersion) || c.Version == n.CNIVersion.ValueString()) {
				ok = true
			}
		}
		if !ok {
			diags.AddAttributeError(root.AtName("networking"), "Unsupported CNI",
				fmt.Sprintf("Profile %q supports %s; got %s %s.", profile, strings.Join(names, ", "), n.CNIName.ValueString(), n.CNIVersion.ValueString()))
		}
	}
	if cp := s.ControlPlane; cp != nil {
		at := root.AtName("control_plane")
		checkGroup(diags, at, profile, p.Spec.ControlPlane, p.Spec.Datacenters, cp.SKUName, cp.SKUType, cp.OSImage, cp.Datacenter)
	}
	for i, wp := range s.WorkerPools {
		checkGroup(diags, root.AtName("worker_pools").AtListIndex(i), profile, p.Spec.Workers, p.Spec.Datacenters, wp.SKUName, wp.SKUType, wp.OSImage, wp.Datacenter)
	}
}

// checkGroup validates the SKU, SKU type, image and datacenter of a node group against what the
// profile allows for it.
func checkGroup(diags *diag.Diagnostics, at path.Path, profile string, allowed *apiv1.MKSGroupProfile, datacenters []string, sku, skuType, image, datacenter types.String) {
	if allowed != nil {
		checkIn(diags, at.AtName("sku_name"), "SKU", sku, allowed.SKUNames, profile)
		checkIn(diags, at.AtName("sku_type"), "SKU type", skuType, allowed.SKUTypes, profile)
		checkIn(diags, at.AtName("os_image"), "image", image, allowed.Images, profile)
	}
	checkIn(diags, at.AtName("datacenter"), "datacenter", datacenter, datacenters, profile)
}

// checkIn reports a configured value that is not among the allowed ones (case-insensitive),
// with the allowed values and the nearest suggestion. An empty allowed list allows anything.
func checkIn(diags *diag.Diagnostics, at path.Path, label string, v types.String, allowed []string, profile string) {
	if !set(v) || len(allowed) == 0 {
		return
	}
	for _, a := range allowed {
		if strings.EqualFold(a, v.ValueString()) {
			return
		}
	}
	diags.AddAttributeError(at, "Not allowed by the profile",
		fmt.Sprintf("Profile %q allows these %s values: %s; got %q.%s", profile, label, strings.Join(allowed, ", "), v.ValueString(), didYouMean(v.ValueString(), allowed)))
}

func didYouMean(value string, candidates []string) string {
	if c := closestName(value, candidates); c != "" {
		return fmt.Sprintf(" Did you mean %q?", c)
	}
	return ""
}

// poolName is a DNS-1123 label.
var poolName = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

// validateMKSPoolNames rejects a new worker pool whose name the platform refuses: control-plane is reserved
// and names are DNS-1123 labels. A pool the cluster already has keeps its name whatever it is.
func validateMKSPoolNames(plan mksResourceModel, prior *mksResourceModel, diags *diag.Diagnostics) {
	held := map[string]bool{}
	if prior != nil {
		for _, wp := range prior.Spec.WorkerPools {
			held[wp.Name.ValueString()] = true
		}
	}
	for i, wp := range plan.Spec.WorkerPools {
		if !set(wp.Name) || held[wp.Name.ValueString()] {
			continue
		}
		name := wp.Name.ValueString()
		at := path.Root("spec").AtName("worker_pools").AtListIndex(i).AtName("name")
		switch {
		case name == "control-plane":
			diags.AddAttributeError(at, "Reserved pool name", "control-plane is the name of the control plane; give the worker pool another name.")
		case len(name) > 63 || !poolName.MatchString(name):
			diags.AddAttributeError(at, "Invalid pool name", fmt.Sprintf("%q is not a valid pool name: use lower-case letters, digits and '-', starting and ending with a letter or digit, at most 63 characters.", name))
		}
	}
}

// validateMKSUpgrade checks a change of kubernetes_version against the targets the platform reports: one
// minor version at a time, never a downgrade. Without a reported list (an older controller) it checks nothing.
func validateMKSUpgrade(plan, state mksResourceModel, diags *diag.Diagnostics) {
	want, have := plan.Spec.KubernetesVersion, state.Spec.KubernetesVersion
	if !set(want) || !set(have) || sameK8sVersion(want.ValueString(), have.ValueString()) {
		return
	}
	if len(state.Status.AvailableVersions) == 0 {
		return
	}
	var targets []string
	for _, v := range state.Status.AvailableVersions {
		targets = append(targets, v.ValueString())
		if sameK8sVersion(want.ValueString(), v.ValueString()) || sameK8sVersion(v.ValueString(), want.ValueString()) {
			return
		}
	}
	// The cluster's own version (a patch of it) is not an upgrade.
	if cur := state.Status.KubernetesVersion; set(cur) && sameK8sVersion(want.ValueString(), cur.ValueString()) {
		return
	}
	diags.AddAttributeError(path.Root("spec").AtName("kubernetes_version"), "Version not available as an upgrade",
		fmt.Sprintf("The cluster runs %s and the platform offers an upgrade to %s only (one minor version at a time, no downgrade); got %q.",
			firstNonEmpty(state.Status.KubernetesVersion.ValueString(), have.ValueString()), strings.Join(targets, ", "), want.ValueString()))
}
