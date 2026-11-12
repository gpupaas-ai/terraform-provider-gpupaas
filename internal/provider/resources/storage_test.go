package resources

import (
	"context"
	"errors"
	"testing"
	"time"

	gpupaas "github.com/gpupaas-ai/gpupaas-go"
	apiv1 "github.com/gpupaas-ai/gpupaas-go/apis/v1alpha1"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/gpupaas-ai/terraform-provider-gpupaas/internal/client"
)

func TestWireBoolsRoundTripAndUnsetStaysUnset(t *testing.T) {
	for in, want := range map[types.Bool]string{types.BoolValue(true): "true", types.BoolValue(false): "false", types.BoolNull(): "", types.BoolUnknown(): ""} {
		if got := wireBool(in); got != want {
			t.Errorf("wireBool(%v) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]types.Bool{"true": types.BoolValue(true), "FALSE": types.BoolValue(false), " true ": types.BoolValue(true), "": types.BoolNull(), "maybe": types.BoolNull()} {
		if got := boolFromWire(in); !got.Equal(want) {
			t.Errorf("boolFromWire(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestAFieldThePlatformDoesNotEchoKeepsTheConfiguredValue(t *testing.T) {
	base := storageResourceModel{
		Metadata: scopedMetadataModel{Name: types.StringValue("data"), Project: types.StringValue("p")},
		Spec: storageSpecModel{
			Type: types.StringValue("block-ddn"), Size: types.StringValue("50"), Datacenter: types.StringValue("dc"),
			EnableEncryptionAtRest: types.BoolValue(true),
		},
	}
	// a profile with a fixed volume leaves size, datacenter and the flag out of what it returns
	srv := &apiv1.Storage{Metadata: apiv1.ObjectMeta{Name: "data"}, Spec: apiv1.StorageSpec{Type: "block-ddn"}, Status: apiv1.StorageStatus{Status: "success"}}
	var diags diag.Diagnostics
	got := storageModelFromServer(context.Background(), base, srv, &diags)
	if diags.HasError() {
		t.Fatal(diags)
	}
	if got.Spec.Size.ValueString() != "50" || got.Spec.Datacenter.ValueString() != "dc" || !got.Spec.EnableEncryptionAtRest.ValueBool() {
		t.Fatalf("configured values must survive a platform that does not echo them: %+v", got.Spec)
	}
	// an unset field the platform fills in is taken from the platform
	base.Spec.Size = types.StringNull()
	srv.Spec.Size = "100"
	if got = storageModelFromServer(context.Background(), base, srv, &diags); got.Spec.Size.ValueString() != "100" {
		t.Fatalf("size %q", got.Spec.Size.ValueString())
	}
	// and a value that changed on the platform shows as drift
	base.Spec.Size = types.StringValue("50")
	if got = storageModelFromServer(context.Background(), base, srv, &diags); got.Spec.Size.ValueString() != "100" {
		t.Fatalf("drift must show the platform's size, got %q", got.Spec.Size.ValueString())
	}
}

func TestStateHasASensitiveOutputOnlyWhenThereIsOne(t *testing.T) {
	base := storageResourceModel{
		Metadata: scopedMetadataModel{Name: types.StringValue("data"), Project: types.StringValue("p"), Workspace: types.StringValue("w")},
		Spec:     storageSpecModel{Type: types.StringValue("t")},
	}
	var diags diag.Diagnostics
	inProgress := &apiv1.Storage{Metadata: apiv1.ObjectMeta{Name: "data"}, Status: apiv1.StorageStatus{Status: "in_progress"}}
	got := storageModelFromServer(context.Background(), base, inProgress, &diags)
	if !got.Status.Output.IsNull() || got.ID.ValueString() != "p/w/data" {
		t.Errorf("no output yet: %+v", got.Status)
	}
	got = storageModelFromServer(context.Background(), base, &apiv1.Storage{Metadata: apiv1.ObjectMeta{Name: "data"}, Status: apiv1.StorageStatus{Status: "success", Output: map[string]string{"k": "v"}}}, &diags)
	if got.Status.Output.IsNull() || len(got.Status.Output.Elements()) != 1 {
		t.Errorf("output: %v", got.Status.Output)
	}
	// the timeouts block is stored as the model holds it: nil stays nil, and a value is kept
	base.Timeouts = &storageTimeoutsModel{Create: types.StringValue("5m")}
	if got = storageModelFromServer(context.Background(), base, inProgress, &diags); got.Timeouts == nil || got.Timeouts.Create.ValueString() != "5m" {
		t.Error("a timeouts block with a value must be kept")
	}
	base.Timeouts = nil
	if got = storageModelFromServer(context.Background(), base, inProgress, &diags); got.Timeouts != nil {
		t.Error("an absent timeouts block must stay null")
	}
}

func TestOnlyTheInPlaceFieldsAreCheckedAfterAnUpdate(t *testing.T) {
	have := &apiv1.Storage{Spec: apiv1.StorageSpec{Type: "t", Size: "50", AccessPolicy: "rw", Sharing: &apiv1.DevSharingSpec{ShareMode: "None"}}}
	same := &apiv1.Storage{Spec: apiv1.StorageSpec{Type: "t", Size: "50", AccessPolicy: "rw"}}
	if storageNeedsApply(same, have) || len(storageNotApplied(same, have)) != 0 {
		t.Fatal("an identical request needs nothing")
	}
	unset := &apiv1.Storage{Spec: apiv1.StorageSpec{Type: "t"}}
	if storageNeedsApply(unset, have) {
		t.Fatal("an unset size and access policy leave the field to the platform")
	}
	resize := &apiv1.Storage{Spec: apiv1.StorageSpec{Type: "t", Size: "80", AccessPolicy: "ro"}}
	got := storageNotApplied(resize, have)
	if len(got) != 2 || got[0] != `size is "50", not the requested "80"` || got[1] != `access_policy is "rw", not the requested "ro"` {
		t.Fatalf("not applied: %q", got)
	}
	share := &apiv1.Storage{Spec: apiv1.StorageSpec{Sharing: &apiv1.DevSharingSpec{ShareMode: "SPECIFIC_WORKSPACES", Workspaces: []string{"a"}}}}
	if got := storageNotApplied(share, have); len(got) != 1 {
		t.Fatalf("sharing change: %q", got)
	}
	// "None" and no sharing are the same thing
	none := &apiv1.Storage{Spec: apiv1.StorageSpec{Sharing: &apiv1.DevSharingSpec{ShareMode: "None"}}}
	if storageNeedsApply(none, &apiv1.Storage{}) {
		t.Fatal("sharing None against no sharing is no change")
	}
}

func TestDeleteWaitToleratesAStaleFailedRecordButNotAFailedTeardown(t *testing.T) {
	failedDeploy := &apiv1.Storage{Status: apiv1.StorageStatus{Status: "failed", Action: "deploy"}}
	pred := storageGone(2)
	for i := 0; i < 2; i++ {
		if done, err := pred(failedDeploy); done || err != nil {
			t.Fatalf("poll %d: a stale failed record is tolerated, got %v %v", i+1, done, err)
		}
	}
	if _, err := pred(failedDeploy); err == nil {
		t.Fatal("a failed record that never starts its teardown must end the wait")
	}
	for _, st := range []apiv1.StorageStatus{
		{Status: "destroyfailed", Action: "destroy"},
		{Status: "delete_pending", Action: "destroy"},
		{Status: "failed", Action: "destroy"},
	} {
		if _, err := storageGone(8)(&apiv1.Storage{Status: st}); err == nil {
			t.Errorf("%+v: a failed teardown must end the wait at once", st)
		}
	}
	if done, err := storageGone(8)(&apiv1.Storage{Status: apiv1.StorageStatus{Status: "deleting", Action: "destroy"}}); done || err != nil {
		t.Fatalf("deleting: %v %v", done, err)
	}
}

func TestTimeoutsParseAndFallBackToTheDefault(t *testing.T) {
	var diags diag.Diagnostics
	var none *storageTimeoutsModel
	if got := none.get("create", time.Minute, &diags); got != time.Minute {
		t.Fatalf("nil block: %v", got)
	}
	tm := &storageTimeoutsModel{Create: types.StringValue("5m"), Update: types.StringNull(), Delete: types.StringValue("")}
	if tm.get("create", time.Minute, &diags) != 5*time.Minute || tm.get("update", time.Hour, &diags) != time.Hour || tm.get("delete", 2*time.Hour, &diags) != 2*time.Hour || diags.HasError() {
		t.Fatalf("timeouts: %v", diags)
	}
	bad := &storageTimeoutsModel{Create: types.StringValue("soon")}
	if bad.get("create", time.Minute, &diags) != time.Minute || !diags.HasError() {
		t.Fatal("an invalid duration must be reported")
	}
}

// A controller that ignores the serviceType filter returns every volume for both families: the name is then in both
// lists, which must never read as "the other family".
func TestFamilyCheckIsSilentWhenTheControllerIgnoresTheFilter(t *testing.T) {
	cs := client.NewFakeClientset()
	pd := &client.ProviderData{Clientset: cs}
	if _, err := cs.V1alpha1().Workspaces("p").Storages("w").Create(context.Background(), &apiv1.Storage{Metadata: apiv1.ObjectMeta{Name: "data"}, Spec: apiv1.StorageSpec{Type: "t"}}, gpupaas.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	inOwn, inOther := storageFamily(context.Background(), pd, highSpeedStorage, "p", "w", "data")
	if !inOwn || !inOther {
		t.Fatalf("a fake that ignores the filter lists the volume in both: own %v other %v", inOwn, inOther)
	}
	if inOther && !inOwn {
		t.Fatal("must not report a mismatch")
	}
	// a name in no list (a custom profile, or another workspace) is not a mismatch either
	if inOwn, inOther := storageFamily(context.Background(), pd, highSpeedStorage, "p", "other-ws", "data"); inOwn || inOther {
		t.Fatalf("another workspace: own %v other %v", inOwn, inOther)
	}
}

func TestKindsPointAtEachOther(t *testing.T) {
	if highSpeedStorage.other != &objectStorage || objectStorage.other != &highSpeedStorage {
		t.Fatal("the kinds must name each other")
	}
	if highSpeedStorage.serviceType == objectStorage.serviceType {
		t.Fatal("the families must differ")
	}
}

func TestSharingIsComparedAsASetNotByPosition(t *testing.T) {
	a := &apiv1.DevSharingSpec{ShareMode: "SPECIFIC_PROJECTS", Workspaces: []string{"x", "y"}, Projects: []apiv1.DevProjectSharingSpec{
		{Name: "p1", Workspaces: []string{"a", "b"}}, {Name: "p2", Workspaces: []string{"c"}}}}
	reordered := &apiv1.DevSharingSpec{ShareMode: "SPECIFIC_PROJECTS", Workspaces: []string{"y", "x"}, Projects: []apiv1.DevProjectSharingSpec{
		{Name: "p2", Workspaces: []string{"c"}}, {Name: "p1", Workspaces: []string{"b", "a"}}}}
	if !sameSharing(a, reordered) {
		t.Fatal("the same entries in another order are the same sharing")
	}
	other := &apiv1.DevSharingSpec{ShareMode: "SPECIFIC_PROJECTS", Workspaces: []string{"x", "y"}, Projects: []apiv1.DevProjectSharingSpec{
		{Name: "p1", Workspaces: []string{"a"}}, {Name: "p2", Workspaces: []string{"c"}}}}
	if sameSharing(a, other) {
		t.Fatal("a project with fewer workspaces is different sharing")
	}
	if sameSet([]string{"a", "a"}, []string{"a", "b"}) || !sameSet(nil, []string{}) {
		t.Fatal("sameSet")
	}
}

func TestUpdateWaitIgnoresTheEarlierRunUntilItSeesTheNewOne(t *testing.T) {
	settled := &apiv1.Storage{Status: apiv1.StorageStatus{Status: "success", Action: "deploy"}}
	active := &apiv1.Storage{Status: apiv1.StorageStatus{Status: "in_progress", Action: "update"}}

	// a settled record that shows before the new run starts is the previous run's
	pred := storageUpdateDone(apiv1.StorageStatus{Status: "success"})
	for i := 0; i < storageUnseenPolls-1; i++ {
		if done, err := pred(settled); done || err != nil {
			t.Fatalf("poll %d: a stale success must not end the update: %v %v", i+1, done, err)
		}
	}
	if done, err := pred(active); done || err != nil {
		t.Fatalf("in progress: %v %v", done, err)
	}
	if done, err := pred(settled); !done || err != nil {
		t.Fatalf("after the run was seen, a settled record ends the update: %v %v", done, err)
	}

	// a run that finished between two polls is accepted after enough settled polls
	pred = storageUpdateDone(apiv1.StorageStatus{Status: "success"})
	var done bool
	for i := 0; i < storageUnseenPolls; i++ {
		done, _ = pred(settled)
	}
	if !done {
		t.Fatal("a run that was never seen in progress must be accepted after storageUnseenPolls settled polls")
	}

	// the previous run's failure is not this update's, until it persists
	prevFailed := apiv1.StorageStatus{Status: "failed", Action: "deploy", Reason: "boom"}
	failed := &apiv1.Storage{Status: prevFailed}
	pred = storageUpdateDone(prevFailed)
	for i := 0; i < storageUnseenPolls-1; i++ {
		if done, err := pred(failed); done || err != nil {
			t.Fatalf("poll %d: the earlier failure is tolerated: %v %v", i+1, done, err)
		}
	}
	if _, err := pred(failed); err == nil {
		t.Fatal("a failure that persists is the update's failure")
	}
	// a new failure (different reason) fails at once
	if _, err := storageUpdateDone(prevFailed)(&apiv1.Storage{Status: apiv1.StorageStatus{Status: "failed", Action: "update", Reason: "new"}}); err == nil {
		t.Fatal("a different failure is this update's")
	}
	// and a failure after the run was seen fails at once
	pred = storageUpdateDone(prevFailed)
	pred(active)
	if _, err := pred(failed); err == nil {
		t.Fatal("a failure after the run started is the update's")
	}
}

func TestOnlyAServerSideRefusalIsAClientError(t *testing.T) {
	for code, want := range map[int]bool{400: true, 404: true, 422: true, 500: false, 502: false} {
		if got := isClientError(&gpupaas.APIError{StatusCode: code}); got != want {
			t.Errorf("%d: %v", code, got)
		}
	}
	if isClientError(errors.New("connection reset")) {
		t.Error("a network error says nothing about what the platform stored")
	}
}
