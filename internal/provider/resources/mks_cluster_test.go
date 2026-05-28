package resources

import (
	"context"
	"testing"

	gpupaas "github.com/gpupaas-ai/gpupaas-go"
	apiv1 "github.com/gpupaas-ai/gpupaas-go/apis/v1alpha1"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/gpupaas-ai/terraform-provider-gpupaas/internal/client"
)

// fullMKSClusterModel returns a resource model with every spec field populated
// so the round-trip exercises all converters.
func fullMKSClusterModel() mksClusterResourceModel {
	return mksClusterResourceModel{
		Metadata: MetadataModel{
			Name:    types.StringValue("cluster-1"),
			Project: types.StringValue("demo"),
		},
		Spec: mksClusterSpecModel{
			KubernetesVersion:     types.StringValue("1.31"),
			PlatformVersion:       types.StringValue("p1"),
			CNI:                   types.StringValue("calico"),
			CNIVersion:            types.StringValue("v3"),
			OS:                    types.StringValue("ubuntu22.04"),
			HAEnabled:             types.BoolValue(true),
			DedicatedControlPlane: types.BoolValue(false),
			Location:              types.StringValue("us-west"),
			Blueprint:             &mksBlueprintModel{Name: types.StringValue("minimal"), Version: types.StringValue("v1")},
			Networking: &mksNetworkingModel{
				VPC:            types.StringValue("vpc-1"),
				Subnet:         types.StringValue("subnet-1"),
				PodCIDR:        types.StringValue("192.168.0.0/16"),
				ServiceCIDR:    types.StringValue("10.96.0.0/12"),
				IPFamily:       types.StringValue("IPv4"),
				SecurityGroups: listFromStringSlice([]string{"sg-1", "sg-2"}),
			},
			Proxy: &mksProxyModel{
				HTTPProxy:    types.StringValue("http://proxy:3128"),
				TLSTerminate: types.BoolValue(true),
			},
			Storage: &mksStorageModel{
				Block: &mksStorageBackendModel{
					Type:       types.StringValue("ceph"),
					AccessMode: types.StringValue("ReadWriteOnce"),
					Config:     mapFromStringMap(map[string]string{"pool": "rbd"}),
				},
				DefaultStorageClass: types.StringValue("block"),
			},
			Tags: mapFromStringMap(map[string]string{"team": "a"}),
			ControlPlaneNodeGroup: &mksNodeGroupModel{
				ID:         types.StringValue("cp-1"),
				SKU:        types.StringValue("m5.large"),
				NodeCount:  types.Int64Value(3),
				PublicIP:   types.BoolValue(false),
				NodeLabels: mapFromStringMap(map[string]string{"role": "cp"}),
				KubeletConfig: &mksKubeletConfigModel{
					KeyValue: mapFromStringMap(map[string]string{"maxPods": "110"}),
					YAML:     types.StringValue("kind: KubeletConfiguration"),
				},
			},
			WorkerNodeGroups: []mksNodeGroupModel{{
				ID:           types.StringValue("ng-1"),
				SKU:          types.StringValue("g4dn.xlarge"),
				ScalingMode:  types.StringValue("auto"),
				NodeCount:    types.Int64Value(2),
				MinNodes:     types.Int64Value(1),
				MaxNodes:     types.Int64Value(5),
				DesiredNodes: types.Int64Value(2),
				PublicIP:     types.BoolValue(true),
			}},
			Nodes: []mksNodeSpecModel{{
				Hostname:        types.StringValue("master-1"),
				Roles:           listFromStringSlice([]string{"master", "worker"}),
				SSHUserName:     types.StringValue("ubuntu"),
				SSHPort:         types.Int64Value(22),
				PrivateIP:       types.StringValue("10.0.0.10"),
				Arch:            types.StringValue("amd64"),
				OperatingSystem: types.StringValue("ubuntu22.04"),
				NodeLabels:      mapFromStringMap(map[string]string{"zone": "a"}),
			}},
		},
	}
}

func TestMKSClusterRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	var diags diag.Diagnostics

	in := fullMKSClusterModel()
	sdk := mksClusterModelToSDK(ctx, in, &diags)
	if diags.HasError() {
		t.Fatalf("modelToSDK diags: %v", diags)
	}

	if sdk.APIVersion != apiv1.APIVersion || sdk.Kind != apiv1.KindMKSCluster {
		t.Fatalf("unexpected TypeMeta: %s/%s", sdk.APIVersion, sdk.Kind)
	}
	if sdk.Metadata.Project != "demo" || sdk.Metadata.Name != "cluster-1" {
		t.Fatalf("unexpected metadata: %+v", sdk.Metadata)
	}
	if sdk.Spec.HAEnabled == nil || !*sdk.Spec.HAEnabled {
		t.Errorf("ha_enabled lost: %+v", sdk.Spec.HAEnabled)
	}

	got := mksClusterSDKToModel(sdk, "demo")

	if got.Spec.KubernetesVersion.ValueString() != "1.31" {
		t.Errorf("kubernetes_version lost: %q", got.Spec.KubernetesVersion.ValueString())
	}
	if got.Spec.Blueprint == nil || got.Spec.Blueprint.Name.ValueString() != "minimal" {
		t.Errorf("blueprint lost: %+v", got.Spec.Blueprint)
	}
	if got.Spec.Networking == nil {
		t.Fatalf("networking lost")
	}
	sgs := stringSliceFromTF(ctx, got.Spec.Networking.SecurityGroups, &diags)
	if len(sgs) != 2 || sgs[0] != "sg-1" {
		t.Errorf("security_groups lost: %v", sgs)
	}
	if got.Spec.Storage == nil || got.Spec.Storage.Block == nil ||
		got.Spec.Storage.Block.Type.ValueString() != "ceph" {
		t.Errorf("storage block lost: %+v", got.Spec.Storage)
	}
	if got.Spec.ControlPlaneNodeGroup == nil ||
		got.Spec.ControlPlaneNodeGroup.NodeCount.ValueInt64() != 3 {
		t.Errorf("control plane node group lost: %+v", got.Spec.ControlPlaneNodeGroup)
	}
	if got.Spec.ControlPlaneNodeGroup.KubeletConfig == nil ||
		got.Spec.ControlPlaneNodeGroup.KubeletConfig.YAML.ValueString() != "kind: KubeletConfiguration" {
		t.Errorf("kubelet config lost: %+v", got.Spec.ControlPlaneNodeGroup.KubeletConfig)
	}
	if len(got.Spec.WorkerNodeGroups) != 1 || got.Spec.WorkerNodeGroups[0].MaxNodes.ValueInt64() != 5 {
		t.Errorf("worker node groups lost: %+v", got.Spec.WorkerNodeGroups)
	}
	if len(got.Spec.Nodes) != 1 || got.Spec.Nodes[0].PrivateIP.ValueString() != "10.0.0.10" {
		t.Errorf("nodes lost: %+v", got.Spec.Nodes)
	}
}

func TestMKSClusterEmptyNestedRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	var diags diag.Diagnostics

	in := mksClusterResourceModel{
		Metadata: MetadataModel{Name: types.StringValue("c-min"), Project: types.StringValue("demo")},
		Spec:     mksClusterSpecModel{KubernetesVersion: types.StringValue("1.31")},
	}
	sdk := mksClusterModelToSDK(ctx, in, &diags)
	got := mksClusterSDKToModel(sdk, "demo")
	if diags.HasError() {
		t.Fatalf("diags: %v", diags)
	}
	if got.Spec.Blueprint != nil || got.Spec.Networking != nil || got.Spec.Proxy != nil ||
		got.Spec.Storage != nil || got.Spec.ControlPlaneNodeGroup != nil {
		t.Fatalf("nil nested blocks should stay nil, got %+v", got.Spec)
	}
	if len(got.Spec.WorkerNodeGroups) != 0 || len(got.Spec.Nodes) != 0 {
		t.Fatalf("empty slices should stay empty, got %+v", got.Spec)
	}
}

func TestMKSClusterStatusRoundTrip(t *testing.T) {
	t.Parallel()
	status := apiv1.MKSClusterStatus{
		Condition:       "MKS_CLUSTER_STATUS_RUNNING",
		ConditionReason: "ready",
		Action:          "upgrade",
		Output: &apiv1.MKSClusterOutput{
			APIServerEndpoint: "https://api:6443",
			ClusterIDEdgesrv:  "edge-123",
		},
	}
	got := mksClusterStatusFromSDK(status)
	if got.Condition.ValueString() != "MKS_CLUSTER_STATUS_RUNNING" {
		t.Errorf("condition lost: %q", got.Condition.ValueString())
	}
	if got.Output == nil || got.Output.APIServerEndpoint.ValueString() != "https://api:6443" {
		t.Fatalf("output lost: %+v", got.Output)
	}

	// Nil output must stay nil so Terraform diffs stay quiet.
	emptyOut := mksClusterStatusFromSDK(apiv1.MKSClusterStatus{Condition: "x"})
	if emptyOut.Output != nil {
		t.Fatalf("nil output should stay nil, got %+v", emptyOut.Output)
	}
}

func TestMKSClusterImportIDParser(t *testing.T) {
	t.Parallel()
	project, name, err := ParseProjectScopedImportID("demo/cluster-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if project != "demo" || name != "cluster-1" {
		t.Fatalf("parsed (%q,%q) want (demo,cluster-1)", project, name)
	}
	for _, bad := range []string{"cluster-1", "demo/", "/cluster-1", "demo/ws/cluster-1", ""} {
		if _, _, err := ParseProjectScopedImportID(bad); err == nil {
			t.Errorf("expected error for %q", bad)
		}
	}
}

func TestMKSClusterActionDispatchDecision(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name           string
		old, planned   types.String
		shouldDispatch bool
	}{
		{"unset-to-unset", types.StringNull(), types.StringNull(), false},
		{"unset-to-none", types.StringNull(), types.StringValue("none"), false},
		{"none-to-none", types.StringValue("none"), types.StringValue("none"), false},
		{"unset-to-upgrade", types.StringNull(), types.StringValue("upgrade"), true},
		{"upgrade-to-upgrade", types.StringValue("upgrade"), types.StringValue("upgrade"), false},
		{"upgrade-to-scale", types.StringValue("upgrade"), types.StringValue("scale_node_group"), true},
		{"scale-to-add", types.StringValue("scale_node_group"), types.StringValue("add_node_group"), true},
		{"add-to-remove", types.StringValue("add_node_group"), types.StringValue("remove_node_group"), true},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			oldA := normalizedActionValue(tc.old)
			newA := normalizedActionValue(tc.planned)
			got := newA != "" && newA != "none" && newA != oldA
			if got != tc.shouldDispatch {
				t.Fatalf("dispatch(old=%q, new=%q) = %v want %v", oldA, newA, got, tc.shouldDispatch)
			}
		})
	}
}

func TestMKSClusterActionPayloadExtractors(t *testing.T) {
	t.Parallel()
	if mksUpgradeRequest(nil) != nil || mksUpgradeRequest(&mksClusterActionInputs{}) != nil {
		t.Fatal("missing upgrade inputs must yield nil")
	}
	up := mksUpgradeRequest(&mksClusterActionInputs{Upgrade: &mksUpgradeInputs{
		K8sVersion: types.StringValue("1.32"),
	}})
	if up == nil || up.K8sVersion != "1.32" {
		t.Fatalf("upgrade request not propagated: %+v", up)
	}

	if mksScaleNodeGroupRequest(nil) != nil {
		t.Fatal("nil scale inputs must yield nil")
	}
	sc := mksScaleNodeGroupRequest(&mksClusterActionInputs{ScaleNodeGroup: &mksScaleNodeGroupInputs{
		NodeGroupName: types.StringValue("ng-1"),
		DesiredCount:  types.Int64Value(4),
	}})
	if sc == nil || sc.NodeGroupName != "ng-1" || sc.DesiredCount == nil || *sc.DesiredCount != 4 {
		t.Fatalf("scale request not propagated: %+v", sc)
	}
	if sc.MinCount != nil || sc.MaxCount != nil {
		t.Fatalf("unset counts must remain nil: %+v", sc)
	}

	if mksAddNodeGroup(nil) != nil || mksAddNodeGroup(&mksClusterActionInputs{}) != nil {
		t.Fatal("missing add node group must yield nil")
	}
	ng := mksAddNodeGroup(&mksClusterActionInputs{AddNodeGroup: &mksAddNodeGroupInputs{
		NodeGroup: &mksNodeGroupModel{ID: types.StringValue("ng-2"), NodeCount: types.Int64Value(3)},
	}})
	if ng == nil || ng.ID != "ng-2" || ng.NodeCount != 3 {
		t.Fatalf("add node group not propagated: %+v", ng)
	}

	if mksRemoveNodeGroupName(nil) != "" {
		t.Fatal("nil remove inputs must yield empty name")
	}
	if got := mksRemoveNodeGroupName(&mksClusterActionInputs{RemoveNodeGroup: &mksRemoveNodeGroupInputs{
		NodeGroupName: types.StringValue("ng-3"),
	}}); got != "ng-3" {
		t.Fatalf("remove node group name not propagated: %q", got)
	}
}

// TestMKSClusterActionsWithFake drives the imperative verbs through the fake
// clientset and asserts observable state changes.
func TestMKSClusterActionsWithFake(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cs := client.NewFakeClientset()
	cli := cs.V1alpha1().MKSClusters("demo")

	_, err := cli.Create(ctx, &apiv1.MKSCluster{
		Metadata: apiv1.ObjectMeta{Name: "c-1", Project: "demo"},
		Spec: apiv1.MKSClusterSpec{
			KubernetesVersion: "1.31",
			WorkerNodeGroups:  []apiv1.MKSNodeGroup{{ID: "ng-1", DesiredNodes: 2}},
		},
	}, gpupaas.CreateOptions{})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Upgrade.
	four := int32(4)
	out, err := cli.Upgrade(ctx, "c-1", &apiv1.MKSUpgradeRequest{K8sVersion: "1.32"}, gpupaas.ActionOptions{})
	if err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	if out.Spec.KubernetesVersion != "1.32" || out.Status.Action != "upgrade" {
		t.Fatalf("upgrade not applied: %+v / %q", out.Spec.KubernetesVersion, out.Status.Action)
	}

	// Scale ng-1.
	out, err = cli.ScaleNodeGroup(ctx, "c-1", &apiv1.MKSScaleNodeGroupRequest{
		NodeGroupName: "ng-1", DesiredCount: &four,
	}, gpupaas.ActionOptions{})
	if err != nil {
		t.Fatalf("scale: %v", err)
	}
	if out.Spec.WorkerNodeGroups[0].DesiredNodes != 4 || out.Status.Action != "scale_node_group" {
		t.Fatalf("scale not applied: %+v", out.Spec.WorkerNodeGroups)
	}

	// Add ng-2.
	out, err = cli.AddNodeGroup(ctx, "c-1", &apiv1.MKSNodeGroup{ID: "ng-2", DesiredNodes: 1}, gpupaas.ActionOptions{})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if len(out.Spec.WorkerNodeGroups) != 2 || out.Status.Action != "add_node_group" {
		t.Fatalf("add not applied: %+v", out.Spec.WorkerNodeGroups)
	}

	// Remove ng-1.
	out, err = cli.RemoveNodeGroup(ctx, "c-1", "ng-1", gpupaas.ActionOptions{})
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if len(out.Spec.WorkerNodeGroups) != 1 || out.Spec.WorkerNodeGroups[0].ID != "ng-2" ||
		out.Status.Action != "remove_node_group" {
		t.Fatalf("remove not applied: %+v", out.Spec.WorkerNodeGroups)
	}

	// Delete is idempotent with IgnoreNotFound.
	if err := cli.Delete(ctx, "c-1", gpupaas.DeleteOptions{}); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := cli.Delete(ctx, "c-1", gpupaas.DeleteOptions{IgnoreNotFound: true}); err != nil {
		t.Fatalf("idempotent delete: %v", err)
	}
}

func TestMKSInt32Helpers(t *testing.T) {
	t.Parallel()
	if int32FromTF(types.Int64Null()) != 0 || int32FromTF(types.Int64Unknown()) != 0 {
		t.Fatal("null/unknown int64 must produce 0")
	}
	if int32FromTF(types.Int64Value(7)) != 7 {
		t.Fatal("value int64 must produce matching int32")
	}
	if int32PtrFromTF(types.Int64Null()) != nil || int32PtrFromTF(types.Int64Unknown()) != nil {
		t.Fatal("null/unknown int64 must produce nil pointer")
	}
	if p := int32PtrFromTF(types.Int64Value(0)); p == nil || *p != 0 {
		t.Fatal("explicit zero must produce non-nil pointer to 0")
	}
}
