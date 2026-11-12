package resources

import (
	"encoding/json"
	"strings"
	"testing"

	apiv1 "github.com/gpupaas-ai/gpupaas-go/apis/v1alpha1"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Per-pool settings of the v3.1.41 API reference: network, boot disk, public IP, password, labels, taints
// and automatic scaling.

func netModel(vpc, subnet, sg string) *mksPoolNetworkModel {
	return &mksPoolNetworkModel{VPC: str(vpc), Subnet: str(subnet), SecurityGroup: nullableString(sg)}
}

func labelMap(kv map[string]string) types.Map { return mapFromStringMap(kv) }

func taint(k, v, e string) mksTaintModel {
	return mksTaintModel{Key: str(k), Value: nullableString(v), Effect: str(e)}
}

func ptr[T any](v T) *T { return &v }

// poolPlan is a worker pool with every new setting configured.
func poolPlan() mksWorkerPoolModel {
	return mksWorkerPoolModel{
		Name: str("gpu"), NodeCount: types.Int64Value(2), ScalingMode: str("manual"),
		MinNodes: types.Int64Null(), MaxNodes: types.Int64Null(),
		Network:        netModel("vpc-a", "sub-1", "sg-1"),
		BootDiskSizeGB: types.Int64Value(60), AssignPublicIP: types.BoolValue(false), UserPassword: str("s3cret"),
		Labels: labelMap(map[string]string{"team": "ml"}),
		Taints: []mksTaintModel{taint("nvidia.com/gpu", "true", "NoSchedule")},
	}
}

func TestBuildMKSClusterCreateSendsEveryPoolSetting(t *testing.T) {
	plan := plannedModel()
	plan.Spec.ControlPlane.Network = netModel("vpc-a", "sub-0", "")
	plan.Spec.ControlPlane.UserPassword = str("cp-secret")
	plan.Spec.WorkerPools = []mksWorkerPoolModel{poolPlan()}
	spec := buildMKSCluster(plan, nil).Spec

	cp := spec.ControlPlane
	if cp.Network == nil || *cp.Network != (apiv1.MKSNodePoolNetwork{VPC: "vpc-a", Subnet: "sub-0"}) || cp.UserPassword != "cp-secret" {
		t.Errorf("control plane %+v", cp)
	}
	w := spec.WorkerPools[0]
	if w.Network == nil || *w.Network != (apiv1.MKSNodePoolNetwork{VPC: "vpc-a", Subnet: "sub-1", SecurityGroup: "sg-1"}) {
		t.Errorf("network %+v", w.Network)
	}
	if w.BootDiskSizeGb != 60 || w.AssignPublicIP == nil || *w.AssignPublicIP || w.UserPassword != "s3cret" {
		t.Errorf("pool %+v", w)
	}
	if w.Labels["team"] != "ml" || len(w.Taints) != 1 {
		t.Errorf("labels %v taints %v", w.Labels, w.Taints)
	}
	got, _ := json.Marshal(w.Taints)
	if string(got) != `[{"effect":"NoSchedule","key":"nvidia.com/gpu","value":"true"}]` {
		t.Errorf("taints on the wire: %s", got)
	}
	if w.Scaling == nil || w.Scaling.Mode != "manual" || w.Scaling.Count != 2 || w.Scaling.Min != 0 || w.Scaling.Max != 0 {
		t.Errorf("scaling %+v", w.Scaling)
	}
}

// A read returns the password as "******". The provider keeps what the user configured and sends it only
// when it is new or changed; sending the configured one on every update would redeploy the cluster.
func TestPasswordIsSentOnlyWhenNewOrChanged(t *testing.T) {
	cur := serverCluster()
	cur.Spec.WorkerPools = []apiv1.MKSNodeGroup{{Name: "gpu", UserPassword: "******", Scaling: &apiv1.MKSScaling{Mode: "manual", Count: 2}}}
	pool := mksWorkerPoolModel{Name: str("gpu"), NodeCount: types.Int64Value(2), ScalingMode: str("manual"), UserPassword: str("s3cret"), Labels: types.MapNull(types.StringType)}
	plan := plannedModel()
	plan.Spec.ControlPlane.NodeCount = types.Int64Value(1)
	plan.Spec.WorkerPools = []mksWorkerPoolModel{pool}
	prior := plan.Spec

	got := buildMKSClusterFrom(plan, cur, &prior)
	if p := got.Spec.WorkerPools[0].UserPassword; p != "******" {
		t.Fatalf("an unchanged password was sent again: %q", p)
	}
	if !sameRequest(got, cur) {
		t.Fatal("an unchanged password makes the cluster look changed (a redeploy on every apply)")
	}

	changed := plan
	changed.Spec.WorkerPools = []mksWorkerPoolModel{pool}
	changed.Spec.WorkerPools[0].UserPassword = str("new-one")
	if p := buildMKSClusterFrom(changed, cur, &prior).Spec.WorkerPools[0].UserPassword; p != "new-one" {
		t.Fatalf("a changed password was not sent: %q", p)
	}

	added := plan
	added.Spec.WorkerPools = []mksWorkerPoolModel{pool, {Name: str("extra"), NodeCount: types.Int64Value(1), ScalingMode: str("manual"), UserPassword: str("s3cret"), Labels: types.MapNull(types.StringType)}}
	if p := buildMKSClusterFrom(added, cur, &prior).Spec.WorkerPools[1].UserPassword; p != "s3cret" {
		t.Fatalf("a new pool must get its password: %q", p)
	}
}

func TestPoolModelKeepsConfiguredValuesAndHidesWhatWasNotConfigured(t *testing.T) {
	plan := plannedModel()
	plan.Spec.WorkerPools = []mksWorkerPoolModel{poolPlan()}
	srv := serverCluster()
	srv.Spec.WorkerPools = []apiv1.MKSNodeGroup{{
		Name: "gpu", SKUName: "bm-1", OSImage: "ubuntu-26", Scaling: &apiv1.MKSScaling{Mode: "manual", Count: 2},
		Network: &apiv1.MKSNodePoolNetwork{VPC: "vpc-a", Subnet: "sub-1", SecurityGroup: "sg-1"}, BootDiskSizeGb: 60, AssignPublicIP: ptr(false), UserPassword: "******",
		Labels: map[string]string{"team": "ml", "platform/owned": "yes"}, Taints: []any{map[string]any{"key": "nvidia.com/gpu", "value": "true", "effect": "NoSchedule"}},
	}}
	srv.Spec.ControlPlane.Network = &apiv1.MKSNodePoolNetwork{VPC: "vpc-a", Subnet: "sub-0"}
	srv.Spec.ControlPlane.BootDiskSizeGb = 40
	srv.Spec.ControlPlane.AssignPublicIP = ptr(true)

	got := mksClusterModelFromServer(plan, srv, nil, true).Spec
	w := got.WorkerPools[0]
	if !networkConfigured(w.Network) || w.Network.Subnet.ValueString() != "sub-1" || w.BootDiskSizeGB.ValueInt64() != 60 || w.AssignPublicIP.ValueBool() {
		t.Errorf("configured values lost: %+v", w)
	}
	if w.UserPassword.ValueString() != "s3cret" {
		t.Errorf("the configured password must stay in the state, got %v", w.UserPassword)
	}
	if !w.Labels.Equal(labelMap(map[string]string{"team": "ml"})) {
		t.Errorf("a label the platform added must not show as a change: %v", w.Labels)
	}
	if len(w.Taints) != 1 {
		t.Errorf("taints %v", w.Taints)
	}
	// The control plane was configured without any of these: whatever the platform holds stays hidden.
	cp := got.ControlPlane
	if cp.Network != nil || !cp.BootDiskSizeGB.IsNull() || !cp.AssignPublicIP.IsNull() || !cp.UserPassword.IsNull() {
		t.Errorf("server defaults leaked into the state: %+v", cp)
	}
}

func TestPoolModelReportsDriftOfConfiguredValues(t *testing.T) {
	plan := plannedModel()
	plan.Spec.WorkerPools = []mksWorkerPoolModel{poolPlan()}
	srv := serverCluster()
	srv.Spec.WorkerPools = []apiv1.MKSNodeGroup{{
		Name: "gpu", Scaling: &apiv1.MKSScaling{Mode: "manual", Count: 2},
		Network: &apiv1.MKSNodePoolNetwork{VPC: "vpc-a", Subnet: "moved", SecurityGroup: "sg-2"}, BootDiskSizeGb: 80, AssignPublicIP: ptr(true),
		Labels: map[string]string{"team": "other"}, Taints: []any{map[string]any{"key": "x", "effect": "NoExecute"}},
	}}
	w := mksClusterModelFromServer(plan, srv, nil, true).Spec.WorkerPools[0]
	if w.Network.Subnet.ValueString() != "moved" || w.Network.SecurityGroup.ValueString() != "sg-2" || w.BootDiskSizeGB.ValueInt64() != 80 || !w.AssignPublicIP.ValueBool() {
		t.Errorf("drift not reported: %+v %+v", w, w.Network)
	}
	if !w.Labels.Equal(labelMap(map[string]string{"team": "other"})) || len(w.Taints) != 1 || w.Taints[0].Key.ValueString() != "x" {
		t.Errorf("labels %v taints %v", w.Labels, w.Taints)
	}
}

func TestPoolNetworkStaysWhenTheControllerDoesNotReportOne(t *testing.T) {
	plan := plannedModel()
	plan.Spec.WorkerPools = []mksWorkerPoolModel{poolPlan()}
	srv := serverCluster()
	srv.Spec.WorkerPools = []apiv1.MKSNodeGroup{{Name: "gpu", Scaling: &apiv1.MKSScaling{Mode: "manual", Count: 2}}}
	w := mksClusterModelFromServer(plan, srv, nil, true).Spec.WorkerPools[0]
	if !networkConfigured(w.Network) || w.Network.VPC.ValueString() != "vpc-a" {
		t.Errorf("an older controller that ignores the pool network must not make it drift: %+v", w.Network)
	}
}

func TestImportTakesThePoolSettingsFromTheServerAndNeverThePassword(t *testing.T) {
	srv := serverCluster()
	srv.Spec.WorkerPools = []apiv1.MKSNodeGroup{{
		Name: "gpu", SKUName: "bm-1", Scaling: &apiv1.MKSScaling{Mode: "manual", Count: 2},
		Network: &apiv1.MKSNodePoolNetwork{VPC: "vpc-a", Subnet: "sub-1"}, BootDiskSizeGb: 60, AssignPublicIP: ptr(false), UserPassword: "******",
		Labels: map[string]string{"team": "ml"}, Taints: []any{map[string]any{"key": "k", "value": "v", "effect": "NoSchedule"}},
	}}
	w := mksClusterModelFromServer(mksResourceModel{}, srv, nil, false).Spec.WorkerPools[0]
	if w.Network == nil || w.Network.VPC.ValueString() != "vpc-a" || w.BootDiskSizeGB.ValueInt64() != 60 || w.AssignPublicIP.IsNull() || w.AssignPublicIP.ValueBool() {
		t.Errorf("import lost the pool settings: %+v", w)
	}
	if !w.UserPassword.IsNull() {
		t.Error("an import must not invent a password")
	}
	if len(w.Labels.Elements()) != 1 || len(w.Taints) != 1 {
		t.Errorf("labels %v taints %v", w.Labels, w.Taints)
	}
}

func TestRemovedLabelsAndTaintsAreClearedAndUntouchedOnesAreKept(t *testing.T) {
	cur := serverCluster()
	cur.Spec.WorkerPools = []apiv1.MKSNodeGroup{{
		Name: "a", Scaling: &apiv1.MKSScaling{Mode: "manual", Count: 3},
		Labels: map[string]string{"team": "ml"}, Taints: []any{map[string]any{"key": "k", "effect": "NoSchedule"}},
	}}
	prior := mksSpecModel{WorkerPools: []mksWorkerPoolModel{{Name: str("a"), Labels: labelMap(map[string]string{"team": "ml"}), Taints: []mksTaintModel{taint("k", "", "NoSchedule")}}}}

	plan := plannedModel() // pool "a" without labels and taints: they were removed from the configuration
	got := buildMKSClusterFrom(plan, cur, &prior).Spec.WorkerPools[0]
	if got.Labels != nil || got.Taints != nil {
		t.Errorf("removed labels/taints were kept: %v %v", got.Labels, got.Taints)
	}

	// Never configured: whatever the platform holds is left alone.
	got = buildMKSClusterFrom(plan, cur, &mksSpecModel{WorkerPools: []mksWorkerPoolModel{{Name: str("a")}}}).Spec.WorkerPools[0]
	if got.Labels["team"] != "ml" || len(got.Taints) != 1 {
		t.Errorf("labels/taints the user never managed were dropped: %v %v", got.Labels, got.Taints)
	}
}

func TestTaintsAreComparedAsSets(t *testing.T) {
	a := []mksTaintModel{taint("a", "1", "NoSchedule"), taint("b", "", "NoExecute")}
	b := []mksTaintModel{taint("b", "", "NoExecute"), taint("a", "1", "NoSchedule")}
	if !sameTaints(a, b) {
		t.Error("the order of taints is not a change")
	}
	if sameTaints(a, []mksTaintModel{taint("a", "1", "NoSchedule"), taint("b", "", "NoSchedule")}) {
		t.Error("another effect is a change")
	}
	if sameTaints(a, a[:1]) {
		t.Error("a missing taint is a change")
	}
	got := keepOrDriftTaints(a, []any{map[string]any{"key": "b", "effect": "NoExecute"}, map[string]any{"key": "a", "value": "1", "effect": "NoSchedule"}}, false)
	if len(got) != 2 || got[0].Key.ValueString() != "a" {
		t.Errorf("a reordered list must keep the configured order: %v", got)
	}
}

func autoPool(min, max, desired int64) mksWorkerPoolModel {
	return mksWorkerPoolModel{Name: str("a"), NodeCount: types.Int64Value(desired), ScalingMode: str("automatic"), MinNodes: types.Int64Value(min), MaxNodes: types.Int64Value(max), Labels: types.MapNull(types.StringType)}
}

func TestAutomaticScalingIsSentWithBoundsAndTheAutoscalerKeepsTheSize(t *testing.T) {
	plan := plannedModel()
	plan.Spec.WorkerPools = []mksWorkerPoolModel{autoPool(1, 5, 2)}

	created := buildMKSCluster(plan, nil).Spec.WorkerPools[0].Scaling
	if created.Mode != "automatic" || created.Min != 1 || created.Max != 5 || created.Desired != 2 || created.Count != 0 {
		t.Fatalf("create: %+v", created)
	}

	// The autoscaler has moved the pool to 4: an update that changes something else must not resize it.
	cur := serverCluster()
	cur.Spec.WorkerPools = []apiv1.MKSNodeGroup{{Name: "a", Scaling: &apiv1.MKSScaling{Mode: "automatic", Min: 1, Max: 5, Desired: 4}}}
	if sc := buildMKSCluster(plan, cur).Spec.WorkerPools[0].Scaling; sc.Desired != 4 {
		t.Errorf("update reset the autoscaler's size: %+v", sc)
	}
	if !sameRequest(buildMKSCluster(plan, cur), cur) {
		t.Error("an automatic pool the autoscaler resized must not look changed")
	}
	// Bounds that no longer hold the current size fall back to the configured size.
	plan.Spec.WorkerPools = []mksWorkerPoolModel{autoPool(1, 3, 2)}
	if sc := buildMKSCluster(plan, cur).Spec.WorkerPools[0].Scaling; sc.Desired != 2 || sc.Max != 3 {
		t.Errorf("bounds change: %+v", sc)
	}

	// The state keeps node_count while the size is inside the bounds, and follows the platform outside them.
	plan.Spec.WorkerPools = []mksWorkerPoolModel{autoPool(1, 5, 2)}
	w := mksClusterModelFromServer(plan, cur, nil, true).Spec.WorkerPools[0]
	if w.NodeCount.ValueInt64() != 2 || w.MinNodes.ValueInt64() != 1 || w.MaxNodes.ValueInt64() != 5 || w.ScalingMode.ValueString() != "automatic" {
		t.Errorf("state %+v", w)
	}
	cur.Spec.WorkerPools[0].Scaling.Desired = 9
	if w = mksClusterModelFromServer(plan, cur, nil, true).Spec.WorkerPools[0]; w.NodeCount.ValueInt64() != 9 {
		t.Errorf("a size outside the bounds is drift: %+v", w)
	}
}

func TestSwitchingTheScalingModeClearsTheOtherModesFields(t *testing.T) {
	cur := serverCluster() // pool "a" is manual with count 2
	plan := plannedModel()
	plan.Spec.WorkerPools = []mksWorkerPoolModel{autoPool(1, 4, 3)}
	if sc := buildMKSCluster(plan, cur).Spec.WorkerPools[0].Scaling; sc.Count != 0 || sc.Min != 1 || sc.Max != 4 || sc.Desired != 3 {
		t.Errorf("manual to automatic: %+v", sc)
	}
	cur.Spec.WorkerPools[0].Scaling = &apiv1.MKSScaling{Mode: "automatic", Min: 1, Max: 4, Desired: 3}
	plan = plannedModel() // back to manual, 3 nodes
	if sc := buildMKSCluster(plan, cur).Spec.WorkerPools[0].Scaling; sc.Mode != "manual" || sc.Count != 3 || sc.Min != 0 || sc.Max != 0 || sc.Desired != 0 {
		t.Errorf("automatic to manual: %+v", sc)
	}
}

func TestExpectedNodesCountsTheCurrentSizeOfAnAutomaticPool(t *testing.T) {
	spec := apiv1.MKSClusterSpec{
		ControlPlane: &apiv1.MKSNodeGroup{Scaling: &apiv1.MKSScaling{Mode: "manual", Count: 1}},
		WorkerPools:  []apiv1.MKSNodeGroup{{Scaling: &apiv1.MKSScaling{Mode: "automatic", Min: 1, Max: 5, Desired: 3}}, {Scaling: &apiv1.MKSScaling{Mode: "manual", Count: 2}}},
	}
	if n := expectedNodes(spec); n != 6 {
		t.Errorf("expectedNodes = %d, want 6", n)
	}
}

// The platform resolves platformVersion itself and refuses a request that carries it.
func TestPlatformVersionIsNeverSentAndNeverCountsAsAChange(t *testing.T) {
	cur := serverCluster()
	cur.Spec.Extra["platformVersion"] = json.RawMessage(`"v3.1.42"`)
	got := buildMKSCluster(plannedModel(), cur)
	if _, ok := got.Spec.Extra["platformVersion"]; ok {
		t.Fatal("platformVersion was sent back")
	}
	if _, ok := cur.Spec.Extra["platformVersion"]; !ok {
		t.Fatal("the cluster read from the API was changed")
	}
	same := buildMKSCluster(plannedModel(), cur)
	same.Spec.KubernetesVersion = cur.Spec.KubernetesVersion
	same.Spec.WorkerPools = cur.Spec.WorkerPools
	same.Spec.ControlPlane = cur.Spec.ControlPlane
	if !sameRequest(same, cur) {
		t.Fatal("platformVersion makes an unchanged cluster look changed")
	}
}

func TestStatusListsTheUpgradeTargets(t *testing.T) {
	srv := serverCluster()
	srv.Status.AvailableKubernetesVersions = []string{"1.37"}
	got := mksClusterModelFromServer(plannedModel(), srv, nil, true).Status.AvailableVersions
	if len(got) != 1 || got[0].ValueString() != "1.37" {
		t.Errorf("available versions %v", got)
	}
	if v := mksClusterModelFromServer(plannedModel(), serverCluster(), nil, true).Status.AvailableVersions; v != nil {
		t.Errorf("no targets must stay null, got %v", v)
	}
}

// --- plan-time checks -----------------------------------------------------

func shapeErrors(plan mksResourceModel) string {
	var d diag.Diagnostics
	validateMKSShape(plan, &d)
	var out []string
	for _, e := range d.Errors() {
		out = append(out, e.Summary()+": "+e.Detail())
	}
	return strings.Join(out, "\n")
}

func TestShapeChecksOfThePoolSettings(t *testing.T) {
	with := func(mod func(*mksResourceModel)) mksResourceModel {
		p := plannedModel()
		p.Spec.WorkerPools = []mksWorkerPoolModel{poolPlan()}
		mod(&p)
		return p
	}
	cases := []struct {
		name string
		plan mksResourceModel
		want string // "" means no error
	}{
		{"valid pool", with(func(*mksResourceModel) {}), ""},
		{"valid automatic pool", with(func(p *mksResourceModel) { p.Spec.WorkerPools[0] = autoPool(1, 5, 2) }), ""},
		{"min without automatic", with(func(p *mksResourceModel) { p.Spec.WorkerPools[0].MinNodes = types.Int64Value(1) }), "min_nodes and max_nodes need automatic scaling"},
		{"automatic without bounds", with(func(p *mksResourceModel) {
			p.Spec.WorkerPools[0].ScalingMode = str("automatic")
		}), "Automatic scaling needs bounds"},
		{"min above max", with(func(p *mksResourceModel) { p.Spec.WorkerPools[0] = autoPool(5, 2, 3) }), "larger than max_nodes"},
		{"max below one", with(func(p *mksResourceModel) { p.Spec.WorkerPools[0] = autoPool(0, 0, 0) }), "max_nodes >= 1"},
		{"start outside bounds", with(func(p *mksResourceModel) { p.Spec.WorkerPools[0] = autoPool(1, 3, 7) }), "must lie between"},
		{"unknown mode", with(func(p *mksResourceModel) { p.Spec.WorkerPools[0].ScalingMode = str("auto") }), "must be \"manual\" or \"automatic\""},
		{"automatic control plane", with(func(p *mksResourceModel) { p.Spec.ControlPlane.ScalingMode = str("automatic") }), "always \"manual\""},
		{"taint effect", with(func(p *mksResourceModel) {
			p.Spec.WorkerPools[0].Taints = []mksTaintModel{taint("k", "v", "Sometimes")}
		}), "NoSchedule, PreferNoSchedule or NoExecute"},
		{"empty taint key", with(func(p *mksResourceModel) {
			p.Spec.WorkerPools[0].Taints = []mksTaintModel{taint("", "v", "NoSchedule")}
		}), "needs a key"},
		{"empty subnet", with(func(p *mksResourceModel) {
			p.Spec.WorkerPools[0].Network = netModel("v", "", "")
			p.Spec.WorkerPools[0].Network.Subnet = str("")
		}), "names both a VPC and a subnet"},
		{"boot disk zero", with(func(p *mksResourceModel) { p.Spec.WorkerPools[0].BootDiskSizeGB = types.Int64Value(0) }), "at least 1"},
		{"public IPs disagree", with(func(p *mksResourceModel) {
			second := poolPlan()
			second.Name, second.AssignPublicIP = str("cpu"), types.BoolValue(true)
			p.Spec.WorkerPools = append(p.Spec.WorkerPools, second)
		}), "every worker pool must carry the same value"},
		{"public IPs agree", with(func(p *mksResourceModel) {
			second := poolPlan()
			second.Name = str("cpu")
			p.Spec.WorkerPools = append(p.Spec.WorkerPools, second)
		}), ""},
		{"public IP set on one pool only", with(func(p *mksResourceModel) {
			second := poolPlan()
			second.Name, second.AssignPublicIP = str("cpu"), types.BoolNull()
			p.Spec.WorkerPools = append(p.Spec.WorkerPools, second)
		}), ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := shapeErrors(c.plan)
			if c.want == "" && got != "" {
				t.Fatalf("unexpected error: %s", got)
			}
			if c.want != "" && !strings.Contains(got, c.want) {
				t.Fatalf("want an error mentioning %q, got %q", c.want, got)
			}
		})
	}
}

func TestAPoolNetworkAndBootDiskCannotChangeAfterCreate(t *testing.T) {
	prior := plannedModel()
	prior.Spec.ControlPlane.Network = netModel("v", "c0", "")
	prior.Spec.WorkerPools = []mksWorkerPoolModel{poolPlan()}
	run := func(mod func(*mksResourceModel)) string {
		plan := plannedModel()
		plan.Spec.ControlPlane.Network = netModel("v", "c0", "")
		plan.Spec.WorkerPools = []mksWorkerPoolModel{poolPlan()}
		mod(&plan)
		var d diag.Diagnostics
		validateMKSFixedAfterCreate(plan, prior, &d)
		var out []string
		for _, e := range d.Errors() {
			out = append(out, e.Summary())
		}
		return strings.Join(out, "; ")
	}
	if got := run(func(*mksResourceModel) {}); got != "" {
		t.Errorf("no change: %s", got)
	}
	if got := run(func(p *mksResourceModel) { p.Spec.WorkerPools[0].Network = netModel("vpc-a", "other", "sg-1") }); !strings.Contains(got, "network can't be changed") {
		t.Errorf("subnet change: %q", got)
	}
	if got := run(func(p *mksResourceModel) { p.Spec.WorkerPools[0].Network = netModel("vpc-a", "sub-1", "sg-9") }); !strings.Contains(got, "network can't be changed") {
		t.Errorf("security group change: %q", got)
	}
	if got := run(func(p *mksResourceModel) { p.Spec.WorkerPools[0].Network = netModel("vpc-a", "sub-1", "") }); got != "" {
		t.Errorf("a security group left out keeps the pool's one: %q", got)
	}
	if got := run(func(p *mksResourceModel) { p.Spec.WorkerPools[0].BootDiskSizeGB = types.Int64Value(100) }); !strings.Contains(got, "boot disk can't be changed") {
		t.Errorf("boot disk change: %q", got)
	}
	if got := run(func(p *mksResourceModel) { p.Spec.ControlPlane.Network = netModel("v", "c9", "") }); !strings.Contains(got, "network can't be changed") {
		t.Errorf("control plane network change: %q", got)
	}
	// A pool with a new name is a new pool: it may take any network.
	if got := run(func(p *mksResourceModel) {
		p.Spec.WorkerPools[0].Name = str("renamed")
		p.Spec.WorkerPools[0].Network = netModel("elsewhere", "x", "")
		p.Spec.WorkerPools[0].BootDiskSizeGB = types.Int64Value(10)
	}); got != "" {
		t.Errorf("a renamed pool is a new pool: %q", got)
	}
}

// --- what the platform adds or omits must never look like a change ---------

// serverPool is a pool as a live platform shows it: labels and a taint of its own, taints in another
// order, an empty taint value, no desired size, no assignPublicIp.
func serverWithPlatformPool() *apiv1.MKSCluster {
	cur := serverCluster()
	cur.Spec.WorkerPools = []apiv1.MKSNodeGroup{{
		Name: "gpu", SKUName: "bm-1", OSImage: "ubuntu-26", Scaling: &apiv1.MKSScaling{Mode: "manual", Count: 2},
		Labels: map[string]string{"team": "ml", "platform/owned": "yes"},
		Taints: []any{
			map[string]any{"key": "platform/reserved", "effect": "PreferNoSchedule"},
			map[string]any{"key": "nvidia.com/gpu", "value": "", "effect": "NoSchedule"},
			map[string]any{"key": "b", "value": "2", "effect": "NoExecute"},
		},
		UserPassword: "******",
	}}
	return cur
}

func gpuPlanWithoutPublicIP() mksResourceModel {
	plan := plannedModel()
	pool := poolPlan()
	pool.Network, pool.BootDiskSizeGB, pool.AssignPublicIP = nil, types.Int64Null(), types.BoolValue(false)
	pool.Labels = labelMap(map[string]string{"team": "ml"})
	pool.Taints = []mksTaintModel{taint("b", "2", "NoExecute"), taint("nvidia.com/gpu", "", "NoSchedule")}
	plan.Spec.WorkerPools = []mksWorkerPoolModel{pool}
	return plan
}

func TestPlatformAddedLabelsAndTaintsDoNotMakeAnUnchangedPoolLookChanged(t *testing.T) {
	cur := serverWithPlatformPool()
	plan := gpuPlanWithoutPublicIP()
	prior := plan.Spec // what a normal apply leaves in the state: the configured values only

	got := buildMKSClusterFrom(plan, cur, &prior)
	if !sameRequest(got, cur) {
		a, _ := json.Marshal(got.Spec.WorkerPools)
		b, _ := json.Marshal(cur.Spec.WorkerPools)
		t.Fatalf("an unchanged pool looks changed (a redeploy on every apply):\nwant %s\n got %s", a, b)
	}
	w := mksClusterModelFromServer(plan, cur, nil, true).Spec.WorkerPools[0]
	if !w.Labels.Equal(plan.Spec.WorkerPools[0].Labels) {
		t.Errorf("a label the platform added shows as a change: %v", w.Labels)
	}
	if len(w.Taints) != 2 || w.Taints[0].Key.ValueString() != "b" {
		t.Errorf("taints the platform added or reordered show as a change: %v", w.Taints)
	}
	if !w.AssignPublicIP.ValueBool() == false && w.AssignPublicIP.IsNull() {
		t.Errorf("assign_public_ip = false must stay false when the platform omits it: %v", w.AssignPublicIP)
	}
}

func TestChangingLabelsAndTaintsKeepsWhatThePlatformAdded(t *testing.T) {
	cur := serverWithPlatformPool()
	plan := gpuPlanWithoutPublicIP()
	prior := gpuPlanWithoutPublicIP().Spec // a separate copy: the pools slice is shared otherwise
	plan.Spec.WorkerPools[0].Labels = labelMap(map[string]string{"team": "ml", "tier": "gpu"})
	plan.Spec.WorkerPools[0].Taints = []mksTaintModel{taint("nvidia.com/gpu", "", "NoSchedule")} // "b" removed

	w := buildMKSClusterFrom(plan, cur, &prior).Spec.WorkerPools[0]
	if w.Labels["platform/owned"] != "yes" || w.Labels["tier"] != "gpu" || w.Labels["team"] != "ml" {
		t.Errorf("labels sent: %v", w.Labels)
	}
	keys := map[string]bool{}
	for _, e := range w.Taints {
		k, _ := wireTaintKey(e)
		keys[k[0]] = true
	}
	if !keys["platform/reserved"] || !keys["nvidia.com/gpu"] || keys["b"] || len(w.Taints) != 2 {
		t.Errorf("taints sent: %v", w.Taints)
	}
	// Removing every label and taint clears the managed ones only.
	plan = gpuPlanWithoutPublicIP()
	plan.Spec.WorkerPools[0].Labels, plan.Spec.WorkerPools[0].Taints = types.MapNull(types.StringType), nil
	w = buildMKSClusterFrom(plan, cur, &prior).Spec.WorkerPools[0]
	if len(w.Labels) != 1 || w.Labels["platform/owned"] != "yes" || len(w.Taints) != 1 {
		t.Errorf("after removing the configured ones: labels %v taints %v", w.Labels, w.Taints)
	}
}

func TestTaintOrderAndEmptyValuesAreNotChanges(t *testing.T) {
	a := serverCluster()
	a.Spec.WorkerPools = []apiv1.MKSNodeGroup{{Name: "p", Taints: []any{map[string]any{"key": "x", "value": "", "effect": "NoSchedule"}, map[string]any{"key": "y", "effect": "NoExecute"}}}}
	b := serverCluster()
	b.Spec.WorkerPools = []apiv1.MKSNodeGroup{{Name: "p", Taints: []any{map[string]any{"key": "y", "effect": "NoExecute"}, map[string]any{"key": "x", "effect": "NoSchedule"}}}}
	if !sameRequest(a, b) {
		t.Error("taint order or an empty value counts as a change")
	}
	b.Spec.WorkerPools[0].Taints = b.Spec.WorkerPools[0].Taints[:1]
	if sameRequest(a, b) {
		t.Error("a missing taint was not noticed")
	}
}

func TestAnOmittedFalsePublicIPIsNotAChange(t *testing.T) {
	cur := serverCluster() // pool "a" without assignPublicIp
	plan := plannedModel()
	plan.Spec.WorkerPools[0].NodeCount = types.Int64Value(2)
	plan.Spec.WorkerPools[0].AssignPublicIP = types.BoolValue(false)
	if !sameRequest(buildMKSCluster(plan, cur), cur) {
		t.Error("assign_public_ip = false against an omitted flag counts as a change")
	}
	plan.Spec.WorkerPools[0].AssignPublicIP = types.BoolValue(true)
	if sameRequest(buildMKSCluster(plan, cur), cur) {
		t.Error("turning the public IP on was not noticed")
	}
}

func TestAutomaticPoolWhoseSizeTheServerOmitsIsNotAChange(t *testing.T) {
	cur := serverCluster()
	cur.Spec.WorkerPools = []apiv1.MKSNodeGroup{{Name: "a", Scaling: &apiv1.MKSScaling{Mode: "automatic", Min: 1, Max: 5}}} // no desired
	plan := plannedModel()
	plan.Spec.WorkerPools = []mksWorkerPoolModel{autoPool(1, 5, 2)}
	if !sameRequest(buildMKSCluster(plan, cur), cur) {
		t.Error("an automatic pool without a reported size looks changed")
	}
	if w := mksClusterModelFromServer(plan, cur, nil, true).Spec.WorkerPools[0]; w.NodeCount.ValueInt64() != 2 {
		t.Errorf("node_count must keep the configured size when the platform reports none: %v", w.NodeCount)
	}
	if w := mksClusterModelFromServer(mksResourceModel{}, cur, nil, false).Spec.WorkerPools[0]; w.NodeCount.ValueInt64() != 1 {
		t.Errorf("an imported pool without a reported size shows its minimum: %v", w.NodeCount)
	}
	// Bounds that exclude the size the autoscaler chose pull it inside them.
	cur.Spec.WorkerPools[0].Scaling.Desired = 5
	plan.Spec.WorkerPools = []mksWorkerPoolModel{autoPool(1, 3, 2)}
	if sc := buildMKSCluster(plan, cur).Spec.WorkerPools[0].Scaling; sc.Desired != 2 || sc.Max != 3 {
		t.Errorf("a size above the new maximum: %+v", sc)
	}
	plan.Spec.WorkerPools = []mksWorkerPoolModel{autoPool(1, 3, 9)}
	if sc := buildMKSCluster(plan, cur).Spec.WorkerPools[0].Scaling; sc.Desired != 3 {
		t.Errorf("a starting size above the maximum is clamped: %+v", sc)
	}
}

// A controller that places nodes per pool clears the cluster-level VPC and subnet.
func TestClusterLevelNetworkTheControllerClearsIsNeitherResentNorReportedAsDrift(t *testing.T) {
	cur := serverCluster()
	cur.Spec.Networking.VPC, cur.Spec.Networking.Subnet = "", ""
	plan := plannedModel()
	plan.Spec.WorkerPools[0].NodeCount = types.Int64Value(2) // what the cluster holds
	plan.Spec.Networking = &mksNetworkingModel{VPC: str("x"), Subnet: str("y"), PodSubnet: types.StringNull(), ServiceSubnet: types.StringNull(), CNIName: types.StringNull(), CNIVersion: types.StringNull()}

	if !sameRequest(buildMKSCluster(plan, cur), cur) {
		t.Error("a cluster-level VPC the controller clears makes every apply a redeploy")
	}
	if got := mksClusterModelFromServer(plan, cur, nil, false).Spec.Networking; got.VPC.ValueString() != "x" || got.Subnet.ValueString() != "y" {
		t.Errorf("the configured VPC and subnet must stay in the state: %+v", got)
	}
	// A create still sends them: controllers that use them need them.
	if nw := buildMKSCluster(plan, nil).Spec.Networking; nw.VPC != "x" || nw.Subnet != "y" {
		t.Errorf("create: %+v", nw)
	}
	// Not configured and not reported: null, never an unknown value left over from the plan.
	plain := plannedModel()
	plain.Spec.Networking = &mksNetworkingModel{VPC: types.StringUnknown(), Subnet: types.StringUnknown()}
	if got := mksClusterModelFromServer(plain, cur, nil, false).Spec.Networking; got.VPC.IsUnknown() || got.Subnet.IsUnknown() {
		t.Errorf("computed values must be known after apply: %+v", got)
	}
}

func TestSKUChangeToBareMetalDropsTheBootDisk(t *testing.T) {
	cur := serverCluster()
	cur.Spec.WorkerPools = []apiv1.MKSNodeGroup{{Name: "a", SKUName: "vm-small", SKUType: "VMAAS", BootDiskSizeGb: 60, Scaling: &apiv1.MKSScaling{Mode: "manual", Count: 2}}}
	plan := plannedModel()
	plan.Spec.WorkerPools[0].NodeCount = types.Int64Value(2)
	plan.Spec.WorkerPools[0].SKUName, plan.Spec.WorkerPools[0].SKUType = str("bm-1"), str("BMAAS")
	if w := buildMKSCluster(plan, cur).Spec.WorkerPools[0]; w.BootDiskSizeGb != 0 {
		t.Errorf("a bare-metal pool was sent a boot disk of %d GB", w.BootDiskSizeGb)
	}
}

// --- plan-time checks added after review ----------------------------------

func TestShapeChecksSkipValuesKnownOnlyAfterApply(t *testing.T) {
	plan := plannedModel()
	pool := poolPlan()
	pool.Network = &mksPoolNetworkModel{VPC: str("v"), Subnet: types.StringUnknown(), SecurityGroup: types.StringNull()}
	pool.Taints = []mksTaintModel{{Key: types.StringUnknown(), Value: types.StringNull(), Effect: str("NoSchedule")}}
	pool.BootDiskSizeGB = types.Int64Unknown()
	plan.Spec.WorkerPools = []mksWorkerPoolModel{pool}
	if got := shapeErrors(plan); got != "" {
		t.Errorf("unknown values were judged: %s", got)
	}
	auto := autoPool(1, 5, 2)
	auto.MinNodes = types.Int64Unknown()
	plan.Spec.WorkerPools = []mksWorkerPoolModel{auto}
	if got := shapeErrors(plan); got != "" {
		t.Errorf("an unknown bound was judged: %s", got)
	}
	unknownMode := autoPool(1, 5, 2)
	unknownMode.ScalingMode = types.StringUnknown()
	plan.Spec.WorkerPools = []mksWorkerPoolModel{unknownMode}
	if got := shapeErrors(plan); got != "" {
		t.Errorf("an unknown mode was judged: %s", got)
	}
	// A known problem next to unknown values is still reported.
	bad := poolPlan()
	bad.Taints = []mksTaintModel{taint("k", "v", "Sometimes")}
	bad.Network = &mksPoolNetworkModel{VPC: str("v"), Subnet: types.StringUnknown()}
	plan.Spec.WorkerPools = []mksWorkerPoolModel{bad}
	if got := shapeErrors(plan); !strings.Contains(got, "NoSchedule, PreferNoSchedule or NoExecute") {
		t.Errorf("a known error was lost: %q", got)
	}
}

func TestAnUnknownNetworkIsNotACHangeOfTheNetwork(t *testing.T) {
	prior := plannedModel()
	prior.Spec.WorkerPools = []mksWorkerPoolModel{poolPlan()}
	plan := plannedModel()
	pool := poolPlan()
	pool.Network = &mksPoolNetworkModel{VPC: str("vpc-a"), Subnet: types.StringUnknown(), SecurityGroup: types.StringNull()}
	plan.Spec.WorkerPools = []mksWorkerPoolModel{pool}
	var d diag.Diagnostics
	validateMKSFixedAfterCreate(plan, prior, &d)
	if d.HasError() {
		t.Errorf("a subnet that is known after apply was called a change: %v", d)
	}
}

func TestPoolNamesAreChecked(t *testing.T) {
	prior := plannedModel()
	prior.Spec.WorkerPools = []mksWorkerPoolModel{{Name: str("Legacy_Pool")}}
	run := func(names ...string) string {
		plan := plannedModel()
		plan.Spec.WorkerPools = nil
		for _, n := range names {
			plan.Spec.WorkerPools = append(plan.Spec.WorkerPools, mksWorkerPoolModel{Name: str(n)})
		}
		var d diag.Diagnostics
		validateMKSPoolNames(plan, &prior, &d)
		var out []string
		for _, e := range d.Errors() {
			out = append(out, e.Summary())
		}
		return strings.Join(out, "; ")
	}
	if got := run("gpu-1", "a", "9lives"); got != "" {
		t.Errorf("valid names: %s", got)
	}
	if got := run("control-plane"); !strings.Contains(got, "Reserved") {
		t.Errorf("control-plane: %q", got)
	}
	for _, bad := range []string{"Upper", "under_score", "-lead", "trail-", strings.Repeat("a", 64)} {
		if got := run(bad); !strings.Contains(got, "Invalid pool name") {
			t.Errorf("%q: %q", bad, got)
		}
	}
	if got := run("Legacy_Pool"); got != "" {
		t.Errorf("a pool the cluster already has keeps its name: %q", got)
	}
}

func TestUpgradeMustBeOneOfTheTargetsThePlatformReports(t *testing.T) {
	state := plannedModel()
	state.Spec.KubernetesVersion = str("1.35")
	state.Status.KubernetesVersion = str("v1.35.4")
	state.Status.AvailableVersions = []types.String{str("1.36")}
	run := func(version string, st mksResourceModel) string {
		plan := plannedModel()
		plan.Spec.KubernetesVersion = str(version)
		var d diag.Diagnostics
		validateMKSUpgrade(plan, st, &d)
		var out []string
		for _, e := range d.Errors() {
			out = append(out, e.Detail())
		}
		return strings.Join(out, "; ")
	}
	for _, ok := range []string{"1.36", "v1.36.1", "1.35", "v1.35.4"} {
		if got := run(ok, state); got != "" {
			t.Errorf("%s: %s", ok, got)
		}
	}
	if got := run("1.37", state); !strings.Contains(got, "upgrade to 1.36 only") {
		t.Errorf("skipping a minor version: %q", got)
	}
	if got := run("1.34", state); got == "" {
		t.Error("a downgrade was accepted")
	}
	none := state
	none.Status.AvailableVersions = nil
	if got := run("1.37", none); got != "" {
		t.Errorf("without a reported list nothing can be checked: %s", got)
	}
}

func TestBootDiskNeedsAVirtualMachineSKUType(t *testing.T) {
	plan := plannedModel()
	pool := poolPlan()
	pool.SKUType = str("BMAAS")
	plan.Spec.WorkerPools = []mksWorkerPoolModel{pool}
	if got := shapeErrors(plan); !strings.Contains(got, "bare-metal pools refuse it") {
		t.Errorf("boot disk on a bare-metal pool: %q", got)
	}
	pool.SKUType = str("VMAAS")
	plan.Spec.WorkerPools = []mksWorkerPoolModel{pool}
	if got := shapeErrors(plan); got != "" {
		t.Errorf("boot disk on a VM pool: %s", got)
	}
}

func TestAPublicIPSetOnSomeWorkerPoolsOnlyWarns(t *testing.T) {
	a, b := poolPlan(), poolPlan()
	b.Name, b.AssignPublicIP = str("other"), types.BoolNull()
	var d diag.Diagnostics
	validateMKSPublicIPs([]mksWorkerPoolModel{a, b}, &d)
	if d.HasError() || d.WarningsCount() != 1 {
		t.Errorf("want one warning, got %v", d)
	}
}
