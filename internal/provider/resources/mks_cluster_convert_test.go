package resources

import (
	"encoding/json"
	"testing"

	apiv1 "github.com/gpupaas-ai/gpupaas-go/apis/v1alpha1"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func str(s string) types.String { return types.StringValue(s) }

func serverCluster() *apiv1.MKSCluster {
	return &apiv1.MKSCluster{
		Metadata: apiv1.ObjectMeta{Name: "c1", Project: "p", Workspace: "ws"},
		Version:  7,
		Spec: apiv1.MKSClusterSpec{
			Profile:           &apiv1.MKSProfileRef{Name: "prof"},
			KubernetesVersion: "v1.36.4",
			Location:          "dc",
			Blueprint:         &apiv1.MKSBlueprint{Name: "minimal", Version: "latest"},
			Networking:        &apiv1.MKSNetworking{VPC: "default", Subnet: "default", PodSubnet: "10.244.0.0/16", ServiceSubnet: "10.96.0.0/12", CNI: &apiv1.MKSCNI{Name: "Calico", Version: "3.32.2"}},
			ControlPlane: &apiv1.MKSNodeGroup{
				Name: "control-plane", OSImage: "ubuntu-26", SKUName: "bm-1", SKUType: "BMAAS", SSHKey: "mine",
				ComputeType: "GPU", SKUDisplayName: "custom", Shape: map[string]any{"gpu": map[string]any{"count": 1}},
				Scaling: &apiv1.MKSScaling{Mode: "manual", Count: 1},
				Extra:   map[string]json.RawMessage{"serverOnly": json.RawMessage(`"keep"`)},
			},
			WorkerPools: []apiv1.MKSNodeGroup{{Name: "a", SKUName: "bm-1", OSImage: "ubuntu-26", Scaling: &apiv1.MKSScaling{Mode: "manual", Count: 2}}},
			Sharing:     &apiv1.DevSharingSpec{ShareMode: "None"},
			Billing:     &apiv1.MKSBilling{Variables: map[string]string{"ContractTerm": "0", "Other": "x"}},
			Extra:       map[string]json.RawMessage{"futureField": json.RawMessage(`{"a":1}`)},
		},
		Status: apiv1.MKSClusterStatus{Status: "MKS_CLUSTER_STATUS_RUNNING", Reason: "Cluster is ready", Action: "deploy", Output: &apiv1.MKSClusterOutput{ClusterIDEdgesrv: "c1"}},
	}
}

func plannedModel() mksResourceModel {
	return mksResourceModel{
		Metadata: MetadataModel{Name: str("c1"), Project: str("p"), Workspace: str("ws")},
		Spec: mksSpecModel{
			ProfileName:       str("prof"),
			KubernetesVersion: str("1.36"),
			ControlPlane:      &mksControlPlaneModel{Name: str("control-plane"), NodeCount: types.Int64Value(1), ScalingMode: str("manual"), SKUName: str("bm-1")},
			WorkerPools:       []mksWorkerPoolModel{{Name: str("a"), NodeCount: types.Int64Value(3), ScalingMode: str("manual")}},
			Tags:              types.MapNull(types.StringType),
		},
	}
}

func TestBuildMKSClusterOverlaysPlanOnCurrent(t *testing.T) {
	cur := serverCluster()
	obj := buildMKSCluster(plannedModel(), cur)

	if obj.Version != 7 {
		t.Errorf("revision = %d, want the current one echoed back", obj.Version)
	}
	s := obj.Spec
	// The short configured version is not sent when the cluster already runs a matching one.
	if s.KubernetesVersion != "v1.36.4" {
		t.Errorf("version = %q, want the running v1.36.4 kept", s.KubernetesVersion)
	}
	// Fields the user did not configure come from the current cluster.
	if s.Location != "dc" || s.Networking.CNI.Name != "Calico" || s.Blueprint.Name != "minimal" {
		t.Errorf("defaults lost: %#v", s)
	}
	if s.Sharing == nil || s.Billing.Variables["Other"] != "x" || string(s.Extra["futureField"]) != `{"a":1}` {
		t.Errorf("unmanaged fields lost: sharing=%v billing=%v extra=%v", s.Sharing, s.Billing, s.Extra)
	}
	cp := s.ControlPlane
	if cp.ComputeType != "GPU" || cp.Shape == nil || cp.SSHKey != "mine" || string(cp.Extra["serverOnly"]) != `"keep"` {
		t.Errorf("control plane lost derived fields: %#v", cp)
	}
	if len(s.WorkerPools) != 1 || s.WorkerPools[0].Scaling.Count != 3 || s.WorkerPools[0].OSImage != "ubuntu-26" {
		t.Errorf("pool overlay: %#v", s.WorkerPools)
	}
	// The current cluster object must not be modified by building the request.
	if cur.Spec.WorkerPools[0].Scaling.Count != 2 {
		t.Error("buildMKSCluster changed the current cluster")
	}
}

func TestSameRequestIgnoresClientSideChanges(t *testing.T) {
	cur := serverCluster()
	cur.Metadata.Labels = map[string]string{"paas.envmgmt.io/system": "x"} // a label the platform added
	same := func() mksResourceModel {
		m := plannedModel()
		m.Spec.WorkerPools[0].NodeCount = types.Int64Value(2) // what the platform holds
		return m
	}
	// The plan equals what the platform holds: nothing to send.
	if !sameRequest(buildMKSCluster(same(), cur), cur) {
		t.Fatal("an unchanged plan must not be sent")
	}
	// Only force_destroy and timeouts differ: still nothing to send.
	plan := same()
	plan.ForceDestroy = types.BoolValue(true)
	plan.Timeouts = &mksTimeoutsModel{Create: str("1h"), Update: types.StringNull(), Delete: types.StringNull()}
	if !sameRequest(buildMKSCluster(plan, cur), cur) {
		t.Fatal("client-side settings must not cause a request")
	}
	// A real change is sent.
	plan = same()
	plan.Spec.WorkerPools[0].NodeCount = types.Int64Value(5)
	if sameRequest(buildMKSCluster(plan, cur), cur) {
		t.Fatal("a pool resize must be sent")
	}
	plan = same()
	plan.Spec.Tags = mapFromStringMap(map[string]string{"team": "ml"})
	if sameRequest(buildMKSCluster(plan, cur), cur) {
		t.Fatal("a tag change must be sent")
	}
	plan = same()
	plan.Metadata.Labels = mapFromStringMap(map[string]string{"a": "b"})
	if sameRequest(buildMKSCluster(plan, cur), cur) {
		t.Fatal("a label change must be sent")
	}
}

func TestBuildMKSClusterUpgradeSendsNewVersion(t *testing.T) {
	plan := plannedModel()
	plan.Spec.KubernetesVersion = str("1.37")
	obj := buildMKSCluster(plan, serverCluster())
	if obj.Spec.KubernetesVersion != "1.37" {
		t.Errorf("version = %q", obj.Spec.KubernetesVersion)
	}
}

func TestBuildMKSClusterSKUChangeClearsDerivedFields(t *testing.T) {
	plan := plannedModel()
	plan.Spec.ControlPlane.SKUName = str("bm-2")
	cp := buildMKSCluster(plan, serverCluster()).Spec.ControlPlane
	if cp.SKUName != "bm-2" || cp.ComputeType != "" || cp.Shape != nil || cp.SKUDisplayName != "" {
		t.Errorf("derived fields must be cleared so the platform re-derives them: %#v", cp)
	}
}

func TestBuildMKSClusterRemovesPoolsAndTags(t *testing.T) {
	plan := plannedModel()
	plan.Spec.WorkerPools = nil
	cur := serverCluster()
	cur.Spec.Tags = map[string]string{"team": "ml"}
	s := buildMKSCluster(plan, cur).Spec
	if len(s.WorkerPools) != 0 || len(s.Tags) != 0 {
		t.Errorf("pools=%d tags=%v", len(s.WorkerPools), s.Tags)
	}
	out, _ := json.Marshal(s)
	var m map[string]json.RawMessage
	_ = json.Unmarshal(out, &m)
	if string(m["workerPools"]) != "[]" {
		t.Errorf("an empty pool list must be sent explicitly: %s", out)
	}
}

func TestBuildMKSClusterCreateSendsOnlyConfiguredFields(t *testing.T) {
	plan := plannedModel()
	plan.Spec.ControlPlane.SKUName = types.StringNull()
	plan.Spec.WorkerPools = nil
	obj := buildMKSCluster(plan, nil)
	if obj.Version != 0 || obj.Spec.ControlPlane.SKUName != "" || obj.Spec.Location != "" || obj.Spec.Networking != nil || obj.Spec.Sharing != nil {
		t.Errorf("create must leave defaults to the platform: %#v", obj.Spec)
	}
	if obj.Spec.ControlPlane.Scaling == nil || obj.Spec.ControlPlane.Scaling.Count != 1 || obj.Spec.ControlPlane.Scaling.Mode != "manual" {
		t.Errorf("scaling: %#v", obj.Spec.ControlPlane.Scaling)
	}
}

func TestMKSModelKeepsConfiguredValuesAndHidesServerDefaults(t *testing.T) {
	plan := plannedModel()
	plan.Spec.ControlPlane.SKUName = types.StringNull() // not configured: the profile default applies
	got := mksClusterModelFromServer(plan, serverCluster(), nil, false)

	if got.Spec.KubernetesVersion.ValueString() != "1.36" {
		t.Errorf("configured short version replaced: %q", got.Spec.KubernetesVersion.ValueString())
	}
	if got.Status.KubernetesVersion.ValueString() != "v1.36.4" {
		t.Errorf("exact version = %q", got.Status.KubernetesVersion.ValueString())
	}
	if !got.Spec.ControlPlane.SKUName.IsNull() || !got.Spec.ControlPlane.OSImage.IsNull() {
		t.Errorf("server defaults leaked into optional attributes: %#v", got.Spec.ControlPlane)
	}
	if got.Spec.ControlPlane.ComputeType.ValueString() != "GPU" || got.Spec.Location.ValueString() != "dc" || got.Spec.ContractTerm.ValueString() != "0" {
		t.Errorf("computed attributes: %#v", got.Spec)
	}
	if got.ID.ValueString() != "p/ws/c1" || got.Status.Phase.ValueString() != "running" || got.Status.ClusterID.ValueString() != "c1" {
		t.Errorf("identity/status: %v %v %v", got.ID, got.Status.Phase, got.Status.ClusterID)
	}
}

func TestMKSModelReportsDrift(t *testing.T) {
	plan := plannedModel()
	plan.Spec.ControlPlane.SKUName = str("bm-1")
	srv := serverCluster()
	srv.Spec.ControlPlane.SKUName = "bm-9" // changed outside Terraform
	srv.Spec.KubernetesVersion = "v1.35.2" // upgraded outside Terraform
	got := mksClusterModelFromServer(plan, srv, nil, true)
	if got.Spec.ControlPlane.SKUName.ValueString() != "bm-9" {
		t.Errorf("sku drift not reported: %v", got.Spec.ControlPlane.SKUName)
	}
	if got.Spec.KubernetesVersion.ValueString() != "v1.35.2" {
		t.Errorf("version drift not reported: %v", got.Spec.KubernetesVersion)
	}
}

func TestMKSModelPoolsMatchByNameAndSurfaceExtras(t *testing.T) {
	plan := plannedModel()
	plan.Spec.WorkerPools = []mksWorkerPoolModel{
		{Name: str("gone"), NodeCount: types.Int64Value(1), ScalingMode: str("manual")}, // deleted outside Terraform
		{Name: str("a"), NodeCount: types.Int64Value(2), ScalingMode: str("manual")},
	}
	srv := serverCluster()
	srv.Spec.WorkerPools = append(srv.Spec.WorkerPools, apiv1.MKSNodeGroup{Name: "added", Scaling: &apiv1.MKSScaling{Mode: "manual", Count: 1}})
	got := mksClusterModelFromServer(plan, srv, nil, true).Spec.WorkerPools
	if len(got) != 2 || got[0].Name.ValueString() != "a" || got[1].Name.ValueString() != "added" {
		t.Fatalf("pools = %#v", got)
	}
}

func TestMKSModelImportTakesServerValues(t *testing.T) {
	base := mksResourceModel{Metadata: MetadataModel{Name: str("c1"), Project: str("p"), Workspace: str("ws")}}
	got := mksClusterModelFromServer(base, serverCluster(), nil, true).Spec
	if got.ProfileName.ValueString() != "prof" || got.ControlPlane.SKUName.ValueString() != "bm-1" || got.ControlPlane.OSImage.ValueString() != "ubuntu-26" || got.ControlPlane.SSHKey.ValueString() != "mine" {
		t.Errorf("import control plane: %#v", got.ControlPlane)
	}
	if len(got.WorkerPools) != 1 || got.WorkerPools[0].SKUName.ValueString() != "bm-1" {
		t.Errorf("import pools: %#v", got.WorkerPools)
	}
}

func TestSameK8sVersion(t *testing.T) {
	cases := []struct {
		want, got string
		ok        bool
	}{
		{"1.36", "v1.36.4", true}, {"v1.36", "1.36.4", true}, {"1.36.4", "v1.36.4", true},
		{"1.36", "v1.360.1", false}, {"1.3", "v1.36.4", false}, {"1.37", "v1.36.4", false}, {"", "v1.36.4", false},
	}
	for _, c := range cases {
		if sameK8sVersion(c.want, c.got) != c.ok {
			t.Errorf("sameK8sVersion(%q, %q) = %v", c.want, c.got, !c.ok)
		}
	}
}

func TestExpectedNodes(t *testing.T) {
	if n := expectedNodes(serverCluster().Spec); n != 3 {
		t.Errorf("expectedNodes = %d, want 3", n)
	}
}

func profileForDefaults() *apiv1.MKSClusterProfile {
	return &apiv1.MKSClusterProfile{Spec: apiv1.MKSClusterProfileSpec{
		ControlPlane:             &apiv1.MKSGroupProfile{DefaultImage: "ubuntu-26", DefaultSKUName: "bm-1", SKUTypes: []string{"BMAAS"}},
		Workers:                  &apiv1.MKSGroupProfile{DefaultImage: "ubuntu-24", DefaultSKUName: "bm-2", SKUTypes: []string{"BMAAS", "VMAAS"}},
		Datacenters:              []string{"dc1"},
		SupportedCNIs:            []apiv1.MKSCNI{{Name: "Calico", Version: "3.32.2"}, {Name: "Cilium", Version: "1.15.7"}},
		DefaultCNI:               &apiv1.MKSCNI{Name: "Calico", Version: "3.32.2"},
		DefaultBlueprint:         &apiv1.MKSBlueprint{Name: "minimal", Version: "latest"},
		DefaultKubernetesVersion: "1.36",
		DefaultPodSubnet:         "10.244.0.0/16",
		DefaultServiceSubnet:     "10.96.0.0/12",
		DefaultSSHKey:            "mine",
	}}
}

func TestFillProfileDefaultsCompletesAMinimalSpec(t *testing.T) {
	spec := apiv1.MKSClusterSpec{
		Profile:      &apiv1.MKSProfileRef{Name: "p"},
		ControlPlane: &apiv1.MKSNodeGroup{Name: "control-plane", Scaling: &apiv1.MKSScaling{Mode: "manual", Count: 1}},
		WorkerPools:  []apiv1.MKSNodeGroup{{Name: "w", Scaling: &apiv1.MKSScaling{Count: 2}}},
	}
	fillProfileDefaults(&spec, profileForDefaults(), nil, nil)

	if spec.KubernetesVersion != "1.36" || spec.Location != "dc1" || spec.Blueprint.Name != "minimal" {
		t.Errorf("basics: %#v", spec)
	}
	if spec.Networking.CNI == nil || spec.Networking.CNI.Name != "Calico" || spec.Networking.CNI.Version != "3.32.2" || spec.Networking.PodSubnet != "10.244.0.0/16" || spec.Networking.ServiceSubnet != "10.96.0.0/12" {
		t.Errorf("networking: %#v", spec.Networking)
	}
	cp := spec.ControlPlane
	if cp.Datacenter != "dc1" || cp.OSImage != "ubuntu-26" || cp.SKUName != "bm-1" || cp.SKUType != "BMAAS" || cp.SSHKey != "mine" {
		t.Errorf("control plane: %#v", cp)
	}
	w := spec.WorkerPools[0]
	// Two SKU types are allowed for workers, so none is guessed.
	if w.OSImage != "ubuntu-24" || w.SKUName != "bm-2" || w.SKUType != "" {
		t.Errorf("worker pool: %#v", w)
	}
}

func TestFillProfileDefaultsNeverOverridesConfiguredValues(t *testing.T) {
	spec := apiv1.MKSClusterSpec{
		KubernetesVersion: "1.35",
		Location:          "mine-dc",
		Blueprint:         &apiv1.MKSBlueprint{Name: "custom"},
		Networking:        &apiv1.MKSNetworking{PodSubnet: "192.168.0.0/16", CNI: &apiv1.MKSCNI{Name: "Cilium"}},
		ControlPlane:      &apiv1.MKSNodeGroup{OSImage: "ubuntu-22", SKUName: "bm-9", SKUType: "VMAAS", SSHKey: "other", Datacenter: "dc9"},
	}
	fillProfileDefaults(&spec, profileForDefaults(), nil, nil)
	if spec.KubernetesVersion != "1.35" || spec.Location != "mine-dc" || spec.Blueprint.Name != "custom" || spec.Blueprint.Version != "latest" {
		t.Errorf("configured values changed: %#v", spec)
	}
	if spec.Networking.PodSubnet != "192.168.0.0/16" || spec.Networking.ServiceSubnet != "10.96.0.0/12" || spec.Networking.CNI.Name != "Cilium" || spec.Networking.CNI.Version != "1.15.7" {
		t.Errorf("networking: %#v", spec.Networking)
	}
	cp := spec.ControlPlane
	if cp.OSImage != "ubuntu-22" || cp.SKUName != "bm-9" || cp.SKUType != "VMAAS" || cp.SSHKey != "other" || cp.Datacenter != "dc9" {
		t.Errorf("control plane changed: %#v", cp)
	}
}

func TestFillProfileDefaultsLeavesLocationWhenProfileHasSeveralDatacenters(t *testing.T) {
	p := profileForDefaults()
	p.Spec.Datacenters = []string{"a", "b"}
	spec := apiv1.MKSClusterSpec{ControlPlane: &apiv1.MKSNodeGroup{}}
	fillProfileDefaults(&spec, p, nil, nil)
	if spec.Location != "" || spec.ControlPlane.Datacenter != "" {
		t.Errorf("a datacenter was guessed: %q %q", spec.Location, spec.ControlPlane.Datacenter)
	}
	fillProfileDefaults(&spec, nil, nil, nil) // no profile: nothing to do, no panic
}

func TestFillProfileDefaultsSkipsADefaultSSHKeyThatDoesNotExist(t *testing.T) {
	for _, tc := range []struct {
		name   string
		exists func(string) bool
		want   string
	}{
		{"missing key is not used", func(string) bool { return false }, ""},
		{"existing key is used", func(n string) bool { return n == "mine" }, "mine"},
	} {
		spec := apiv1.MKSClusterSpec{ControlPlane: &apiv1.MKSNodeGroup{}, WorkerPools: []apiv1.MKSNodeGroup{{Name: "w"}}}
		fillProfileDefaults(&spec, profileForDefaults(), tc.exists, nil)
		if spec.ControlPlane.SSHKey != tc.want || spec.WorkerPools[0].SSHKey != tc.want {
			t.Errorf("%s: control plane %q, pool %q, want %q", tc.name, spec.ControlPlane.SSHKey, spec.WorkerPools[0].SSHKey, tc.want)
		}
	}
	// A key the user configured is never replaced or checked.
	spec := apiv1.MKSClusterSpec{ControlPlane: &apiv1.MKSNodeGroup{SSHKey: "mine-2"}}
	fillProfileDefaults(&spec, profileForDefaults(), func(string) bool { return false }, nil)
	if spec.ControlPlane.SSHKey != "mine-2" {
		t.Errorf("configured key changed: %q", spec.ControlPlane.SSHKey)
	}
}

func TestFillProfileDefaultsFallsBackToStandardSubnetsWhenTheProfileHasNone(t *testing.T) {
	p := profileForDefaults()
	p.Spec.DefaultPodSubnet, p.Spec.DefaultServiceSubnet = "", ""
	spec := apiv1.MKSClusterSpec{ControlPlane: &apiv1.MKSNodeGroup{}}
	fillProfileDefaults(&spec, p, nil, nil)
	if spec.Networking.PodSubnet != "10.244.0.0/16" || spec.Networking.ServiceSubnet != "10.96.0.0/12" {
		t.Errorf("networking: %#v", spec.Networking)
	}
	// Configured values still win over the fallback.
	spec = apiv1.MKSClusterSpec{Networking: &apiv1.MKSNetworking{PodSubnet: "192.168.0.0/16"}, ControlPlane: &apiv1.MKSNodeGroup{}}
	fillProfileDefaults(&spec, p, nil, nil)
	if spec.Networking.PodSubnet != "192.168.0.0/16" {
		t.Errorf("configured pod subnet replaced: %q", spec.Networking.PodSubnet)
	}
}

// liveShapedCluster is a healthy one-node cluster as narayana-tb1 returns it: the control-plane
// group is named through displayName only and holds no datacenter of its own (the cluster has the
// location), no worker pools, billing and sharing present.
func liveShapedCluster() *apiv1.MKSCluster {
	return &apiv1.MKSCluster{
		Metadata: apiv1.ObjectMeta{Name: "tf-mks-2", Project: "system-catalog"},
		Version:  5,
		Spec: apiv1.MKSClusterSpec{
			Profile:           &apiv1.MKSProfileRef{Name: "mks-oneclick-1"},
			KubernetesVersion: "v1.36.4",
			Location:          "default-dc",
			Blueprint:         &apiv1.MKSBlueprint{Name: "minimal", Version: "latest"},
			Networking: &apiv1.MKSNetworking{VPC: "default", Subnet: "default", PodSubnet: "10.244.0.0/16", ServiceSubnet: "10.96.0.0/12",
				CNI: &apiv1.MKSCNI{Name: "Calico", Version: "3.32.2"}},
			ControlPlane: &apiv1.MKSNodeGroup{
				DisplayName: "control-plane", OSImage: "ubuntu-26", SKUName: "bmaas-test-1", SKUType: "BMAAS", SSHKey: "mine",
				ComputeType: "GPU", SKUDisplayName: "custom", SKUDisplayText: "custom",
				Shape:   map[string]any{"gpu": map[string]any{"count": 1, "model": "custom"}},
				Scaling: &apiv1.MKSScaling{Mode: "manual", Count: 1},
			},
			Billing: &apiv1.MKSBilling{Variables: map[string]string{"ContractTerm": "0"}},
			Sharing: &apiv1.DevSharingSpec{ShareMode: "None"},
		},
		Status: apiv1.MKSClusterStatus{Status: "MKS_CLUSTER_STATUS_RUNNING", Reason: "Cluster is ready", Action: "deploy"},
	}
}

// An imported cluster managed with a minimal configuration must not be re-applied: the plan equals
// what the platform holds. Found on a dry run against narayana-tb1, where the provider would have
// posted the same cluster again with a name and a datacenter on the control-plane group that the
// API does not hold.
func TestUnchangedLiveShapedClusterIsNotReapplied(t *testing.T) {
	cur := liveShapedCluster()
	plan := mksResourceModel{
		Metadata: MetadataModel{Name: str("tf-mks-2"), Project: str("system-catalog")},
		Spec: mksSpecModel{
			ProfileName:       str("mks-oneclick-1"),
			KubernetesVersion: str("1.36"),
			Location:          str("default-dc"), // carried from state
			// Name and scaling mode have schema defaults, so the plan always holds them.
			ControlPlane: &mksControlPlaneModel{Name: str("control-plane"), NodeCount: types.Int64Value(1), ScalingMode: str("manual"), SSHKey: str("mine")},
			Tags:         types.MapNull(types.StringType),
		},
	}
	obj := buildMKSCluster(plan, cur)
	fillProfileDefaults(&obj.Spec, profileForDefaults(), func(string) bool { return true }, &cur.Spec)
	if !sameRequest(obj, cur) {
		want, _ := json.Marshal(obj.Spec)
		have, _ := json.Marshal(cur.Spec)
		t.Fatalf("an unchanged cluster would be re-applied\nwant %s\nhave %s", want, have)
	}

	// A pool added in the same update still gets the profile's defaults.
	plan.Spec.WorkerPools = []mksWorkerPoolModel{{Name: str("a"), NodeCount: types.Int64Value(1), ScalingMode: str("manual")}}
	obj = buildMKSCluster(plan, cur)
	fillProfileDefaults(&obj.Spec, profileForDefaults(), func(string) bool { return true }, &cur.Spec)
	if sameRequest(obj, cur) {
		t.Fatal("a new pool must be sent")
	}
	if w := obj.Spec.WorkerPools[0]; w.OSImage != "ubuntu-24" || w.SKUName != "bm-2" || w.Datacenter != "default-dc" {
		t.Fatalf("new pool did not get the profile defaults: %#v", w)
	}
	if cp := obj.Spec.ControlPlane; cp.Datacenter != "" || cp.Name != "" {
		t.Fatalf("the existing control plane was filled in: %#v", cp)
	}
}
