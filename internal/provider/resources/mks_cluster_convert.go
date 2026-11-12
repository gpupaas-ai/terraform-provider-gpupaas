package resources

import (
	"encoding/json"
	"sort"
	"strings"

	apiv1 "github.com/gpupaas-ai/gpupaas-go/apis/v1alpha1"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Models for gpupaas_kubernetes_cluster (first-class managed Kubernetes, paas.k8smgmt.io/v3).

type mksBlueprintModel struct {
	Name    types.String `tfsdk:"name"`
	Version types.String `tfsdk:"version"`
}

type mksNetworkingModel struct {
	VPC           types.String `tfsdk:"vpc"`
	Subnet        types.String `tfsdk:"subnet"`
	PodSubnet     types.String `tfsdk:"pod_subnet"`
	ServiceSubnet types.String `tfsdk:"service_subnet"`
	CNIName       types.String `tfsdk:"cni_name"`
	CNIVersion    types.String `tfsdk:"cni_version"`
}

// mksPoolNetworkModel is the VPC, subnet and security group of a node group.
type mksPoolNetworkModel struct {
	VPC           types.String `tfsdk:"vpc"`
	Subnet        types.String `tfsdk:"subnet"`
	SecurityGroup types.String `tfsdk:"security_group"`
}

// mksTaintModel is one taint of a worker pool.
type mksTaintModel struct {
	Key    types.String `tfsdk:"key"`
	Value  types.String `tfsdk:"value"`
	Effect types.String `tfsdk:"effect"`
}

type mksControlPlaneModel struct {
	Name           types.String         `tfsdk:"name"`
	Datacenter     types.String         `tfsdk:"datacenter"`
	OSImage        types.String         `tfsdk:"os_image"`
	SKUName        types.String         `tfsdk:"sku_name"`
	SKUType        types.String         `tfsdk:"sku_type"`
	SSHKey         types.String         `tfsdk:"ssh_key"`
	NodeCount      types.Int64          `tfsdk:"node_count"`
	ScalingMode    types.String         `tfsdk:"scaling_mode"`
	ComputeType    types.String         `tfsdk:"compute_type"`
	Network        *mksPoolNetworkModel `tfsdk:"network"`
	BootDiskSizeGB types.Int64          `tfsdk:"boot_disk_size_gb"`
	AssignPublicIP types.Bool           `tfsdk:"assign_public_ip"`
	UserPassword   types.String         `tfsdk:"user_password"`
}

type mksWorkerPoolModel struct {
	Name           types.String         `tfsdk:"name"`
	Datacenter     types.String         `tfsdk:"datacenter"`
	OSImage        types.String         `tfsdk:"os_image"`
	SKUName        types.String         `tfsdk:"sku_name"`
	SKUType        types.String         `tfsdk:"sku_type"`
	SSHKey         types.String         `tfsdk:"ssh_key"`
	NodeCount      types.Int64          `tfsdk:"node_count"`
	ScalingMode    types.String         `tfsdk:"scaling_mode"`
	MinNodes       types.Int64          `tfsdk:"min_nodes"`
	MaxNodes       types.Int64          `tfsdk:"max_nodes"`
	Network        *mksPoolNetworkModel `tfsdk:"network"`
	BootDiskSizeGB types.Int64          `tfsdk:"boot_disk_size_gb"`
	AssignPublicIP types.Bool           `tfsdk:"assign_public_ip"`
	UserPassword   types.String         `tfsdk:"user_password"`
	Labels         types.Map            `tfsdk:"labels"`
	Annotations    types.Map            `tfsdk:"annotations"`
	Taints         []mksTaintModel      `tfsdk:"taints"`
}

type mksSpecModel struct {
	ProfileName       types.String          `tfsdk:"profile_name"`
	KubernetesVersion types.String          `tfsdk:"kubernetes_version"`
	Location          types.String          `tfsdk:"location"`
	Blueprint         *mksBlueprintModel    `tfsdk:"blueprint"`
	Networking        *mksNetworkingModel   `tfsdk:"networking"`
	ControlPlane      *mksControlPlaneModel `tfsdk:"control_plane"`
	WorkerPools       []mksWorkerPoolModel  `tfsdk:"worker_pools"`
	Tags              types.Map             `tfsdk:"tags"`
	ContractTerm      types.String          `tfsdk:"contract_term"`
}

type mksNodeModel struct {
	Hostname          types.String `tfsdk:"hostname"`
	NodePool          types.String `tfsdk:"node_pool"`
	KubernetesVersion types.String `tfsdk:"kubernetes_version"`
	// The fields below are reported by controllers of release v3.1.41 and later; they are null before.
	PrivateIP       types.String `tfsdk:"private_ip"`
	Roles           types.List   `tfsdk:"roles"`
	OperatingSystem types.String `tfsdk:"operating_system"`
	Arch            types.String `tfsdk:"arch"`
	Status          types.String `tfsdk:"status"`
}

type mksStatusModel struct {
	Phase             types.String   `tfsdk:"phase"`
	Status            types.String   `tfsdk:"status"`
	Reason            types.String   `tfsdk:"reason"`
	Action            types.String   `tfsdk:"action"`
	ClusterID         types.String   `tfsdk:"cluster_id"`
	KubernetesVersion types.String   `tfsdk:"kubernetes_version"`
	NodeCount         types.Int64    `tfsdk:"node_count"`
	Nodes             []mksNodeModel `tfsdk:"nodes"`
	// AvailableVersions are the Kubernetes versions the cluster can be upgraded to.
	AvailableVersions []types.String `tfsdk:"available_kubernetes_versions"`
}

type mksTimeoutsModel struct {
	Create types.String `tfsdk:"create"`
	Update types.String `tfsdk:"update"`
	Delete types.String `tfsdk:"delete"`
}

type mksResourceModel struct {
	ID       types.String      `tfsdk:"id"`
	Metadata MetadataModel     `tfsdk:"metadata"`
	Spec     mksSpecModel      `tfsdk:"spec"`
	Timeouts *mksTimeoutsModel `tfsdk:"timeouts"`
	// ForceDestroy removes the cluster record even if its infrastructure cannot be torn down.
	ForceDestroy types.Bool     `tfsdk:"force_destroy"`
	Status       mksStatusModel `tfsdk:"status"`
}

func mksClusterID(project, workspace, name string) string {
	if workspace == "" {
		return project + "/" + name
	}
	return project + "/" + workspace + "/" + name
}

// set reports whether a configured string has a value.
func set(v types.String) bool { return !v.IsNull() && !v.IsUnknown() && v.ValueString() != "" }

func trimV(v string) string { return strings.TrimPrefix(strings.TrimSpace(v), "v") }

// sameK8sVersion reports whether got satisfies want: "1.36" matches "v1.36.4".
func sameK8sVersion(want, got string) bool {
	w, g := trimV(want), trimV(got)
	return w == g || (w != "" && strings.HasPrefix(g, w+"."))
}

// buildMKSCluster turns the planned model into the object sent to the API. With current set
// (an update) the cluster read from the API is the starting point, so fields the provider does
// not manage - sharing, billing, derived node group fields, anything the SDK does not model -
// are sent back unchanged. The planned values then override what the user configured.
func buildMKSCluster(plan mksResourceModel, current *apiv1.MKSCluster) *apiv1.MKSCluster {
	return buildMKSClusterFrom(plan, current, nil)
}

// buildMKSClusterFrom is buildMKSCluster with the prior state of an update: it tells which node
// passwords, labels and taints the user already had, so a password the user did not change is not sent
// again (a read returns it masked) and a label or taint that was removed from the configuration is cleared.
func buildMKSClusterFrom(plan mksResourceModel, current *apiv1.MKSCluster, prior *mksSpecModel) *apiv1.MKSCluster {
	var state *mksResourceModel
	if prior != nil {
		state = &mksResourceModel{Spec: *prior}
	}
	return buildMKSClusterAfter(plan, current, state)
}

// buildMKSClusterAfter is buildMKSClusterFrom with the whole prior state, so that the metadata the user did not
// change (labels, annotations) is sent back as the platform holds it, and the tags the user never managed survive.
func buildMKSClusterAfter(plan mksResourceModel, current *apiv1.MKSCluster, priorState *mksResourceModel) *apiv1.MKSCluster {
	var prior *mksSpecModel
	if priorState != nil {
		prior = &priorState.Spec
	}
	obj := &apiv1.MKSCluster{
		TypeMeta: apiv1.TypeMeta{APIVersion: apiv1.APIVersion, Kind: apiv1.KindMKSCluster},
		Metadata: metadataToSDK(plan.Metadata),
	}
	var spec apiv1.MKSClusterSpec
	if current != nil {
		cp := current.DeepCopyObject().(*apiv1.MKSCluster)
		spec = cp.Spec
		// The platform resolves platformVersion itself and refuses a request that carries it.
		delete(spec.Extra, "platformVersion")
		obj.Version = cp.Version
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
		// The platform adds labels of its own (paas.envmgmt.io/compute_type, for example): what the user did not
		// change since the last apply is sent back as it was read.
		if planned.Labels != nil && (priorState == nil || !plan.Metadata.Labels.Equal(priorState.Metadata.Labels)) {
			meta.Labels = planned.Labels
		}
		if planned.Annotations != nil && (priorState == nil || !plan.Metadata.Annotations.Equal(priorState.Metadata.Annotations)) {
			meta.Annotations = planned.Annotations
		}
		obj.Metadata = meta
	}
	p := plan.Spec
	spec.Profile = &apiv1.MKSProfileRef{Name: p.ProfileName.ValueString()}
	if set(p.KubernetesVersion) {
		// Keep the version the API already reports when it satisfies the configured one, so a
		// short "1.36" does not look like a change from "v1.36.4".
		if current == nil || !sameK8sVersion(p.KubernetesVersion.ValueString(), current.Spec.KubernetesVersion) {
			spec.KubernetesVersion = p.KubernetesVersion.ValueString()
		}
	}
	if set(p.Location) {
		spec.Location = p.Location.ValueString()
	}
	if b := p.Blueprint; b != nil && (set(b.Name) || set(b.Version)) {
		bp := apiv1.MKSBlueprint{}
		if spec.Blueprint != nil {
			bp = *spec.Blueprint
		}
		if set(b.Name) {
			bp.Name = b.Name.ValueString()
		}
		if set(b.Version) {
			bp.Version = b.Version.ValueString()
		}
		spec.Blueprint = &bp
	}
	if n := p.Networking; n != nil {
		nw := apiv1.MKSNetworking{}
		if spec.Networking != nil {
			nw = *spec.Networking
		}
		assign := func(dst *string, v types.String) {
			if set(v) {
				*dst = v.ValueString()
			}
		}
		// Controllers that take the network per pool clear the cluster-level VPC and subnet (they read back
		// empty). Sending them again would make every apply look like a change, so an update leaves them.
		if !clusterNetworkCleared(current) {
			assign(&nw.VPC, n.VPC)
			assign(&nw.Subnet, n.Subnet)
		}
		assign(&nw.PodSubnet, n.PodSubnet)
		assign(&nw.ServiceSubnet, n.ServiceSubnet)
		if set(n.CNIName) || set(n.CNIVersion) {
			cni := apiv1.MKSCNI{}
			if nw.CNI != nil {
				cni = *nw.CNI
			}
			assign(&cni.Name, n.CNIName)
			assign(&cni.Version, n.CNIVersion)
			nw.CNI = &cni
		}
		spec.Networking = &nw
	}
	if cp := p.ControlPlane; cp != nil {
		base := apiv1.MKSNodeGroup{}
		if spec.ControlPlane != nil {
			base = *spec.ControlPlane
		}
		var before *mksGroupInput
		if prior != nil && prior.ControlPlane != nil {
			in := prior.ControlPlane.input()
			before = &in
		}
		g := overlayNodeGroup(base, cp.input(), before)
		spec.ControlPlane = &g
	}
	pools := make([]apiv1.MKSNodeGroup, 0, len(p.WorkerPools))
	existing := map[string]apiv1.MKSNodeGroup{}
	for _, wp := range spec.WorkerPools {
		existing[wp.Name] = wp
	}
	priorPools := map[string]mksGroupInput{}
	if prior != nil {
		for _, wp := range prior.WorkerPools {
			priorPools[wp.Name.ValueString()] = wp.input()
		}
	}
	for _, wp := range p.WorkerPools {
		base := existing[wp.Name.ValueString()]
		var before *mksGroupInput
		if in, ok := priorPools[wp.Name.ValueString()]; ok {
			before = &in
		}
		pools = append(pools, overlayNodeGroup(base, wp.input(), before))
	}
	spec.WorkerPools = pools
	// Tags the user manages replace the ones they managed before; tags set elsewhere (the console) are kept.
	var plannedTags, priorTags map[string]string
	if !p.Tags.IsNull() && !p.Tags.IsUnknown() {
		plannedTags = elementsToStringMap(p.Tags)
	}
	if prior != nil {
		priorTags = elementsToStringMap(prior.Tags)
	}
	if current != nil && priorState != nil {
		spec.Tags = mergeManagedLabels(spec.Tags, priorTags, plannedTags)
	} else {
		spec.Tags = plannedTags // a create, or no knowledge of what the user managed before
	}
	if set(p.ContractTerm) {
		billing := apiv1.MKSBilling{Variables: map[string]string{}}
		if spec.Billing != nil {
			for k, v := range spec.Billing.Variables {
				billing.Variables[k] = v
			}
		}
		billing.Variables["ContractTerm"] = p.ContractTerm.ValueString()
		spec.Billing = &billing
	}
	obj.Spec = spec
	return obj
}

// clusterNetworkCleared reports that the cluster holds no cluster-level VPC and subnet: a controller that
// places nodes per pool has dropped them.
func clusterNetworkCleared(current *apiv1.MKSCluster) bool {
	return current != nil && current.Spec.Networking != nil && current.Spec.Networking.VPC == "" && current.Spec.Networking.Subnet == ""
}

// keepIfServerEmpty returns the server value, or the configured one when the server reports none (a
// controller that clears a field it was given), or null when neither exists.
func keepIfServerEmpty(configured types.String, server string) types.String {
	if server != "" {
		return types.StringValue(server)
	}
	if set(configured) {
		return configured
	}
	return types.StringNull()
}

// mksGroupInput is what the user configured for a control plane or a worker pool, in one shape for both.
type mksGroupInput struct {
	Name, Datacenter, OSImage, SKUName, SKUType, SSHKey, ScalingMode, UserPassword types.String
	NodeCount, MinNodes, MaxNodes, BootDiskSizeGB                                  types.Int64
	AssignPublicIP                                                                 types.Bool
	Network                                                                        *mksPoolNetworkModel
	Labels, Annotations                                                            types.Map
	Taints                                                                         []mksTaintModel
}

func (m mksControlPlaneModel) input() mksGroupInput {
	return mksGroupInput{
		Name: m.Name, Datacenter: m.Datacenter, OSImage: m.OSImage, SKUName: m.SKUName, SKUType: m.SKUType, SSHKey: m.SSHKey,
		ScalingMode: m.ScalingMode, UserPassword: m.UserPassword, NodeCount: m.NodeCount, BootDiskSizeGB: m.BootDiskSizeGB,
		AssignPublicIP: m.AssignPublicIP, Network: m.Network, Labels: types.MapNull(types.StringType), Annotations: types.MapNull(types.StringType),
		MinNodes: types.Int64Null(), MaxNodes: types.Int64Null(),
	}
}

func (m mksWorkerPoolModel) input() mksGroupInput {
	return mksGroupInput{
		Name: m.Name, Datacenter: m.Datacenter, OSImage: m.OSImage, SKUName: m.SKUName, SKUType: m.SKUType, SSHKey: m.SSHKey,
		ScalingMode: m.ScalingMode, UserPassword: m.UserPassword, NodeCount: m.NodeCount, MinNodes: m.MinNodes, MaxNodes: m.MaxNodes,
		BootDiskSizeGB: m.BootDiskSizeGB, AssignPublicIP: m.AssignPublicIP, Network: m.Network, Labels: m.Labels, Annotations: m.Annotations, Taints: m.Taints,
	}
}

// networkConfigured reports whether the user gave a network. A block that is absent decodes from state as
// an object whose attributes are all null.
func networkConfigured(n *mksPoolNetworkModel) bool {
	return n != nil && (set(n.VPC) || set(n.Subnet) || set(n.SecurityGroup))
}

func isAutomatic(mode string) bool { return strings.EqualFold(mode, apiv1.MKSScalingAutomatic) }

// overlayNodeGroup applies the configured fields to a node group read from the API. When the
// SKU changes, the fields the API derived from the old SKU are cleared so it derives them again.
// before is the group's configuration in the prior state (nil for a new group).
func overlayNodeGroup(base apiv1.MKSNodeGroup, in mksGroupInput, before *mksGroupInput) apiv1.MKSNodeGroup {
	g := base
	if set(in.SKUName) && in.SKUName.ValueString() != g.SKUName || set(in.SKUType) && in.SKUType.ValueString() != g.SKUType {
		g.ComputeType, g.SKUDisplayName, g.SKUDisplayText, g.Shape = "", "", "", nil
		if set(in.SKUType) && !strings.EqualFold(in.SKUType.ValueString(), "VMAAS") {
			g.BootDiskSizeGb = 0 // only virtual-machine pools take a boot disk
		}
	}
	assign := func(dst *string, v types.String) {
		if set(v) {
			*dst = v.ValueString()
		}
	}
	// The API names a control-plane group through displayName and leaves name empty: the same
	// name is not a change, and sending it would make every apply look like an update.
	if set(in.Name) && in.Name.ValueString() != firstNonEmpty(g.Name, g.DisplayName) {
		g.Name = in.Name.ValueString()
	}
	assign(&g.Datacenter, in.Datacenter)
	assign(&g.OSImage, in.OSImage)
	assign(&g.SKUName, in.SKUName)
	assign(&g.SKUType, in.SKUType)
	assign(&g.SSHKey, in.SSHKey)

	sc := apiv1.MKSScaling{}
	if g.Scaling != nil {
		sc = *g.Scaling
	}
	wasAutomatic := isAutomatic(sc.Mode)
	assign(&sc.Mode, in.ScalingMode)
	switch {
	case isAutomatic(sc.Mode):
		if !wasAutomatic {
			sc.Count = 0
		}
		if !in.MinNodes.IsNull() && !in.MinNodes.IsUnknown() {
			sc.Min = int32(in.MinNodes.ValueInt64())
		}
		if !in.MaxNodes.IsNull() && !in.MaxNodes.IsUnknown() {
			sc.Max = int32(in.MaxNodes.ValueInt64())
		}
		// node_count is the size the pool starts with. Once it runs, the autoscaler owns the size: keep
		// what the platform holds while it is inside the bounds, so an update does not resize the pool.
		// A desired size of 0 is how a controller that omits the field looks: keep it as read.
		switch {
		case wasAutomatic && (sc.Desired == 0 || sc.Desired >= sc.Min && sc.Desired <= sc.Max):
		case !in.NodeCount.IsNull() && !in.NodeCount.IsUnknown():
			sc.Desired = int32(in.NodeCount.ValueInt64())
			if wasAutomatic { // the bounds changed under the current size
				sc.Desired = min(max(sc.Desired, sc.Min), sc.Max)
			}
		}
	default:
		if wasAutomatic {
			sc.Min, sc.Desired, sc.Max = 0, 0, 0
		}
		if !in.NodeCount.IsNull() && !in.NodeCount.IsUnknown() {
			sc.Count = int32(in.NodeCount.ValueInt64())
		}
	}
	g.Scaling = &sc

	if n := in.Network; networkConfigured(n) {
		nw := apiv1.MKSNodePoolNetwork{}
		if g.Network != nil {
			nw = *g.Network
		}
		assign(&nw.VPC, n.VPC)
		assign(&nw.Subnet, n.Subnet)
		assign(&nw.SecurityGroup, n.SecurityGroup)
		g.Network = &nw
	}
	if !in.BootDiskSizeGB.IsNull() && !in.BootDiskSizeGB.IsUnknown() {
		g.BootDiskSizeGb = apiv1.MKSInt(in.BootDiskSizeGB.ValueInt64())
	}
	if !in.AssignPublicIP.IsNull() && !in.AssignPublicIP.IsUnknown() {
		v := in.AssignPublicIP.ValueBool()
		g.AssignPublicIP = &v
	}
	// A read returns the password masked: send the configured one only when it is new or changed.
	if set(in.UserPassword) && (before == nil || !before.UserPassword.Equal(in.UserPassword)) {
		g.UserPassword = in.UserPassword.ValueString()
	}
	// The platform may add labels and taints of its own. What the user managed before is replaced by what
	// is configured now; whatever else the pool holds is sent back unchanged.
	var priorLabels, priorAnnotations map[string]string
	var priorTaints []mksTaintModel
	if before != nil {
		priorLabels, priorAnnotations, priorTaints = elementsToStringMap(before.Labels), elementsToStringMap(before.Annotations), before.Taints
	}
	switch {
	case !in.Labels.IsNull() && !in.Labels.IsUnknown():
		g.Labels = mergeManagedLabels(g.Labels, priorLabels, elementsToStringMap(in.Labels))
	case before != nil && !before.Labels.IsNull():
		g.Labels = mergeManagedLabels(g.Labels, priorLabels, nil) // removed from the configuration
	}
	switch {
	case !in.Annotations.IsNull() && !in.Annotations.IsUnknown():
		g.Annotations = mergeManagedLabels(g.Annotations, priorAnnotations, elementsToStringMap(in.Annotations))
	case before != nil && !before.Annotations.IsNull():
		g.Annotations = mergeManagedLabels(g.Annotations, priorAnnotations, nil)
	}
	switch {
	case in.Taints != nil:
		g.Taints = mergeManagedTaints(g.Taints, priorTaints, in.Taints)
	case before != nil && before.Taints != nil:
		g.Taints = mergeManagedTaints(g.Taints, priorTaints, nil)
	}
	return g
}

// mergeManagedLabels returns the labels (or annotations) to send: those the pool holds, minus the keys the
// user managed before, plus the configured ones.
func mergeManagedLabels(server, prior, planned map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range server {
		if _, managed := prior[k]; !managed {
			out[k] = v
		}
	}
	for k, v := range planned {
		out[k] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// taintKey identifies a taint; a missing value and an empty one are the same.
func taintKey(key, value, effect string) [3]string { return [3]string{key, value, effect} }

func wireTaintKey(e any) ([3]string, bool) {
	m, ok := e.(map[string]any)
	if !ok {
		return [3]string{}, false
	}
	str := func(k string) string { v, _ := m[k].(string); return v }
	return taintKey(str("key"), str("value"), str("effect")), true
}

// mergeManagedTaints returns the taints to send: those the pool holds, in the order it holds them, minus the
// ones the user managed before and no longer configures, plus the configured ones that are not there yet.
func mergeManagedTaints(server []any, prior, planned []mksTaintModel) []any {
	managed := map[[3]string]bool{}
	for _, t := range prior {
		managed[taintKey(t.Key.ValueString(), t.Value.ValueString(), t.Effect.ValueString())] = true
	}
	want := map[[3]string]bool{}
	for _, t := range planned {
		want[taintKey(t.Key.ValueString(), t.Value.ValueString(), t.Effect.ValueString())] = true
	}
	var out []any
	held := map[[3]string]bool{}
	for _, e := range server {
		k, ok := wireTaintKey(e)
		if ok && managed[k] && !want[k] {
			continue
		}
		if ok {
			held[k] = true
		}
		out = append(out, e)
	}
	for _, e := range taintsToWire(planned) {
		if k, _ := wireTaintKey(e); !held[k] {
			out = append(out, e)
		}
	}
	return out
}

// taintsToWire turns the configured taints into the objects the API takes.
func taintsToWire(in []mksTaintModel) []any {
	out := make([]any, 0, len(in))
	for _, t := range in {
		m := map[string]any{"key": t.Key.ValueString(), "effect": t.Effect.ValueString()}
		if set(t.Value) {
			m["value"] = t.Value.ValueString()
		}
		out = append(out, m)
	}
	return out
}

// taintsFromWire reads the taints the API reports. An entry that is not a key/effect object is skipped.
func taintsFromWire(in []any) []mksTaintModel {
	var out []mksTaintModel
	for _, e := range in {
		m, ok := e.(map[string]any)
		if !ok {
			continue
		}
		str := func(k string) types.String {
			v, _ := m[k].(string)
			return nullableString(v)
		}
		out = append(out, mksTaintModel{Key: str("key"), Value: str("value"), Effect: str("effect")})
	}
	return out
}

// sameTaints compares two taint lists as sets: the order is not part of the meaning.
func sameTaints(a, b []mksTaintModel) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[[3]string]int{}
	k := func(t mksTaintModel) [3]string {
		return [3]string{t.Key.ValueString(), t.Value.ValueString(), t.Effect.ValueString()}
	}
	for _, t := range a {
		seen[k(t)]++
	}
	for _, t := range b {
		if seen[k(t)] == 0 {
			return false
		}
		seen[k(t)]--
	}
	return true
}

// keepOrDrift returns what to store for an Optional attribute that the API may default. When
// the user configured a value it is kept unless the API now reports a different one (drift);
// when nothing was configured it stays null, so a server default never shows as a change.
// During import (importing) the server value is taken.
func keepOrDrift(prior types.String, server string, importing bool) types.String {
	if importing {
		return nullableString(server)
	}
	if prior.IsNull() || prior.IsUnknown() {
		return types.StringNull()
	}
	if server != "" && server != prior.ValueString() {
		return types.StringValue(server)
	}
	return prior
}

// scalingCount is the number of nodes a group has or asks for: the count of a manual group, the
// current size (desired) of an automatic one.
func scalingCount(g *apiv1.MKSNodeGroup) int64 {
	if g == nil || g.Scaling == nil {
		return 0
	}
	if isAutomatic(g.Scaling.Mode) {
		if g.Scaling.Desired == 0 {
			return int64(g.Scaling.Min) // a controller that omits the size: the least it holds
		}
		return int64(g.Scaling.Desired)
	}
	return int64(g.Scaling.Count)
}

// keptPassword returns the node password to store. The API never reports it (a read gives "******"),
// so the state keeps what the user configured, and nothing after an import.
func keptPassword(prior types.String, importing bool) types.String {
	if importing || prior.IsNull() || prior.IsUnknown() {
		return types.StringNull()
	}
	return prior
}

// keepOrDriftInt is keepOrDrift for an Optional integer the API may default (0 means unset).
func keepOrDriftInt(prior types.Int64, server int64, importing bool) types.Int64 {
	if importing {
		if server == 0 {
			return types.Int64Null()
		}
		return types.Int64Value(server)
	}
	if prior.IsNull() || prior.IsUnknown() {
		return types.Int64Null()
	}
	if server != 0 && server != prior.ValueInt64() {
		return types.Int64Value(server)
	}
	return prior
}

// keepOrDriftBool is keepOrDrift for an Optional boolean the API reports through a pointer.
func keepOrDriftBool(prior types.Bool, server *bool, importing bool) types.Bool {
	if importing {
		if server == nil {
			return types.BoolNull()
		}
		return types.BoolValue(*server)
	}
	if prior.IsNull() || prior.IsUnknown() {
		return types.BoolNull()
	}
	if server != nil && *server != prior.ValueBool() {
		return types.BoolValue(*server)
	}
	return prior
}

// keepOrDriftNetwork is keepOrDrift for the network of a node group. A controller that does not report
// a pool network leaves the configured one in the state.
func keepOrDriftNetwork(prior *mksPoolNetworkModel, server *apiv1.MKSNodePoolNetwork, importing bool) *mksPoolNetworkModel {
	if importing {
		if server == nil {
			return nil
		}
		return &mksPoolNetworkModel{VPC: nullableString(server.VPC), Subnet: nullableString(server.Subnet), SecurityGroup: nullableString(server.SecurityGroup)}
	}
	if !networkConfigured(prior) {
		return nil
	}
	if server == nil {
		return prior
	}
	return &mksPoolNetworkModel{
		VPC:           keepOrDrift(prior.VPC, server.VPC, false),
		Subnet:        keepOrDrift(prior.Subnet, server.Subnet, false),
		SecurityGroup: keepOrDrift(prior.SecurityGroup, server.SecurityGroup, false),
	}
}

// keepOrDriftLabels is keepOrDrift for the labels of a pool. The platform may add labels of its own, so
// the configured ones are kept as long as the pool still holds each of them.
func keepOrDriftLabels(prior types.Map, server map[string]string, importing bool) types.Map {
	if importing {
		if len(server) == 0 {
			return types.MapNull(types.StringType)
		}
		return mapFromStringMap(server)
	}
	if prior.IsNull() || prior.IsUnknown() {
		return types.MapNull(types.StringType)
	}
	for k, v := range elementsToStringMap(prior) {
		if sv, ok := server[k]; !ok || sv != v {
			return mapFromStringMap(server)
		}
	}
	return prior
}

// keepOrDriftTaints is keepOrDrift for the taints of a pool, compared as sets.
func keepOrDriftTaints(prior []mksTaintModel, server []any, importing bool) []mksTaintModel {
	got := taintsFromWire(server)
	if importing {
		return got
	}
	if prior == nil {
		return nil
	}
	// The platform may hold taints of its own: the configured ones are kept as long as the pool holds each.
	if containsTaints(got, prior) {
		return prior
	}
	return got
}

// containsTaints reports whether every taint of want is in have.
func containsTaints(have, want []mksTaintModel) bool {
	held := map[[3]string]bool{}
	for _, t := range have {
		held[taintKey(t.Key.ValueString(), t.Value.ValueString(), t.Effect.ValueString())] = true
	}
	for _, t := range want {
		if !held[taintKey(t.Key.ValueString(), t.Value.ValueString(), t.Effect.ValueString())] {
			return false
		}
	}
	return true
}

func scalingMode(g *apiv1.MKSNodeGroup, fallback types.String) types.String {
	if g != nil && g.Scaling != nil && g.Scaling.Mode != "" {
		return types.StringValue(g.Scaling.Mode)
	}
	if fallback.IsUnknown() {
		return types.StringNull()
	}
	return fallback
}

func groupName(g *apiv1.MKSNodeGroup, fallback types.String) types.String {
	switch {
	case g != nil && g.Name != "":
		return types.StringValue(g.Name)
	case g != nil && g.DisplayName != "":
		return types.StringValue(g.DisplayName)
	case fallback.IsUnknown():
		return types.StringNull()
	}
	return fallback
}

// mksClusterModelFromServer maps the API cluster to Terraform state. base holds the configured
// values; fromRead is true when refreshing existing state. nodes may be nil when they could not be read.
func mksClusterModelFromServer(base mksResourceModel, srv *apiv1.MKSCluster, nodes *apiv1.MKSNodeList, fromRead bool) mksResourceModel {
	out := base
	importing := base.Spec.ProfileName.IsNull() || base.Spec.ProfileName.IsUnknown()
	out.Metadata = metadataFromServer(base.Metadata, srv.Metadata, fromRead)
	out.ID = types.StringValue(mksClusterID(out.Metadata.Project.ValueString(), out.Metadata.Workspace.ValueString(), srv.Metadata.Name))

	s := srv.Spec
	spec := base.Spec
	if s.Profile != nil && s.Profile.Name != "" {
		spec.ProfileName = types.StringValue(s.Profile.Name)
	}
	switch {
	case set(base.Spec.KubernetesVersion) && sameK8sVersion(base.Spec.KubernetesVersion.ValueString(), s.KubernetesVersion):
		spec.KubernetesVersion = base.Spec.KubernetesVersion
	case s.KubernetesVersion != "":
		spec.KubernetesVersion = types.StringValue(s.KubernetesVersion)
	}
	if s.Location != "" {
		spec.Location = types.StringValue(s.Location)
	} else if spec.Location.IsUnknown() {
		spec.Location = types.StringNull()
	}
	spec.Blueprint = nil
	if s.Blueprint != nil {
		spec.Blueprint = &mksBlueprintModel{Name: nullableString(s.Blueprint.Name), Version: nullableString(s.Blueprint.Version)}
	}
	spec.Networking = nil
	if n := s.Networking; n != nil {
		var cfgVPC, cfgSubnet types.String
		if base.Spec.Networking != nil {
			cfgVPC, cfgSubnet = base.Spec.Networking.VPC, base.Spec.Networking.Subnet
		}
		m := &mksNetworkingModel{VPC: keepIfServerEmpty(cfgVPC, n.VPC), Subnet: keepIfServerEmpty(cfgSubnet, n.Subnet), PodSubnet: nullableString(n.PodSubnet), ServiceSubnet: nullableString(n.ServiceSubnet), CNIName: types.StringNull(), CNIVersion: types.StringNull()}
		if n.CNI != nil {
			m.CNIName, m.CNIVersion = nullableString(n.CNI.Name), nullableString(n.CNI.Version)
		}
		spec.Networking = m
	}

	spec.ControlPlane = nil
	if cp := s.ControlPlane; cp != nil {
		var prior mksControlPlaneModel
		if base.Spec.ControlPlane != nil {
			prior = *base.Spec.ControlPlane
		}
		spec.ControlPlane = &mksControlPlaneModel{
			Name:           groupName(cp, prior.Name),
			Datacenter:     keepOrDrift(prior.Datacenter, cp.Datacenter, importing),
			OSImage:        keepOrDrift(prior.OSImage, cp.OSImage, importing),
			SKUName:        keepOrDrift(prior.SKUName, cp.SKUName, importing),
			SKUType:        keepOrDrift(prior.SKUType, cp.SKUType, importing),
			SSHKey:         keepOrDrift(prior.SSHKey, cp.SSHKey, importing),
			NodeCount:      types.Int64Value(scalingCount(cp)),
			ScalingMode:    scalingMode(cp, prior.ScalingMode),
			ComputeType:    nullableString(cp.ComputeType),
			Network:        keepOrDriftNetwork(prior.Network, cp.Network, importing),
			BootDiskSizeGB: keepOrDriftInt(prior.BootDiskSizeGB, int64(cp.BootDiskSizeGb), importing),
			AssignPublicIP: keepOrDriftBool(prior.AssignPublicIP, cp.AssignPublicIP, importing),
			UserPassword:   keptPassword(prior.UserPassword, importing),
		}
	}

	priorPools := map[string]mksWorkerPoolModel{}
	var order []string
	for _, wp := range base.Spec.WorkerPools {
		priorPools[wp.Name.ValueString()] = wp
		order = append(order, wp.Name.ValueString())
	}
	serverPools := map[string]apiv1.MKSNodeGroup{}
	var extra []string
	for _, wp := range s.WorkerPools {
		serverPools[wp.Name] = wp
		if _, ok := priorPools[wp.Name]; !ok {
			extra = append(extra, wp.Name)
		}
	}
	sort.Strings(extra)
	spec.WorkerPools = nil
	for _, name := range append(order, extra...) {
		sp, ok := serverPools[name]
		if !ok {
			continue // removed outside Terraform: omit it so the next plan adds it back
		}
		prior := priorPools[name]
		pool := mksWorkerPoolModel{
			Name:           types.StringValue(name),
			Datacenter:     keepOrDrift(prior.Datacenter, sp.Datacenter, importing),
			OSImage:        keepOrDrift(prior.OSImage, sp.OSImage, importing),
			SKUName:        keepOrDrift(prior.SKUName, sp.SKUName, importing),
			SKUType:        keepOrDrift(prior.SKUType, sp.SKUType, importing),
			SSHKey:         keepOrDrift(prior.SSHKey, sp.SSHKey, importing),
			NodeCount:      types.Int64Value(scalingCount(&sp)),
			ScalingMode:    scalingMode(&sp, prior.ScalingMode),
			MinNodes:       types.Int64Null(),
			MaxNodes:       types.Int64Null(),
			Network:        keepOrDriftNetwork(prior.Network, sp.Network, importing),
			BootDiskSizeGB: keepOrDriftInt(prior.BootDiskSizeGB, int64(sp.BootDiskSizeGb), importing),
			AssignPublicIP: keepOrDriftBool(prior.AssignPublicIP, sp.AssignPublicIP, importing),
			UserPassword:   keptPassword(prior.UserPassword, importing),
			Labels:         keepOrDriftLabels(prior.Labels, sp.Labels, importing),
			Annotations:    keepOrDriftLabels(prior.Annotations, sp.Annotations, importing),
			Taints:         keepOrDriftTaints(prior.Taints, sp.Taints, importing),
		}
		if sc := sp.Scaling; sc != nil && isAutomatic(sc.Mode) {
			pool.MinNodes, pool.MaxNodes = types.Int64Value(int64(sc.Min)), types.Int64Value(int64(sc.Max))
			// node_count is the initial size: the autoscaler moves the size inside the bounds, and that is
			// not a change to configure away. A size outside the bounds is reported as it is.
			if !importing && !prior.NodeCount.IsNull() && !prior.NodeCount.IsUnknown() && (sc.Desired == 0 || sc.Desired >= sc.Min && sc.Desired <= sc.Max) {
				pool.NodeCount = prior.NodeCount
			}
		}
		spec.WorkerPools = append(spec.WorkerPools, pool)
	}
	// `worker_pools = []` is an empty list, not null: keep it empty so the state matches the plan.
	if spec.WorkerPools == nil && base.Spec.WorkerPools != nil {
		spec.WorkerPools = []mksWorkerPoolModel{}
	}

	// The configured tags are kept as long as the cluster holds each of them; tags set elsewhere stay out of the state.
	// An empty configured map stays an empty map.
	spec.Tags = keepOrDriftLabels(base.Spec.Tags, s.Tags, importing)
	spec.ContractTerm = types.StringNull()
	if s.Billing != nil {
		if ct, ok := s.Billing.Variables["ContractTerm"]; ok {
			spec.ContractTerm = types.StringValue(ct)
		}
	}
	out.Spec = spec

	st := srv.Status
	out.Status = mksStatusModel{
		Phase:             types.StringValue(st.Phase()),
		Status:            types.StringValue(st.Status),
		Reason:            types.StringValue(st.Reason),
		Action:            types.StringValue(st.Action),
		ClusterID:         types.StringNull(),
		KubernetesVersion: nullableString(s.KubernetesVersion),
		NodeCount:         types.Int64Null(),
	}
	if st.Output != nil {
		out.Status.ClusterID = nullableString(st.Output.ClusterIDEdgesrv)
	}
	for _, v := range st.AvailableKubernetesVersions {
		out.Status.AvailableVersions = append(out.Status.AvailableVersions, types.StringValue(v))
	}
	if nodes != nil {
		out.Status.NodeCount = types.Int64Value(int64(len(nodes.Items)))
		for _, n := range nodes.Items {
			out.Status.Nodes = append(out.Status.Nodes, mksNodeModel{
				Hostname:          types.StringValue(firstNonEmpty(n.Spec.Hostname, n.Metadata.Name)),
				NodePool:          nullableString(firstNonEmpty(n.NodePool(), n.Spec.NodePool)),
				KubernetesVersion: nullableString(n.Spec.KubeVersion),
				PrivateIP:         nullableString(n.Spec.PrivateIP),
				Roles:             listFromStringSlice(n.Spec.Roles),
				OperatingSystem:   nullableString(n.Spec.OperatingSystem),
				Arch:              nullableString(n.Spec.Arch),
				Status:            nullableString(n.Status.EdgeStatus),
			})
		}
	} else if fromRead {
		// The node list could not be read: keep what the state already holds instead of flipping it
		// to null on a transient error.
		out.Status.NodeCount = base.Status.NodeCount
		out.Status.Nodes = base.Status.Nodes
	}
	return out
}

// expectedNodes is how many nodes the spec asks for.
func expectedNodes(spec apiv1.MKSClusterSpec) int {
	n := int(scalingCount(spec.ControlPlane))
	for i := range spec.WorkerPools {
		n += int(scalingCount(&spec.WorkerPools[i]))
	}
	return n
}

// The API requires pod and service CIDRs, but not every profile defines defaults for them. These
// are the Kubernetes standard ranges, also the defaults of the profiles that do define them.
const (
	fallbackPodSubnet     = "10.244.0.0/16"
	fallbackServiceSubnet = "10.96.0.0/12"
)

// fillProfileDefaults sets the fields the user left out from the profile's defaults. The API
// does not do this itself (it rejects a create without, for example, a CNI), so the provider
// does what the Developer Hub does: pick the profile's default for every unset value. Fields
// the user configured are never changed. keyExists tells whether an SSH key exists in the
// cluster's workspace (nil means "assume it does"). On an update, current is the cluster the API
// holds: groups it already has are left as they are, because filling them would send values the
// API does not hold (a datacenter on a group that takes it from the cluster) and turn a
// no-change apply into a write. Pass nil on create.
func fillProfileDefaults(spec *apiv1.MKSClusterSpec, p *apiv1.MKSClusterProfile, keyExists func(name string) bool, current *apiv1.MKSClusterSpec) {
	if p == nil {
		return
	}
	ps := p.Spec
	if spec.KubernetesVersion == "" {
		spec.KubernetesVersion = ps.DefaultKubernetesVersion
	}
	if spec.Location == "" && len(ps.Datacenters) == 1 {
		spec.Location = ps.Datacenters[0]
	}
	if spec.Blueprint == nil && ps.DefaultBlueprint != nil {
		bp := *ps.DefaultBlueprint
		spec.Blueprint = &bp
	} else if spec.Blueprint != nil && ps.DefaultBlueprint != nil {
		if spec.Blueprint.Name == "" {
			spec.Blueprint.Name = ps.DefaultBlueprint.Name
		}
		if spec.Blueprint.Version == "" {
			spec.Blueprint.Version = ps.DefaultBlueprint.Version
		}
	}
	nw := apiv1.MKSNetworking{}
	if spec.Networking != nil {
		nw = *spec.Networking
	}
	if nw.PodSubnet == "" {
		nw.PodSubnet = firstNonEmpty(ps.DefaultPodSubnet, fallbackPodSubnet)
	}
	if nw.ServiceSubnet == "" {
		nw.ServiceSubnet = firstNonEmpty(ps.DefaultServiceSubnet, fallbackServiceSubnet)
	}
	if ps.DefaultCNI != nil {
		cni := apiv1.MKSCNI{}
		if nw.CNI != nil {
			cni = *nw.CNI
		}
		if cni.Name == "" {
			cni.Name = ps.DefaultCNI.Name
		}
		if cni.Version == "" {
			cni.Version = versionFor(ps, cni.Name, ps.DefaultCNI.Version)
		}
		nw.CNI = &cni
	}
	spec.Networking = &nw

	fill := func(g *apiv1.MKSNodeGroup, allowed *apiv1.MKSGroupProfile) {
		if g.Datacenter == "" {
			g.Datacenter = spec.Location
		}
		if allowed == nil {
			return
		}
		if g.OSImage == "" {
			g.OSImage = allowed.DefaultImage
		}
		if g.SKUName == "" {
			g.SKUName = allowed.DefaultSKUName
		}
		if g.SKUType == "" && len(allowed.SKUTypes) == 1 {
			g.SKUType = allowed.SKUTypes[0]
		}
		// SSH keys belong to a workspace, so the profile's default key may not exist where the
		// cluster is created; only use it when it does.
		if g.SSHKey == "" && ps.DefaultSSHKey != "" && (keyExists == nil || keyExists(ps.DefaultSSHKey)) {
			g.SSHKey = ps.DefaultSSHKey
		}
	}
	if spec.ControlPlane != nil && (current == nil || current.ControlPlane == nil) {
		fill(spec.ControlPlane, ps.ControlPlane)
	}
	held := map[string]bool{}
	if current != nil {
		for _, wp := range current.WorkerPools {
			held[wp.Name] = true
		}
	}
	for i := range spec.WorkerPools {
		if !held[spec.WorkerPools[i].Name] {
			fill(&spec.WorkerPools[i], ps.Workers)
		}
	}
}

// versionFor returns the version of the named CNI the profile supports, or fallback.
func versionFor(ps apiv1.MKSClusterProfileSpec, name, fallback string) string {
	for _, c := range ps.SupportedCNIs {
		if strings.EqualFold(c.Name, name) {
			return c.Version
		}
	}
	return fallback
}

// sameRequest reports whether sending want would change nothing the platform holds in current. It
// keeps client-side changes (timeouts, force_destroy) from re-applying a cluster, which would
// start a redeploy for no reason.
func sameRequest(want, current *apiv1.MKSCluster) bool {
	ws, errW := json.Marshal(withoutServerOnly(want.Spec))
	cs, errC := json.Marshal(withoutServerOnly(current.Spec))
	if errW != nil || errC != nil || string(ws) != string(cs) {
		return false
	}
	w, c := want.Metadata, current.Metadata
	return w.DisplayName == c.DisplayName && w.Description == c.Description && sameStringMap(w.Labels, c.Labels) && sameStringMap(w.Annotations, c.Annotations)
}

// withoutServerOnly drops the spec fields that the platform reports but refuses in a request
// (platformVersion), so that they never make an unchanged cluster look changed. The input is not changed.
func withoutServerOnly(spec apiv1.MKSClusterSpec) apiv1.MKSClusterSpec {
	if _, ok := spec.Extra["platformVersion"]; ok {
		extra := make(map[string]json.RawMessage, len(spec.Extra))
		for k, v := range spec.Extra {
			if k != "platformVersion" {
				extra[k] = v
			}
		}
		spec.Extra = extra
	}
	if spec.ControlPlane != nil {
		cp := normalizedGroup(*spec.ControlPlane)
		spec.ControlPlane = &cp
	}
	if len(spec.WorkerPools) > 0 {
		pools := make([]apiv1.MKSNodeGroup, len(spec.WorkerPools))
		for i, wp := range spec.WorkerPools {
			pools[i] = normalizedGroup(wp)
		}
		spec.WorkerPools = pools
	}
	return spec
}

// normalizedGroup writes a node group the one way the comparison needs: an omitted public-IP flag is false,
// the order of the taints does not matter, and an empty taint value is no value. The input is not changed.
func normalizedGroup(g apiv1.MKSNodeGroup) apiv1.MKSNodeGroup {
	if g.AssignPublicIP == nil {
		off := false
		g.AssignPublicIP = &off
	}
	if len(g.Taints) > 0 {
		taints := make([]any, 0, len(g.Taints))
		for _, e := range g.Taints {
			if m, ok := e.(map[string]any); ok {
				c := make(map[string]any, len(m))
				for k, v := range m {
					if s, isString := v.(string); k == "value" && isString && s == "" {
						continue
					}
					c[k] = v
				}
				e = c
			}
			taints = append(taints, e)
		}
		sort.Slice(taints, func(i, j int) bool {
			a, _ := json.Marshal(taints[i])
			b, _ := json.Marshal(taints[j])
			return string(a) < string(b)
		})
		g.Taints = taints
	}
	return g
}

func sameStringMap(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if bv, ok := b[k]; !ok || bv != v {
			return false
		}
	}
	return true
}
