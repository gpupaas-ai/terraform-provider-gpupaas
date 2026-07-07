package client

import (
	"context"
	"testing"
	"time"

	gpupaas "github.com/gpupaas-ai/gpupaas-go"
	apiv1 "github.com/gpupaas-ai/gpupaas-go/apis/v1alpha1"

	"github.com/gpupaas-ai/terraform-provider-gpupaas/internal/wait"
)

func TestFakeProjectCRUD(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cs := NewFakeClientset()
	projects := cs.V1alpha1().Projects()

	in := &apiv1.Project{
		TypeMeta: apiv1.TypeMeta{APIVersion: apiv1.APIVersion, Kind: apiv1.KindProject},
		Metadata: apiv1.ObjectMeta{Name: "p1"},
		Spec:     apiv1.ProjectSpec{DisplayName: "P1"},
	}
	if _, err := projects.Create(ctx, in, gpupaas.CreateOptions{}); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := projects.Get(ctx, "p1", gpupaas.GetOptions{})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Spec.DisplayName != "P1" {
		t.Fatalf("get returned wrong project: %+v", got)
	}
	if err := projects.Delete(ctx, "p1", gpupaas.DeleteOptions{}); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := projects.Get(ctx, "p1", gpupaas.GetOptions{}); !gpupaas.IsNotFound(err) {
		t.Fatalf("expected NotFound, got %v", err)
	}
}

func TestFakeVMScopes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cs := NewFakeClientset()

	vmInProj := &apiv1.VirtualMachine{
		Metadata: apiv1.ObjectMeta{Name: "vm-project"},
		Spec:     apiv1.VirtualMachineSpec{VirtualMachine: apiv1.ResourceRef{Name: "S"}},
	}
	if _, err := cs.V1alpha1().VirtualMachines("p").Create(ctx, vmInProj, gpupaas.CreateOptions{}); err != nil {
		t.Fatalf("create project-scoped vm: %v", err)
	}

	vmInWs := &apiv1.VirtualMachine{
		Metadata: apiv1.ObjectMeta{Name: "vm-ws"},
		Spec:     apiv1.VirtualMachineSpec{VirtualMachine: apiv1.ResourceRef{Name: "S"}},
	}
	if _, err := cs.V1alpha1().Workspaces("p").VirtualMachines("w").Create(ctx, vmInWs, gpupaas.CreateOptions{}); err != nil {
		t.Fatalf("create workspace-scoped vm: %v", err)
	}

	// VM in project scope must not appear in workspace scope and vice versa.
	if _, err := cs.V1alpha1().VirtualMachines("p").Get(ctx, "vm-ws", gpupaas.GetOptions{}); !gpupaas.IsNotFound(err) {
		t.Fatalf("expected NotFound for workspace VM in project scope, got %v", err)
	}
	if _, err := cs.V1alpha1().Workspaces("p").VirtualMachines("w").Get(ctx, "vm-project", gpupaas.GetOptions{}); !gpupaas.IsNotFound(err) {
		t.Fatalf("expected NotFound for project VM in workspace scope, got %v", err)
	}
}

func TestFakeDeleteIgnoreNotFound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cs := NewFakeClientset()
	if err := cs.V1alpha1().Storages("p").Delete(ctx, "missing", gpupaas.DeleteOptions{IgnoreNotFound: true}); err != nil {
		t.Fatalf("expected no error with IgnoreNotFound, got %v", err)
	}
	if err := cs.V1alpha1().Storages("p").Delete(ctx, "missing", gpupaas.DeleteOptions{}); !gpupaas.IsNotFound(err) {
		t.Fatalf("expected NotFound, got %v", err)
	}
}

func TestSetVMStatusSequenceAdvancesAndHolds(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cs := NewFakeClientset()
	vms := cs.V1alpha1().VirtualMachines("p")

	if _, err := vms.Create(ctx, &apiv1.VirtualMachine{
		Metadata: apiv1.ObjectMeta{Name: "vm-1"},
		Spec:     apiv1.VirtualMachineSpec{VirtualMachine: apiv1.ResourceRef{Name: "S"}},
	}, gpupaas.CreateOptions{}); err != nil {
		t.Fatalf("create: %v", err)
	}

	SetVMStatusSequence(cs, "p", "", "vm-1", []apiv1.VirtualMachineStatus{
		{Status: "Status_SUBMITTED"},
		{Status: "Status_PENDING"},
		{Status: "Status_SUCCESS"},
	})

	wantSeq := []string{"Status_SUBMITTED", "Status_PENDING", "Status_SUCCESS", "Status_SUCCESS", "Status_SUCCESS"}
	for i, want := range wantSeq {
		got, err := vms.GetStatus(ctx, "vm-1", gpupaas.GetOptions{})
		if err != nil {
			t.Fatalf("GetStatus call %d: %v", i, err)
		}
		if got.Status.Status != want {
			t.Fatalf("GetStatus call %d = %q want %q", i, got.Status.Status, want)
		}
	}
}

func TestSetVMDeleteLatencyThenGone(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cs := NewFakeClientset()
	vms := cs.V1alpha1().VirtualMachines("p")

	if _, err := vms.Create(ctx, &apiv1.VirtualMachine{
		Metadata: apiv1.ObjectMeta{Name: "vm-1"},
		Spec:     apiv1.VirtualMachineSpec{VirtualMachine: apiv1.ResourceRef{Name: "S"}},
	}, gpupaas.CreateOptions{}); err != nil {
		t.Fatalf("create: %v", err)
	}
	SetVMDeleteLatency(cs, "p", "", "vm-1", 2)

	if err := vms.Delete(ctx, "vm-1", gpupaas.DeleteOptions{}); err != nil {
		t.Fatalf("delete: %v", err)
	}
	// Still "present" for the configured number of polls...
	for i := 0; i < 2; i++ {
		if _, err := vms.GetStatus(ctx, "vm-1", gpupaas.GetOptions{}); err != nil {
			t.Fatalf("poll %d: expected VM still visible during delete latency, got %v", i, err)
		}
	}
	// ...then gone.
	if _, err := vms.GetStatus(ctx, "vm-1", gpupaas.GetOptions{}); !gpupaas.IsNotFound(err) {
		t.Fatalf("expected NotFound after delete latency elapsed, got %v", err)
	}
}

func TestSetVMStatusSequenceThenRecreateCancelsLeftoverState(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cs := NewFakeClientset()
	vms := cs.V1alpha1().VirtualMachines("p")

	create := func() {
		if _, err := vms.Create(ctx, &apiv1.VirtualMachine{
			Metadata: apiv1.ObjectMeta{Name: "vm-1"},
			Spec:     apiv1.VirtualMachineSpec{VirtualMachine: apiv1.ResourceRef{Name: "S"}},
		}, gpupaas.CreateOptions{}); err != nil {
			t.Fatalf("create: %v", err)
		}
	}
	create()
	SetVMDeleteLatency(cs, "p", "", "vm-1", 5)
	if err := vms.Delete(ctx, "vm-1", gpupaas.DeleteOptions{}); err != nil {
		t.Fatalf("delete: %v", err)
	}
	// Re-create (upsert) supersedes the pending destroy: the VM must be
	// immediately visible again with no leftover countdown.
	create()
	if _, err := vms.GetStatus(ctx, "vm-1", gpupaas.GetOptions{}); err != nil {
		t.Fatalf("expected VM visible after re-create, got %v", err)
	}
}

// TestWaitAgainstFakeClientsetVMReady proves the internal/wait pollers work
// end-to-end against the real clientset.Interface surface (not just a
// synthetic wait.Getter), using SetVMStatusSequence to script a realistic
// SUBMITTED -> PENDING -> SUCCESS transition.
func TestWaitAgainstFakeClientsetVMReady(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cs := NewFakeClientset()
	vms := cs.V1alpha1().Workspaces("p").VirtualMachines("ws")

	if _, err := vms.Create(ctx, &apiv1.VirtualMachine{
		Metadata: apiv1.ObjectMeta{Name: "vm-1"},
		Spec:     apiv1.VirtualMachineSpec{VirtualMachine: apiv1.ResourceRef{Name: "S"}},
	}, gpupaas.CreateOptions{}); err != nil {
		t.Fatalf("create: %v", err)
	}
	SetVMStatusSequence(cs, "p", "ws", "vm-1", []apiv1.VirtualMachineStatus{
		{Status: "Status_SUBMITTED"},
		{Status: "Status_PENDING"},
		{Status: "Status_SUCCESS"},
	})

	getter := func(ctx context.Context) (*apiv1.VirtualMachine, error) {
		return vms.GetStatus(ctx, "vm-1", gpupaas.GetOptions{})
	}
	out, err := wait.WaitForVMReady(ctx, getter, wait.Options{Interval: 5 * time.Millisecond, Timeout: 2 * time.Second})
	if err != nil {
		t.Fatalf("WaitForVMReady: %v", err)
	}
	if wait.NormalizeStatus(out.Status.Status) != "success" {
		t.Fatalf("final status = %q", out.Status.Status)
	}
}

// TestWaitAgainstFakeClientsetDeleteLatency proves WaitForGone works against
// SetVMDeleteLatency-simulated async deletion through the real
// clientset.Interface.
func TestWaitAgainstFakeClientsetDeleteLatency(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cs := NewFakeClientset()
	vms := cs.V1alpha1().VirtualMachines("p")

	if _, err := vms.Create(ctx, &apiv1.VirtualMachine{
		Metadata: apiv1.ObjectMeta{Name: "vm-1"},
		Spec:     apiv1.VirtualMachineSpec{VirtualMachine: apiv1.ResourceRef{Name: "S"}},
	}, gpupaas.CreateOptions{}); err != nil {
		t.Fatalf("create: %v", err)
	}
	SetVMDeleteLatency(cs, "p", "", "vm-1", 3)
	if err := vms.Delete(ctx, "vm-1", gpupaas.DeleteOptions{IgnoreNotFound: true}); err != nil {
		t.Fatalf("delete: %v", err)
	}

	getter := func(ctx context.Context) (*apiv1.VirtualMachine, error) {
		return vms.GetStatus(ctx, "vm-1", gpupaas.GetOptions{})
	}
	if err := wait.WaitForGone(ctx, getter, wait.Options{Interval: 5 * time.Millisecond, Timeout: 2 * time.Second}); err != nil {
		t.Fatalf("WaitForGone: %v", err)
	}
}
