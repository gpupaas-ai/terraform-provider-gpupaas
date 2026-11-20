package resources

import (
	"context"
	"errors"
	"strings"
	"testing"

	gpupaas "github.com/gpupaas-ai/gpupaas-go"
	apiv1 "github.com/gpupaas-ai/gpupaas-go/apis/v1alpha1"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/gpupaas-ai/terraform-provider-gpupaas/internal/client"
)

// Rules and settings of the v3.1.41 API reference ("MKS First-Class - API Docs"): the control plane size, the
// pool datacenter, SSH keys and node access, the boot disk range, the OS image, annotations and the metadata
// fast path.

func diagText(d diag.Diagnostics, errors bool) string {
	var out []string
	list := d.Warnings()
	if errors {
		list = d.Errors()
	}
	for _, e := range list {
		out = append(out, e.Summary()+": "+e.Detail())
	}
	return strings.Join(out, "\n")
}

func TestTheControlPlaneHasOneOrThreeNodes(t *testing.T) {
	for n, want := range map[int64]string{0: "at least one", 1: "", 2: "1 node, or 3", 3: "", 4: "1 node, or 3", 5: "1 node, or 3", 7: "1 node, or 3"} {
		plan := plannedModel()
		plan.Spec.ControlPlane.NodeCount = types.Int64Value(n)
		var d diag.Diagnostics
		validateMKSShape(plan, &d)
		got := diagText(d, true)
		if want == "" && got != "" || want != "" && !strings.Contains(got, want) {
			t.Errorf("node_count %d: errors %q, want %q", n, got, want)
		}
	}
	// A count known only after apply is not judged.
	plan := plannedModel()
	plan.Spec.ControlPlane.NodeCount = types.Int64Unknown()
	var d diag.Diagnostics
	validateMKSShape(plan, &d)
	if d.HasError() {
		t.Errorf("unknown node_count: %s", diagText(d, true))
	}
}

func TestTheControlPlaneSizeCannotChangeAfterCreate(t *testing.T) {
	state, plan := plannedModel(), plannedModel()
	plan.Spec.ControlPlane.NodeCount = types.Int64Value(3)
	var d diag.Diagnostics
	validateMKSFixedAfterCreate(plan, state, &d)
	if got := diagText(d, true); !strings.Contains(got, "can't be changed") || !strings.Contains(got, "high availability") {
		t.Errorf("resize: %q", got)
	}
	plan.Spec.ControlPlane.NodeCount = types.Int64Value(1)
	d = nil
	validateMKSFixedAfterCreate(plan, state, &d)
	if d.HasError() {
		t.Errorf("same size: %s", diagText(d, true))
	}
	// A count known only after apply is not a change.
	plan.Spec.ControlPlane.NodeCount = types.Int64Unknown()
	d = nil
	validateMKSFixedAfterCreate(plan, state, &d)
	if d.HasError() {
		t.Errorf("unknown size: %s", diagText(d, true))
	}
}

func TestAPoolDatacenterMustBeTheLocation(t *testing.T) {
	plan := plannedModel()
	plan.Spec.Location = str("dc-1")
	plan.Spec.ControlPlane.Datacenter = str("dc-1")
	plan.Spec.WorkerPools[0].Datacenter = str("DC-1") // the comparison ignores case
	var d diag.Diagnostics
	validateMKSShape(plan, &d)
	if d.HasError() {
		t.Fatalf("same datacenter: %s", diagText(d, true))
	}
	plan.Spec.WorkerPools[0].Datacenter = str("dc-2")
	d = nil
	validateMKSShape(plan, &d)
	if got := diagText(d, true); !strings.Contains(got, "per-pool datacenter") || !strings.Contains(got, "dc-2") {
		t.Errorf("another datacenter: %q", got)
	}
	// Without a configured location (it is only known after apply) nothing can be compared.
	plan.Spec.Location = types.StringUnknown()
	d = nil
	validateMKSShape(plan, &d)
	if d.HasError() {
		t.Errorf("unknown location: %s", diagText(d, true))
	}
}

func TestSSHKeyMustBeTheNameOfAKey(t *testing.T) {
	for key, bad := range map[string]bool{
		"mine": false, "team-key": false, "key_1": false, "ssh-key": false, "ssh-key-prod": false, "sk-team-key": false, "ecdsa-laptop": false,
		"ssh-ed25519 AAAAC3 me@host": true, "ssh-rsa AAAA": true, "ecdsa-sha2-nistp256 AAAA": true,
		"-----BEGIN OPENSSH PRIVATE KEY-----": true, "two words": true, "sk-ssh-ed25519@openssh.com AAAA": true,
	} {
		plan := plannedModel()
		plan.Spec.ControlPlane.SSHKey = str(key)
		var d diag.Diagnostics
		validateMKSShape(plan, &d)
		if got := diagText(d, true); bad != strings.Contains(got, "name of an SSH key") {
			t.Errorf("ssh_key %q: errors %q", key, got)
		}
	}
}

func TestPoolsNamingDifferentSSHKeysWarnThatTheControlPlanesWins(t *testing.T) {
	plan := plannedModel()
	plan.Spec.ControlPlane.SSHKey = str("cp-key")
	plan.Spec.WorkerPools = []mksWorkerPoolModel{
		{Name: str("a"), SSHKey: str("cp-key"), NodeCount: types.Int64Value(1), ScalingMode: str("manual")},
		{Name: str("b"), SSHKey: str("other"), NodeCount: types.Int64Value(1), ScalingMode: str("manual")},
		{Name: str("c"), NodeCount: types.Int64Value(1), ScalingMode: str("manual")},
	}
	var d diag.Diagnostics
	validateMKSShape(plan, &d)
	if d.HasError() {
		t.Fatalf("errors: %s", diagText(d, true))
	}
	got := diagText(d, false)
	if !strings.Contains(got, `pool "b"`) || !strings.Contains(got, `"cp-key" is used`) || strings.Contains(got, `pool "a"`) || strings.Contains(got, `pool "c"`) {
		t.Errorf("warnings: %q", got)
	}
	// Without a control-plane key the first pool's key is the cluster's.
	plan.Spec.ControlPlane.SSHKey = types.StringNull()
	d = nil
	validateMKSShape(plan, &d)
	if got := diagText(d, false); !strings.Contains(got, `pool "b"`) || !strings.Contains(got, `"cp-key" is used`) {
		t.Errorf("warnings without a control-plane key: %q", got)
	}
}

func asksForNodeAccess() *apiv1.MKSClusterProfile {
	return &apiv1.MKSClusterProfile{Metadata: apiv1.ObjectMeta{Name: "prof"}, Spec: apiv1.MKSClusterProfileSpec{ExposeSSHKeyChoice: true}}
}

func nodeAccessErrors(plan mksResourceModel, prior *mksResourceModel, p *apiv1.MKSClusterProfile) string {
	var d diag.Diagnostics
	validateMKSNodeAccess(plan, prior, p, &d)
	return diagText(d, true)
}

func TestANewClusterNeedsNodeAccessWhenTheProfileAsksForIt(t *testing.T) {
	plan := plannedModel()
	got := nodeAccessErrors(plan, nil, asksForNodeAccess())
	if strings.Count(got, "Node access is required") != 1 || !strings.Contains(got, "control plane or on a worker pool") {
		t.Fatalf("create without access: %q", got)
	}
	// The platform installs one key per cluster: a password or a key on the control plane, or on any pool, is enough.
	cp := plannedModel()
	cp.Spec.ControlPlane.UserPassword = str("pw")
	if got := nodeAccessErrors(cp, nil, asksForNodeAccess()); got != "" {
		t.Errorf("password on the control plane: %q", got)
	}
	pool := plannedModel()
	pool.Spec.WorkerPools[0].SSHKey = str("mine")
	if got := nodeAccessErrors(pool, nil, asksForNodeAccess()); got != "" {
		t.Errorf("key on a pool: %q", got)
	}
	// A value known only after apply is not judged, nor is a control plane that is unknown as a whole.
	unknown := plannedModel()
	unknown.Spec.ControlPlane.SSHKey = types.StringUnknown()
	if got := nodeAccessErrors(unknown, nil, asksForNodeAccess()); got != "" {
		t.Errorf("unknown access: %q", got)
	}
	whole := plannedModel()
	whole.Spec.ControlPlane = &mksControlPlaneModel{} // what an unknown object decodes to
	if got := nodeAccessErrors(whole, nil, asksForNodeAccess()); got != "" {
		t.Errorf("unknown control plane: %q", got)
	}
}

func TestNodeAccessIsOnlyCheckedWhereTheProfileHasNoDefault(t *testing.T) {
	plan := plannedModel()
	for name, p := range map[string]*apiv1.MKSClusterProfile{
		"does not ask":     {Spec: apiv1.MKSClusterProfileSpec{}},
		"default key":      {Spec: apiv1.MKSClusterProfileSpec{ExposeSSHKeyChoice: true, DefaultSSHKey: "mine"}},
		"default password": {Spec: apiv1.MKSClusterProfileSpec{ExposeSSHKeyChoice: true, DefaultUserPassword: "******"}},
	} {
		if got := nodeAccessErrors(plan, nil, p); got != "" {
			t.Errorf("profile that %s: %q", name, got)
		}
	}
}

// An existing cluster settled its access when it was created, and the platform never returns the password: neither
// an update nor a new pool is held to it.
func TestAnExistingClusterIsNotCheckedForNodeAccess(t *testing.T) {
	state := plannedModel()
	plan := plannedModel()
	plan.Spec.WorkerPools = append(plan.Spec.WorkerPools, mksWorkerPoolModel{Name: str("new"), NodeCount: types.Int64Value(1), ScalingMode: str("manual")})
	if got := nodeAccessErrors(plan, &state, asksForNodeAccess()); got != "" {
		t.Fatalf("update with a new pool: %q", got)
	}
}

func TestBootDiskRangeProblem(t *testing.T) {
	bd := &apiv1.VMBootDisk{MinSize: "50", MaxSize: "500", Increment: "10", Unit: "GB"}
	for _, tc := range []struct {
		size int64
		want string
	}{
		{50, ""}, {60, ""}, {500, ""}, {330, ""},
		{40, "outside the boot disk range"}, {510, "outside the boot disk range"}, {0, "outside"},
		{55, "step by 10 GB from 50 GB"}, {61, "step by 10 GB from 50 GB"},
	} {
		got := bootDiskRangeProblem(tc.size, "vm-small", bd)
		if tc.want == "" && got != "" || tc.want != "" && !strings.Contains(got, tc.want) {
			t.Errorf("%d GB: %q, want %q", tc.size, got, tc.want)
		}
	}
	if got := bootDiskRangeProblem(1, "vm-small", nil); got != "" {
		t.Errorf("a SKU without a range: %q", got)
	}
	// A range that is only partly given checks what it has.
	if got := bootDiskRangeProblem(10, "vm-small", &apiv1.VMBootDisk{MinSize: "20"}); !strings.Contains(got, "outside") {
		t.Errorf("minimum only: %q", got)
	}
	if got := bootDiskRangeProblem(1000, "vm-small", &apiv1.VMBootDisk{MinSize: "20"}); got != "" {
		t.Errorf("minimum only, large size: %q", got)
	}
	if got := bootDiskRangeProblem(55, "vm-small", &apiv1.VMBootDisk{MinSize: "50", MaxSize: "100", Unit: "GB"}); got != "" {
		t.Errorf("no increment: %q", got)
	}
	if got := bootDiskRangeProblem(1, "vm-small", &apiv1.VMBootDisk{MinSize: "20", MaxSize: "40"}); !strings.Contains(got, "(20-40 GB)") {
		t.Errorf("message: %q", got)
	}
	// A step below one GB must not panic (an integer division by zero) and fractions are steps like any other.
	for _, step := range []string{"0.5", "0", "-1", "abc"} {
		if got := bootDiskRangeProblem(55, "vm-small", &apiv1.VMBootDisk{MinSize: "50", MaxSize: "100", Increment: step}); got != "" {
			t.Errorf("increment %q: %q", step, got)
		}
	}
	if got := bootDiskRangeProblem(15, "vm-small", &apiv1.VMBootDisk{MinSize: "10", MaxSize: "100", Increment: "2.5"}); got != "" {
		t.Errorf("a fractional step that fits: %q", got)
	}
	if got := bootDiskRangeProblem(16, "vm-small", &apiv1.VMBootDisk{MinSize: "10", MaxSize: "100", Increment: "2.5"}); !strings.Contains(got, "step by 2.5 GB") {
		t.Errorf("a fractional step that does not fit: %q", got)
	}
	// A maximum of 0 is no maximum.
	if got := bootDiskRangeProblem(200, "vm-small", &apiv1.VMBootDisk{MinSize: "10", MaxSize: "0", Increment: "10"}); got != "" {
		t.Errorf("maximum 0: %q", got)
	}
}

// The range check reads the pool's VM SKU through the client; a SKU known only after apply is not looked up.
func TestBootDiskIsCheckedAgainstTheVMSKUOfThePool(t *testing.T) {
	cs := client.NewFakeClientset()
	client.SeedVMProfile(cs, "p", apiv1.VMProfile{Metadata: apiv1.ObjectMeta{Name: "vm-small"}, Spec: apiv1.VMProfileSpec{BootDisk: &apiv1.VMBootDisk{MinSize: "50", MaxSize: "100", Increment: "10", Unit: "GB"}}})
	r := &mksClusterResource{pd: &client.ProviderData{Clientset: cs}}
	profile := &apiv1.MKSClusterProfile{Spec: apiv1.MKSClusterProfileSpec{Workers: &apiv1.MKSGroupProfile{DefaultSKUName: "vm-small"}}}
	check := func(prior *mksResourceModel, edit func(*mksWorkerPoolModel)) string {
		plan := plannedModel()
		wp := mksWorkerPoolModel{Name: str("a"), NodeCount: types.Int64Value(1), ScalingMode: str("manual"), SKUName: str("vm-small"), BootDiskSizeGB: types.Int64Value(200)}
		edit(&wp)
		plan.Spec.WorkerPools = []mksWorkerPoolModel{wp}
		var d diag.Diagnostics
		r.validateMKSBootDiskRanges(context.Background(), plan, prior, profile, &d)
		return diagText(d, true)
	}
	if got := check(nil, func(*mksWorkerPoolModel) {}); !strings.Contains(got, "outside the boot disk range of SKU \"vm-small\" (50-100 GB)") {
		t.Errorf("a size outside the range: %q", got)
	}
	// The profile's default SKU applies when the pool names none.
	if got := check(nil, func(wp *mksWorkerPoolModel) { wp.SKUName = types.StringNull() }); !strings.Contains(got, "outside the boot disk range") {
		t.Errorf("default SKU: %q", got)
	}
	// A SKU known only after apply is not looked up, and a bare-metal pool takes no boot disk here.
	if got := check(nil, func(wp *mksWorkerPoolModel) { wp.SKUName = types.StringUnknown() }); got != "" {
		t.Errorf("unknown SKU: %q", got)
	}
	if got := check(nil, func(wp *mksWorkerPoolModel) { wp.SKUType = str("BMAAS") }); got != "" {
		t.Errorf("bare metal: %q", got)
	}
	// A SKU that is not a VM SKU (no profile) is skipped; an unchanged size is not checked again.
	if got := check(nil, func(wp *mksWorkerPoolModel) { wp.SKUName = str("bmaas-1") }); got != "" {
		t.Errorf("not a VM SKU: %q", got)
	}
	prior := plannedModel()
	prior.Spec.WorkerPools = []mksWorkerPoolModel{{Name: str("a"), BootDiskSizeGB: types.Int64Value(200)}}
	if got := check(&prior, func(*mksWorkerPoolModel) {}); got != "" {
		t.Errorf("unchanged size: %q", got)
	}
	if got := check(nil, func(wp *mksWorkerPoolModel) { wp.BootDiskSizeGB = types.Int64Value(60) }); got != "" {
		t.Errorf("a size inside the range: %q", got)
	}
}

// A problem the cluster already has must not block an unrelated update, whatever kind of rule it breaks.
func TestShapeProblemsOfAnExistingClusterOnlyWarn(t *testing.T) {
	bad := plannedModel()
	bad.Spec.Location = str("dc-1")
	bad.Spec.WorkerPools[0].Datacenter = str("dc-2")
	var now, was diag.Diagnostics
	validateMKSShape(bad, &now)
	validateMKSShape(bad, &was)
	var out diag.Diagnostics
	demoteUnchanged(&out, now, was)
	if out.HasError() || len(out.Warnings()) != 1 || !strings.Contains(diagText(out, false), "not blocking this plan") {
		t.Fatalf("an unchanged problem: errors %q warnings %q", diagText(out, true), diagText(out, false))
	}
	// A new problem is still an error.
	out = nil
	demoteUnchanged(&out, now, diag.Diagnostics{})
	if !out.HasError() {
		t.Error("a new problem was not an error")
	}
}

func TestChangingTheOSImageWarnsThatThePoolIsReimaged(t *testing.T) {
	state, plan := plannedModel(), plannedModel()
	state.Spec.ControlPlane.OSImage, plan.Spec.ControlPlane.OSImage = str("ubuntu-24"), str("ubuntu-24")
	state.Spec.WorkerPools[0].OSImage, plan.Spec.WorkerPools[0].OSImage = str("ubuntu-24"), str("ubuntu-26")
	var d diag.Diagnostics
	warnMKSReimage(plan, state, &d)
	got := diagText(d, false)
	if d.HasError() || !strings.Contains(got, `worker pool "a"`) || !strings.Contains(got, "ubuntu-24") || !strings.Contains(got, "ubuntu-26") || strings.Contains(got, "control plane") {
		t.Errorf("warnings: %q", got)
	}
	// A pool that is new, or that leaves the image out, is not re-imaged by this plan.
	plan.Spec.WorkerPools[0].OSImage = types.StringNull()
	plan.Spec.WorkerPools = append(plan.Spec.WorkerPools, mksWorkerPoolModel{Name: str("new"), OSImage: str("ubuntu-26")})
	d = nil
	warnMKSReimage(plan, state, &d)
	if len(d) != 0 {
		t.Errorf("unexpected warnings: %q", diagText(d, false))
	}
}

func TestWorkerPoolAnnotationsAreSentKeptAndCleared(t *testing.T) {
	pool := func(annotations map[string]string) mksWorkerPoolModel {
		wp := mksWorkerPoolModel{Name: str("a"), NodeCount: types.Int64Value(2), ScalingMode: str("manual"), Labels: types.MapNull(types.StringType), Annotations: types.MapNull(types.StringType)}
		if annotations != nil {
			wp.Annotations = labelMap(annotations)
		}
		return wp
	}
	cur := serverCluster()
	cur.Spec.WorkerPools[0].Annotations = map[string]string{"platform/note": "keep", "team": "old"}

	// The configured annotations replace the ones the user managed; the platform's own stay.
	plan := plannedModel()
	plan.Spec.WorkerPools = []mksWorkerPoolModel{pool(map[string]string{"team": "ml"})}
	prior := plannedModel().Spec
	prior.WorkerPools = []mksWorkerPoolModel{pool(map[string]string{"team": "old"})}
	got := buildMKSClusterFrom(plan, cur, &prior).Spec.WorkerPools[0].Annotations
	if len(got) != 2 || got["team"] != "ml" || got["platform/note"] != "keep" {
		t.Errorf("annotations sent: %v", got)
	}
	// Removing them from the configuration clears the managed ones only.
	plan.Spec.WorkerPools = []mksWorkerPoolModel{pool(nil)}
	got = buildMKSClusterFrom(plan, cur, &prior).Spec.WorkerPools[0].Annotations
	if len(got) != 1 || got["platform/note"] != "keep" {
		t.Errorf("annotations after removal: %v", got)
	}
	// The state keeps the configured ones while the pool holds each, and takes the server's on a difference.
	srv := serverCluster()
	srv.Spec.WorkerPools[0].Annotations = map[string]string{"platform/note": "x", "team": "ml"}
	base := plannedModel()
	base.Spec.WorkerPools = []mksWorkerPoolModel{pool(map[string]string{"team": "ml"})}
	m := mksClusterModelFromServer(base, srv, nil, true)
	if a := elementsToStringMap(m.Spec.WorkerPools[0].Annotations); len(a) != 1 || a["team"] != "ml" {
		t.Errorf("state annotations: %v", a)
	}
	srv.Spec.WorkerPools[0].Annotations = map[string]string{"team": "changed"}
	m = mksClusterModelFromServer(base, srv, nil, true)
	if a := elementsToStringMap(m.Spec.WorkerPools[0].Annotations); a["team"] != "changed" {
		t.Errorf("drift: %v", a)
	}
	// Unconfigured annotations stay null, whatever the platform holds.
	base.Spec.WorkerPools = []mksWorkerPoolModel{pool(nil)}
	if m := mksClusterModelFromServer(base, serverCluster(), nil, true); !m.Spec.WorkerPools[0].Annotations.IsNull() {
		t.Errorf("unconfigured annotations: %v", m.Spec.WorkerPools[0].Annotations)
	}
}

func TestNodeStatusCarriesTheAddressesAndRoles(t *testing.T) {
	nodes := &apiv1.MKSNodeList{Items: []apiv1.MKSNode{{
		Metadata: apiv1.ObjectMeta{Name: "n1", Labels: map[string]string{"nodepoolname": "master"}},
		Spec:     apiv1.MKSNodeSpec{Hostname: "n1", KubeVersion: "v1.36.4", PrivateIP: "10.0.0.5", Roles: []string{"Master"}, OperatingSystem: "ubuntu", Arch: "amd64"},
		Status:   apiv1.MKSNodeStatus{EdgeStatus: "READY"},
	}, {
		// An older controller reports little.
		Metadata: apiv1.ObjectMeta{Name: "n2", Labels: map[string]string{"nodepoolname": "a"}},
		Spec:     apiv1.MKSNodeSpec{Hostname: "n2", KubeVersion: "v1.36.4"},
	}}}
	m := mksClusterModelFromServer(plannedModel(), serverCluster(), nodes, true)
	n := m.Status.Nodes
	if len(n) != 2 || n[0].PrivateIP.ValueString() != "10.0.0.5" || n[0].Status.ValueString() != "READY" || n[0].OperatingSystem.ValueString() != "ubuntu" || n[0].Arch.ValueString() != "amd64" {
		t.Fatalf("node 0: %+v", n)
	}
	if roles := n[0].Roles.Elements(); len(roles) != 1 || roles[0].(types.String).ValueString() != "Master" {
		t.Errorf("roles: %v", n[0].Roles)
	}
	if !n[1].PrivateIP.IsNull() || !n[1].Roles.IsNull() || !n[1].Status.IsNull() || n[1].NodePool.ValueString() != "a" {
		t.Errorf("node 1: %+v", n[1])
	}
}

// --- the metadata fast path ------------------------------------------------

func TestTagsOnlyPatch(t *testing.T) {
	cur := serverCluster()
	cur.Spec.Tags = map[string]string{"env": "dev", "owner": "me"}

	want := func(edit func(*apiv1.MKSCluster)) *apiv1.MKSCluster {
		c := cur.DeepCopyObject().(*apiv1.MKSCluster)
		edit(c)
		return c
	}
	// Nothing differs: no patch.
	if _, ok := tagsOnlyPatch(want(func(*apiv1.MKSCluster) {}), cur); ok {
		t.Error("an unchanged cluster produced a patch")
	}
	p, ok := tagsOnlyPatch(want(func(c *apiv1.MKSCluster) { c.Spec.Tags = map[string]string{"env": "prod"} }), cur)
	if !ok || !p.ReplaceTags || p.Tags["env"] != "prod" || len(p.Tags) != 1 || p.Labels != nil || p.Annotations != nil || p.ReplaceLabels || p.ReplaceAnnotations {
		t.Errorf("tags patch: %+v ok=%v", p, ok)
	}
	// Removing every tag sends an empty map, which clears them; nil would leave them.
	p, ok = tagsOnlyPatch(want(func(c *apiv1.MKSCluster) { c.Spec.Tags = nil }), cur)
	if !ok || !p.ReplaceTags || p.Tags == nil || len(p.Tags) != 0 {
		t.Errorf("clearing patch: %+v ok=%v", p, ok)
	}
	// Anything else that changes needs the full update, whatever the tags do. Labels and annotations are not part
	// of the patch: the platform adds labels of its own.
	for name, edit := range map[string]func(*apiv1.MKSCluster){
		"description":   func(c *apiv1.MKSCluster) { c.Metadata.Description = "new" },
		"labels":        func(c *apiv1.MKSCluster) { c.Metadata.Labels = map[string]string{"team": "ml"}; c.Spec.Tags = nil },
		"annotations":   func(c *apiv1.MKSCluster) { c.Metadata.Annotations = map[string]string{"a": "b"}; c.Spec.Tags = nil },
		"version":       func(c *apiv1.MKSCluster) { c.Spec.KubernetesVersion = "v1.37.0" },
		"pool size":     func(c *apiv1.MKSCluster) { c.Spec.WorkerPools[0].Scaling.Count = 5 },
		"pool and tags": func(c *apiv1.MKSCluster) { c.Spec.WorkerPools[0].Scaling.Count = 5; c.Spec.Tags = nil },
		"new pool": func(c *apiv1.MKSCluster) {
			c.Spec.WorkerPools = append(c.Spec.WorkerPools, apiv1.MKSNodeGroup{Name: "b"})
		},
	} {
		if _, ok := tagsOnlyPatch(want(edit), cur); ok {
			t.Errorf("%s: a patch was offered for a change the patch cannot make", name)
		}
	}
}

// The platform labels the cluster itself and tags can be set in the console. A change Terraform makes to its own tags
// must leave both alone, and an apply that changes nothing must not look like a change.
func TestWhatTerraformDoesNotManageSurvivesAnUpdate(t *testing.T) {
	cur := serverCluster()
	cur.Metadata.Labels = map[string]string{"team": "ml", "paas.envmgmt.io/compute_type": "mks"}
	cur.Spec.Tags = map[string]string{"env": "dev", "console": "set-by-hand"}

	state := plannedModel()
	state.Spec.WorkerPools[0].NodeCount = types.Int64Value(2) // what the platform holds
	state.Metadata.Labels = labelMap(map[string]string{"team": "ml"})
	state.Spec.Tags = labelMap(map[string]string{"env": "dev"})
	plan := state
	plan.Spec.Tags = labelMap(map[string]string{"env": "prod", "owner": "me"})

	// A: only a tag changes. The label of the platform stays, and so does the tag set by hand.
	obj := buildMKSClusterAfter(plan, cur, &state)
	if obj.Metadata.Labels["paas.envmgmt.io/compute_type"] != "mks" || len(obj.Metadata.Labels) != 2 {
		t.Errorf("A: labels sent %v", obj.Metadata.Labels)
	}
	if len(obj.Spec.Tags) != 3 || obj.Spec.Tags["env"] != "prod" || obj.Spec.Tags["owner"] != "me" || obj.Spec.Tags["console"] != "set-by-hand" {
		t.Errorf("A: tags sent %v", obj.Spec.Tags)
	}
	patch, ok := tagsOnlyPatch(obj, cur)
	if !ok || len(patch.Tags) != 3 || patch.Tags["console"] != "set-by-hand" || patch.Labels != nil {
		t.Errorf("A: patch %+v ok=%v", patch, ok)
	}

	// B: nothing the platform holds changes (a timeout, say). The request is the cluster as it is.
	same := buildMKSClusterAfter(state, cur, &state)
	if !sameRequest(same, cur) {
		t.Errorf("B: an apply that changes nothing differs from the cluster: labels %v tags %v", same.Metadata.Labels, same.Spec.Tags)
	}
	if _, ok := tagsOnlyPatch(same, cur); ok {
		t.Error("B: an apply that changes nothing produced a patch")
	}

	// C: tags are not configured and a label changes. The tags set by hand are not cleared.
	noTags := state
	noTags.Spec.Tags = types.MapNull(types.StringType)
	prior := noTags
	changed := noTags
	changed.Metadata.Labels = labelMap(map[string]string{"team": "ml", "tier": "gpu"})
	obj = buildMKSClusterAfter(changed, cur, &prior)
	if len(obj.Spec.Tags) != 2 || obj.Spec.Tags["console"] != "set-by-hand" {
		t.Errorf("C: tags sent %v", obj.Spec.Tags)
	}
	if obj.Metadata.Labels["tier"] != "gpu" {
		t.Errorf("C: the changed label was not sent: %v", obj.Metadata.Labels)
	}

	// D: the user drops the tags they managed: those go, the one set by hand stays.
	dropped := state
	dropped.Spec.Tags = types.MapNull(types.StringType)
	obj = buildMKSClusterAfter(dropped, cur, &state)
	if len(obj.Spec.Tags) != 1 || obj.Spec.Tags["console"] != "set-by-hand" {
		t.Errorf("D: tags sent %v", obj.Spec.Tags)
	}
}

func TestTagsInTheStateAreTheConfiguredOnesAndAnEmptyMapStaysEmpty(t *testing.T) {
	srv := serverCluster()
	srv.Spec.Tags = map[string]string{"env": "dev", "console": "x"}
	base := plannedModel()
	base.Spec.Tags = labelMap(map[string]string{"env": "dev"})
	// The tag set by hand stays out of the state, so it is no difference from the configuration.
	if tags := elementsToStringMap(mksClusterModelFromServer(base, srv, nil, true).Spec.Tags); len(tags) != 1 || tags["env"] != "dev" {
		t.Errorf("tags in the state: %v", tags)
	}
	// A configured tag the cluster no longer holds, or holds with another value, is drift.
	srv.Spec.Tags = map[string]string{"env": "other"}
	if tags := elementsToStringMap(mksClusterModelFromServer(base, srv, nil, true).Spec.Tags); tags["env"] != "other" {
		t.Errorf("drift: %v", tags)
	}
	srv.Spec.Tags = nil
	if m := mksClusterModelFromServer(base, srv, nil, true).Spec.Tags; len(m.Elements()) != 0 {
		t.Errorf("a tag removed outside Terraform: %v", m)
	}
	// tags = {} is an empty map, not null: the framework refuses a result that differs from the plan.
	base.Spec.Tags = types.MapValueMust(types.StringType, map[string]attr.Value{})
	if m := mksClusterModelFromServer(base, srv, nil, true).Spec.Tags; m.IsNull() {
		t.Error("tags = {} came back null")
	}
	// Not configured: null, whatever the cluster holds. An import takes what it holds.
	base.Spec.Tags = types.MapNull(types.StringType)
	srv.Spec.Tags = map[string]string{"console": "x"}
	if m := mksClusterModelFromServer(base, srv, nil, true).Spec.Tags; !m.IsNull() {
		t.Errorf("unconfigured tags: %v", m)
	}
	imported := mksClusterModelFromServer(mksResourceModel{Metadata: base.Metadata}, srv, nil, false)
	if tags := elementsToStringMap(imported.Spec.Tags); tags["console"] != "x" {
		t.Errorf("import: %v", tags)
	}
}

func TestMetadataRouteMissing(t *testing.T) {
	for status, want := range map[int]bool{404: true, 405: true, 501: true, 400: false, 409: false, 412: false, 500: false} {
		if got := metadataRouteMissing(&gpupaas.APIError{StatusCode: status}); got != want {
			t.Errorf("status %d: %v, want %v", status, got, want)
		}
	}
	if metadataRouteMissing(nil) || metadataRouteMissing(errors.New("not an API error")) {
		t.Error("an error that is not an API error was taken for a missing route")
	}
}

// --- ModifyPlan wiring --------------------------------------------------------

// typedNulls gives the maps of a hand-built model their element type, as a decoded plan has.
func typedNulls(m mksResourceModel) mksResourceModel {
	null := types.MapNull(types.StringType)
	if m.Metadata.Labels.ElementType(context.Background()) == nil {
		m.Metadata.Labels = null
	}
	if m.Metadata.Annotations.ElementType(context.Background()) == nil {
		m.Metadata.Annotations = null
	}
	m.Metadata.CreatedBy, m.Metadata.ModifiedBy = userMetaFromSDK(nil), userMetaFromSDK(nil)
	pools := make([]mksWorkerPoolModel, len(m.Spec.WorkerPools))
	copy(pools, m.Spec.WorkerPools)
	for i := range pools {
		if pools[i].Labels.ElementType(context.Background()) == nil {
			pools[i].Labels = null
		}
		if pools[i].Annotations.ElementType(context.Background()) == nil {
			pools[i].Annotations = null
		}
	}
	m.Spec.WorkerPools = pools
	return m
}

func modifyPlan(t *testing.T, plan mksResourceModel, state *mksResourceModel) diag.Diagnostics {
	t.Helper()
	ctx := context.Background()
	plan = typedNulls(plan)
	if state != nil {
		s := typedNulls(*state)
		state = &s
	}
	r := &mksClusterResource{pd: &client.ProviderData{Clientset: client.NewFakeClientset()}}
	var sch resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &sch)
	raw := func(m *mksResourceModel) tftypes.Value {
		st := tfsdk.State{Schema: sch.Schema, Raw: tftypes.NewValue(sch.Schema.Type().TerraformType(ctx), nil)}
		if m == nil {
			return st.Raw
		}
		if d := st.Set(ctx, m); d.HasError() {
			t.Fatalf("building the plan: %v", d)
		}
		return st.Raw
	}
	req := resource.ModifyPlanRequest{Plan: tfsdk.Plan{Schema: sch.Schema, Raw: raw(&plan)}, State: tfsdk.State{Schema: sch.Schema, Raw: raw(state)}}
	var resp resource.ModifyPlanResponse
	r.ModifyPlan(ctx, req, &resp)
	return resp.Diagnostics
}

func TestModifyPlanRunsTheChecksOfTheReference(t *testing.T) {
	state := plannedModel()
	state.Spec.ControlPlane.OSImage = str("ubuntu-24")
	state.Spec.WorkerPools[0].NodeCount = types.Int64Value(3)

	// An image change warns that the pool is re-imaged, and does not fail.
	plan := state
	cp := *state.Spec.ControlPlane
	cp.OSImage = str("ubuntu-26")
	plan.Spec.ControlPlane = &cp
	d := modifyPlan(t, plan, &state)
	if d.HasError() || !strings.Contains(diagText(d, false), "re-images the pool") {
		t.Errorf("image change: errors %q warnings %q", diagText(d, true), diagText(d, false))
	}

	// The control plane cannot be resized.
	plan = state
	cp = *state.Spec.ControlPlane
	cp.NodeCount = types.Int64Value(3)
	plan.Spec.ControlPlane = &cp
	if d := modifyPlan(t, plan, &state); !strings.Contains(diagText(d, true), "can't be changed") {
		t.Errorf("control plane resize: %q", diagText(d, true))
	}

	// A new cluster with two control-plane nodes is refused, and with an inline key.
	bad := plannedModel()
	bad.Spec.ControlPlane.NodeCount = types.Int64Value(2)
	bad.Spec.ControlPlane.SSHKey = str("ssh-ed25519 AAAA me")
	got := diagText(modifyPlan(t, bad, nil), true)
	if !strings.Contains(got, "1 node, or 3") || !strings.Contains(got, "name of an SSH key") {
		t.Errorf("create: %q", got)
	}

	// The same problems in a cluster that already has them only warn, so that an unrelated update goes through.
	old := plannedModel()
	old.Spec.ControlPlane.NodeCount = types.Int64Value(5)
	d = modifyPlan(t, old, &old)
	if d.HasError() || !strings.Contains(diagText(d, false), "not blocking this plan") {
		t.Errorf("an existing cluster with 5 control-plane nodes: errors %q warnings %q", diagText(d, true), diagText(d, false))
	}
}
