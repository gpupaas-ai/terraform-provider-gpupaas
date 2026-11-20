package client

import (
	"context"
	"testing"

	gpupaas "github.com/gpupaas-ai/gpupaas-go"
	apiv1 "github.com/gpupaas-ai/gpupaas-go/apis/v1alpha1"
)

func TestFakeVMInstanceScopes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cs := NewFakeClientset()

	vmInProj := &apiv1.VMInstance{
		Metadata: apiv1.ObjectMeta{Name: "vm-project"},
		Spec:     apiv1.VMInstanceSpec{Type: "S"},
	}
	if _, err := cs.V1alpha1().VMInstances("p").Create(ctx, vmInProj, gpupaas.CreateOptions{}); err != nil {
		t.Fatalf("create project-scoped vm: %v", err)
	}

	vmInWs := &apiv1.VMInstance{
		Metadata: apiv1.ObjectMeta{Name: "vm-ws"},
		Spec:     apiv1.VMInstanceSpec{Type: "S"},
	}
	if _, err := cs.V1alpha1().Workspaces("p").VMInstances("w").Create(ctx, vmInWs, gpupaas.CreateOptions{}); err != nil {
		t.Fatalf("create workspace-scoped vm: %v", err)
	}

	// VM in project scope must not appear in workspace scope and vice versa.
	if _, err := cs.V1alpha1().VMInstances("p").Get(ctx, "vm-ws", gpupaas.GetOptions{}); !gpupaas.IsNotFound(err) {
		t.Fatalf("expected NotFound for workspace VM in project scope, got %v", err)
	}
	if _, err := cs.V1alpha1().Workspaces("p").VMInstances("w").Get(ctx, "vm-project", gpupaas.GetOptions{}); !gpupaas.IsNotFound(err) {
		t.Fatalf("expected NotFound for project VM in workspace scope, got %v", err)
	}
}

func TestFakeDeleteIgnoreNotFound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cs := NewFakeClientset()
	if err := cs.V1alpha1().MKSClusters("p").Delete(ctx, "missing", gpupaas.DeleteOptions{IgnoreNotFound: true}); err != nil {
		t.Fatalf("expected no error with IgnoreNotFound, got %v", err)
	}
	if err := cs.V1alpha1().MKSClusters("p").Delete(ctx, "missing", gpupaas.DeleteOptions{}); !gpupaas.IsNotFound(err) {
		t.Fatalf("expected NotFound, got %v", err)
	}
}

func TestFakeSshKeyIsReadOnlyAndSeeded(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cs := NewFakeClientset()
	if _, err := cs.V1alpha1().SshKeys("p").Get(ctx, "k1", gpupaas.GetOptions{}); !gpupaas.IsNotFound(err) {
		t.Fatalf("expected NotFound before seeding, got %v", err)
	}
	SeedSshKey(cs, "p", "", "k1")
	SeedSshKey(cs, "p", "w", "k2")
	if got, err := cs.V1alpha1().SshKeys("p").Get(ctx, "k1", gpupaas.GetOptions{}); err != nil || got.Metadata.Name != "k1" {
		t.Fatalf("project key: %+v, %v", got, err)
	}
	if got, err := cs.V1alpha1().Workspaces("p").SshKeys("w").Get(ctx, "k2", gpupaas.GetOptions{}); err != nil || got.Metadata.Workspace != "w" {
		t.Fatalf("workspace key: %+v, %v", got, err)
	}
	if _, err := cs.V1alpha1().SshKeys("p").Get(ctx, "k2", gpupaas.GetOptions{}); !gpupaas.IsNotFound(err) {
		t.Fatalf("a workspace key is not in project scope, got %v", err)
	}
}

func TestFakeComputeInstanceIsReadOnlyAndSeeded(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cs := NewFakeClientset()
	if _, err := cs.V1alpha1().ComputeInstances("p").Get(ctx, "vm1", gpupaas.GetOptions{}); !gpupaas.IsNotFound(err) {
		t.Fatalf("expected NotFound before seeding, got %v", err)
	}
	SeedComputeInstance(cs, "p", "w", &apiv1.ComputeInstance{
		Metadata: apiv1.ObjectMeta{Name: "vm1"},
		Status:   apiv1.ComputeInstanceStatus{Status: "failed", Reason: "Failed to deploy"},
	})
	ci := cs.V1alpha1().Workspaces("p").ComputeInstances("w")
	got, err := ci.Get(ctx, "vm1", gpupaas.GetOptions{})
	if err != nil || !got.Status.Failed() || got.Metadata.Workspace != "w" {
		t.Fatalf("seeded instance: %+v, %v", got, err)
	}
	list, err := ci.List(ctx, gpupaas.ListOptions{})
	if err != nil || len(list.Items) != 1 {
		t.Fatalf("list: %+v, %v", list, err)
	}
	if lg, err := ci.FailureLog(ctx, "vm1", 5); err != nil || len(lg.Lines) != 0 {
		t.Fatalf("failure log: %+v, %v", lg, err)
	}
	if _, err := ci.FailureLog(ctx, "missing", 5); err == nil {
		t.Fatal("a missing instance has no failure log")
	}
}
