package client

import (
	"context"
	"testing"

	gpupaas "github.com/gpupaas-ai/gpupaas-go"
	apiv1 "github.com/gpupaas-ai/gpupaas-go/apis/v1alpha1"
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
