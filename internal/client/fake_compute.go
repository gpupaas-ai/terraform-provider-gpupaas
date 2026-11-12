package client

import (
	"context"
	"fmt"
	"strings"

	gpupaas "github.com/gpupaas-ai/gpupaas-go"
	apiv1 "github.com/gpupaas-ai/gpupaas-go/apis/v1alpha1"
	"github.com/gpupaas-ai/gpupaas-go/clientset"
	typed "github.com/gpupaas-ai/gpupaas-go/clientset/typed/v1alpha1"
)

// SeedComputeInstance registers a compute instance in a fake clientset (compute instances are
// read-only through the API: the platform creates them behind first-class objects, so tests have to
// seed them). The instance is stored in project, and in workspace when it is not empty.
func SeedComputeInstance(cs clientset.Interface, project, workspace string, inst *apiv1.ComputeInstance) {
	f := cs.(*fakeClientset).v1
	cp := inst.DeepCopyObject().(*apiv1.ComputeInstance)
	cp.TypeMeta = apiv1.TypeMeta{APIVersion: apiv1.APIVersion, Kind: apiv1.KindComputeInstance}
	cp.Metadata.Project = project
	cp.Metadata.Workspace = workspace
	f.computeInstances[scopeKey(project, workspace, cp.Metadata.Name)] = cp
}

// notFoundGateway mirrors the controller's answer for a missing compute instance:
// HTTP 502 with the reason in the "internal" field.
func notFoundGateway(what string) error {
	body := fmt.Sprintf(`{"internal":"ent: %s not found","code":1,"external":"Upstream misbehaving"}`, what)
	return &gpupaas.APIError{StatusCode: 502, Message: "ent: " + what + " not found", Body: body}
}

type fakeComputeInstances struct {
	f         *fakeV1alpha1
	project   string
	workspace string
}

func (c *fakeComputeInstances) key(name string) string { return scopeKey(c.project, c.workspace, name) }

func (c *fakeComputeInstances) Get(_ context.Context, name string, _ gpupaas.GetOptions) (*apiv1.ComputeInstance, error) {
	cur, ok := c.f.computeInstances[c.key(name)]
	if !ok {
		return nil, notFoundGateway("workspace_compute_instance")
	}
	return cur.DeepCopyObject().(*apiv1.ComputeInstance), nil
}

func (c *fakeComputeInstances) List(_ context.Context, _ gpupaas.ListOptions) (*apiv1.ComputeInstanceList, error) {
	out := &apiv1.ComputeInstanceList{}
	prefix := c.project + "/"
	for k, v := range c.f.computeInstances {
		if strings.HasPrefix(k, prefix) && (c.workspace == "" || v.Metadata.Workspace == c.workspace) {
			out.Items = append(out.Items, *v.DeepCopyObject().(*apiv1.ComputeInstance))
		}
	}
	return out, nil
}

func (c *fakeComputeInstances) FailureLog(_ context.Context, name string, _ int) (*apiv1.ComputeFailureLog, error) {
	if _, ok := c.f.computeInstances[c.key(name)]; !ok {
		return nil, notFoundGateway("workspace_compute_instance")
	}
	return &apiv1.ComputeFailureLog{}, nil
}

var _ typed.ComputeInstanceInterface = (*fakeComputeInstances)(nil)

// SeedMKSProfile adds a cluster profile (Kubernetes SKU) to a fake clientset, for tests.
func SeedMKSProfile(cs clientset.Interface, project string, p apiv1.MKSClusterProfile) {
	f := cs.(*fakeClientset).v1
	cp := p.DeepCopyObject().(*apiv1.MKSClusterProfile)
	cp.Metadata.Project = project
	f.mksProfiles[project+"/"+cp.Metadata.Name] = cp
}

// SeedBmProfile adds a bare-metal SKU to a fake clientset, for tests.
func SeedBmProfile(cs clientset.Interface, project string, p apiv1.BmProfile) {
	f := cs.(*fakeClientset).v1
	cp := p.DeepCopyObject().(*apiv1.BmProfile)
	cp.Metadata.Project = project
	f.bmProfiles[project+"/"+cp.Metadata.Name] = cp
}

// SeedVMProfile adds a VM SKU to a fake clientset, for tests.
func SeedVMProfile(cs clientset.Interface, project string, p apiv1.VMProfile) {
	f := cs.(*fakeClientset).v1
	cp := p.DeepCopyObject().(*apiv1.VMProfile)
	cp.Metadata.Project = project
	f.vmProfiles[project+"/"+cp.Metadata.Name] = cp
}
