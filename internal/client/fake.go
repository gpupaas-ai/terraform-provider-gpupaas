package client

import (
	"context"
	"fmt"

	gpupaas "github.com/gpupaas-ai/gpupaas-go"
	apiv1 "github.com/gpupaas-ai/gpupaas-go/apis/v1alpha1"
	"github.com/gpupaas-ai/gpupaas-go/clientset"
	typed "github.com/gpupaas-ai/gpupaas-go/clientset/typed/v1alpha1"
)

// NewFakeClientset returns an in-memory clientset suitable for unit tests.
// All resources are stored keyed by (project,workspace,name). Resources the API only reads (profiles,
// SSH keys, compute instances) are added with the Seed* helpers.
func NewFakeClientset() clientset.Interface {
	return &fakeClientset{v1: newFakeV1alpha1()}
}

type fakeClientset struct{ v1 *fakeV1alpha1 }

func (f *fakeClientset) V1alpha1() typed.Interface { return f.v1 }

func newFakeV1alpha1() *fakeV1alpha1 {
	return &fakeV1alpha1{
		workspaces:       map[string]*apiv1.Workspace{},
		sshKeys:          map[string]*apiv1.SshKey{},
		securityGroups:   map[string]*apiv1.SecurityGroup{},
		storages:         map[string]*apiv1.Storage{},
		mksClusters:      map[string]*apiv1.MKSCluster{},
		mksNodes:         map[string]*apiv1.MKSNode{},
		mksProfiles:      map[string]*apiv1.MKSClusterProfile{},
		bareMetals:       map[string]*apiv1.BareMetal{},
		bmProfiles:       map[string]*apiv1.BmProfile{},
		vmInstances:      map[string]*apiv1.VMInstance{},
		vmProfiles:       map[string]*apiv1.VMProfile{},
		computeInstances: map[string]*apiv1.ComputeInstance{},
	}
}

type fakeV1alpha1 struct {
	workspaces       map[string]*apiv1.Workspace
	sshKeys          map[string]*apiv1.SshKey
	securityGroups   map[string]*apiv1.SecurityGroup
	storages         map[string]*apiv1.Storage
	mksClusters      map[string]*apiv1.MKSCluster
	mksNodes         map[string]*apiv1.MKSNode
	poolOps          []string
	mksProfiles      map[string]*apiv1.MKSClusterProfile
	bareMetals       map[string]*apiv1.BareMetal
	bmProfiles       map[string]*apiv1.BmProfile
	vmInstances      map[string]*apiv1.VMInstance
	vmProfiles       map[string]*apiv1.VMProfile
	computeInstances map[string]*apiv1.ComputeInstance
}

func (f *fakeV1alpha1) Workspaces(project string) typed.WorkspaceInterface {
	return &fakeWorkspaces{f: f, project: project}
}
func (f *fakeV1alpha1) SshKeys(project string) typed.SshKeyInterface {
	return &fakeSshKeys{f: f, project: project}
}
func (f *fakeV1alpha1) SecurityGroups(project string) typed.SecurityGroupInterface {
	return &fakeSecurityGroups{f: f, project: project}
}
func (f *fakeV1alpha1) Storages(project string) typed.StorageInterface {
	return &fakeStorages{f: f, project: project}
}
func (f *fakeV1alpha1) MKSClusters(project string) typed.MKSClusterInterface {
	return &fakeMKSClusters{f: f, project: project}
}
func (f *fakeV1alpha1) MKSClusterProfiles(project string) typed.MKSClusterProfileInterface {
	return &fakeMKSProfiles{f: f, project: project}
}
func (f *fakeV1alpha1) BareMetals(project string) typed.BareMetalInterface {
	return &fakeBareMetals{f: f, project: project}
}
func (f *fakeV1alpha1) BmProfiles(project string) typed.BmProfileInterface {
	return &fakeBmProfiles{f: f, project: project}
}
func (f *fakeV1alpha1) VMInstances(project string) typed.VMInstanceInterface {
	return &fakeVMInstances{f: f, project: project}
}
func (f *fakeV1alpha1) VMProfiles(project string) typed.VMProfileInterface {
	return &fakeVMProfiles{f: f, project: project}
}
func (f *fakeV1alpha1) MKSNodes(project string) typed.MKSNodeInterface {
	return &fakeMKSNodes{f: f, project: project}
}
func (f *fakeV1alpha1) MKSMatchmaker(project string) typed.MKSMatchmakerInterface {
	return fakeMKSMatchmaker{}
}

func (f *fakeV1alpha1) ComputeInstances(project string) typed.ComputeInstanceInterface {
	return &fakeComputeInstances{f: f, project: project}
}

func notFound(kind, name string) error {
	return &gpupaas.APIError{StatusCode: 404, Message: fmt.Sprintf("%s %q not found", kind, name)}
}

// ---- Workspaces -----------------------------------------------------------

type fakeWorkspaces struct {
	f       *fakeV1alpha1
	project string
}

func (w *fakeWorkspaces) ComputeInstances(workspace string) typed.ComputeInstanceInterface {
	return &fakeComputeInstances{f: w.f, project: w.project, workspace: workspace}
}
func (w *fakeWorkspaces) BareMetals(workspace string) typed.BareMetalInterface {
	return &fakeBareMetals{f: w.f, project: w.project, workspace: workspace}
}
func (w *fakeWorkspaces) VMInstances(workspace string) typed.VMInstanceInterface {
	return &fakeVMInstances{f: w.f, project: w.project, workspace: workspace}
}
func (w *fakeWorkspaces) MKSClusters(workspace string) typed.MKSClusterInterface {
	return &fakeMKSClusters{f: w.f, project: w.project, workspace: workspace}
}

func (w *fakeWorkspaces) SshKeys(workspace string) typed.SshKeyInterface {
	return &fakeSshKeys{f: w.f, project: w.project, workspace: workspace}
}

func (w *fakeWorkspaces) SecurityGroups(workspace string) typed.SecurityGroupInterface {
	return &fakeSecurityGroups{f: w.f, project: w.project, workspace: workspace}
}
func (w *fakeWorkspaces) Storages(workspace string) typed.StorageInterface {
	return &fakeStorages{f: w.f, project: w.project, workspace: workspace}
}

func (w *fakeWorkspaces) key(name string) string { return w.project + "/" + name }

func (w *fakeWorkspaces) store(obj *apiv1.Workspace) *apiv1.Workspace {
	cp := obj.DeepCopyObject().(*apiv1.Workspace)
	cp.TypeMeta = apiv1.TypeMeta{APIVersion: apiv1.APIVersion, Kind: apiv1.KindWorkspace}
	cp.Metadata.Project = w.project
	if cp.Metadata.DisplayName == "" {
		cp.Metadata.DisplayName = cp.Metadata.Name
	}
	w.f.workspaces[w.key(cp.Metadata.Name)] = cp
	return cp.DeepCopyObject().(*apiv1.Workspace)
}

func (w *fakeWorkspaces) Create(_ context.Context, obj *apiv1.Workspace, _ gpupaas.CreateOptions) (*apiv1.Workspace, error) {
	if _, ok := w.f.workspaces[w.key(obj.Metadata.Name)]; ok {
		return nil, &gpupaas.APIError{StatusCode: 409, Message: fmt.Sprintf("workspace %q already exists", obj.Metadata.Name)}
	}
	return w.store(obj), nil
}

func (w *fakeWorkspaces) Update(_ context.Context, obj *apiv1.Workspace, _ gpupaas.UpdateOptions) (*apiv1.Workspace, error) {
	return w.store(obj), nil
}

func (w *fakeWorkspaces) Get(_ context.Context, name string, _ gpupaas.GetOptions) (*apiv1.Workspace, error) {
	if x, ok := w.f.workspaces[w.key(name)]; ok {
		return x.DeepCopyObject().(*apiv1.Workspace), nil
	}
	return nil, notFound("workspace", name)
}

func (w *fakeWorkspaces) List(_ context.Context, _ gpupaas.ListOptions) (*apiv1.WorkspaceList, error) {
	out := &apiv1.WorkspaceList{}
	for _, x := range w.f.workspaces {
		if x.Metadata.Project == w.project {
			out.Items = append(out.Items, *x.DeepCopyObject().(*apiv1.Workspace))
		}
	}
	return out, nil
}

func (w *fakeWorkspaces) Delete(_ context.Context, name string, opts gpupaas.DeleteOptions) error {
	if _, ok := w.f.workspaces[w.key(name)]; !ok {
		if opts.IgnoreNotFound {
			return nil
		}
		return notFound("workspace", name)
	}
	delete(w.f.workspaces, w.key(name))
	return nil
}

// ---- Generic dev-resource helpers -----------------------------------------

func scopeKey(project, workspace, name string) string {
	return project + "/" + workspace + "/" + name
}

// ---- SshKeys --------------------------------------------------------------

type fakeSshKeys struct {
	f         *fakeV1alpha1
	project   string
	workspace string
}

func (s *fakeSshKeys) key(name string) string { return scopeKey(s.project, s.workspace, name) }

// SeedSshKey registers an SSH key in a fake clientset, in project and in workspace when it is not
// empty. Tests that only need a key to exist (an MKS cluster names one) use it.
func SeedSshKey(cs clientset.Interface, project, workspace, name string) {
	f := cs.(*fakeClientset).v1
	f.sshKeys[scopeKey(project, workspace, name)] = &apiv1.SshKey{
		TypeMeta: apiv1.TypeMeta{APIVersion: apiv1.APIVersion, Kind: apiv1.KindSshKey},
		Metadata: apiv1.ObjectMeta{Name: name, Project: project, Workspace: workspace},
	}
}

func (s *fakeSshKeys) Create(_ context.Context, obj *apiv1.SshKey, _ gpupaas.CreateOptions) (*apiv1.SshKey, error) {
	if _, ok := s.f.sshKeys[s.key(obj.Metadata.Name)]; ok {
		return nil, &gpupaas.APIError{StatusCode: 409, Message: fmt.Sprintf("ssh key %q already exists", obj.Metadata.Name)}
	}
	cp := obj.DeepCopyObject().(*apiv1.SshKey)
	cp.TypeMeta = apiv1.TypeMeta{APIVersion: apiv1.APIVersion, Kind: apiv1.KindSshKey}
	cp.Metadata.Project, cp.Metadata.Workspace = s.project, s.workspace
	cp.Status = apiv1.SshKeyStatus{Status: "success"}
	s.f.sshKeys[s.key(cp.Metadata.Name)] = cp
	return cp.DeepCopyObject().(*apiv1.SshKey), nil
}

func (s *fakeSshKeys) Get(_ context.Context, name string, _ gpupaas.GetOptions) (*apiv1.SshKey, error) {
	if x, ok := s.f.sshKeys[s.key(name)]; ok {
		return x.DeepCopyObject().(*apiv1.SshKey), nil
	}
	return nil, notFound("ssh key", name)
}

func (s *fakeSshKeys) List(_ context.Context, _ gpupaas.ListOptions) (*apiv1.SshKeyList, error) {
	out := &apiv1.SshKeyList{}
	for _, x := range s.f.sshKeys {
		if x.Metadata.Project == s.project && x.Metadata.Workspace == s.workspace {
			out.Items = append(out.Items, *x.DeepCopyObject().(*apiv1.SshKey))
		}
	}
	return out, nil
}

func (s *fakeSshKeys) Delete(_ context.Context, name string, opts gpupaas.DeleteOptions) error {
	if _, ok := s.f.sshKeys[s.key(name)]; !ok {
		if opts.IgnoreNotFound {
			return nil
		}
		return notFound("ssh key", name)
	}
	delete(s.f.sshKeys, s.key(name))
	return nil
}

// ---- SecurityGroups -------------------------------------------------------

type fakeSecurityGroups struct {
	f         *fakeV1alpha1
	project   string
	workspace string
}

func (s *fakeSecurityGroups) key(name string) string { return scopeKey(s.project, s.workspace, name) }

func (s *fakeSecurityGroups) Create(_ context.Context, obj *apiv1.SecurityGroup, _ gpupaas.CreateOptions) (*apiv1.SecurityGroup, error) {
	if _, ok := s.f.securityGroups[s.key(obj.Metadata.Name)]; ok {
		return nil, &gpupaas.APIError{StatusCode: 409, Message: fmt.Sprintf("security group %q already exists", obj.Metadata.Name)}
	}
	cp := obj.DeepCopyObject().(*apiv1.SecurityGroup)
	cp.TypeMeta = apiv1.TypeMeta{APIVersion: apiv1.APIVersion, Kind: apiv1.KindSecurityGroup}
	cp.Metadata.Project, cp.Metadata.Workspace = s.project, s.workspace
	cp.Status = apiv1.SecurityGroupStatus{Status: "success"}
	s.f.securityGroups[s.key(cp.Metadata.Name)] = cp
	return cp.DeepCopyObject().(*apiv1.SecurityGroup), nil
}

func (s *fakeSecurityGroups) Get(_ context.Context, name string, _ gpupaas.GetOptions) (*apiv1.SecurityGroup, error) {
	if x, ok := s.f.securityGroups[s.key(name)]; ok {
		return x.DeepCopyObject().(*apiv1.SecurityGroup), nil
	}
	return nil, notFound("security group", name)
}

func (s *fakeSecurityGroups) List(_ context.Context, _ gpupaas.ListOptions) (*apiv1.SecurityGroupList, error) {
	out := &apiv1.SecurityGroupList{}
	for _, x := range s.f.securityGroups {
		if x.Metadata.Project == s.project && x.Metadata.Workspace == s.workspace {
			out.Items = append(out.Items, *x.DeepCopyObject().(*apiv1.SecurityGroup))
		}
	}
	return out, nil
}

func (s *fakeSecurityGroups) Delete(_ context.Context, name string, opts gpupaas.DeleteOptions) error {
	if _, ok := s.f.securityGroups[s.key(name)]; !ok {
		if opts.IgnoreNotFound {
			return nil
		}
		return notFound("security group", name)
	}
	delete(s.f.securityGroups, s.key(name))
	return nil
}

// ---- Storages -------------------------------------------------------------
//
// The fake stores a volume at once with status success and an output. Provisioning over time, failures and
// parked deletes are covered by the provider acceptance tests, which run against the gpupaas-go devapi mock.

type fakeStorages struct {
	f         *fakeV1alpha1
	project   string
	workspace string
}

func (s *fakeStorages) key(name string) string { return scopeKey(s.project, s.workspace, name) }

func (s *fakeStorages) store(obj *apiv1.Storage) *apiv1.Storage {
	cp := obj.DeepCopyObject().(*apiv1.Storage)
	cp.TypeMeta = apiv1.TypeMeta{APIVersion: apiv1.APIVersion, Kind: apiv1.KindStorage}
	cp.Metadata.Project, cp.Metadata.Workspace = s.project, s.workspace
	cp.Status = apiv1.StorageStatus{Status: "success", Action: "deploy", Output: map[string]string{"volume_size": "1000000000"}}
	s.f.storages[s.key(cp.Metadata.Name)] = cp
	return cp.DeepCopyObject().(*apiv1.Storage)
}

func (s *fakeStorages) Create(_ context.Context, obj *apiv1.Storage, _ gpupaas.CreateOptions) (*apiv1.Storage, error) {
	if _, ok := s.f.storages[s.key(obj.Metadata.Name)]; ok {
		return nil, &gpupaas.APIError{StatusCode: 409, Message: fmt.Sprintf("storage %q already exists", obj.Metadata.Name)}
	}
	return s.store(obj), nil
}

func (s *fakeStorages) Update(_ context.Context, obj *apiv1.Storage, _ gpupaas.UpdateOptions) (*apiv1.Storage, error) {
	if _, ok := s.f.storages[s.key(obj.Metadata.Name)]; !ok {
		return nil, notFound("storage", obj.Metadata.Name)
	}
	return s.store(obj), nil
}

func (s *fakeStorages) Get(_ context.Context, name string, _ gpupaas.GetOptions) (*apiv1.Storage, error) {
	if x, ok := s.f.storages[s.key(name)]; ok {
		return x.DeepCopyObject().(*apiv1.Storage), nil
	}
	return nil, notFound("storage", name)
}

func (s *fakeStorages) List(_ context.Context, _ gpupaas.ListOptions) (*apiv1.StorageList, error) {
	out := &apiv1.StorageList{}
	for _, x := range s.f.storages {
		if x.Metadata.Project == s.project && (s.workspace == "" || x.Metadata.Workspace == s.workspace) {
			out.Items = append(out.Items, *x.DeepCopyObject().(*apiv1.Storage))
		}
	}
	return out, nil
}

func (s *fakeStorages) Delete(_ context.Context, name string, opts gpupaas.DeleteOptions) error {
	if _, ok := s.f.storages[s.key(name)]; !ok {
		if opts.IgnoreNotFound {
			return nil
		}
		return notFound("storage", name)
	}
	delete(s.f.storages, s.key(name))
	return nil
}

// ---- BaremetalMachines ----------------------------------------------------
//
// BaremetalMachine is project-scoped only (no workspace), so the storage key
// is just (project, name).

// ---- MKSClusters ----------------------------------------------------------

// The fake stores a cluster at once in the RUNNING phase. Behaviour that depends on timing
// (provisioning, flapping, teardown) is covered by the provider acceptance tests, which run
// against the gpupaas-go cluster mock.

type fakeMKSClusters struct {
	f         *fakeV1alpha1
	project   string
	workspace string
}

func (m *fakeMKSClusters) key(name string) string { return m.project + "/" + m.workspace + "/" + name }

func (m *fakeMKSClusters) store(obj *apiv1.MKSCluster, bump bool) *apiv1.MKSCluster {
	cp := obj.DeepCopyObject().(*apiv1.MKSCluster)
	cp.TypeMeta = apiv1.TypeMeta{APIVersion: "paas.k8smgmt.io/v3", Kind: apiv1.KindMKSCluster}
	cp.Metadata.Project = m.project
	cp.Metadata.Workspace = m.workspace
	if bump {
		cp.Version = obj.Version + 1
	} else if cp.Version == 0 {
		cp.Version = 1
	}
	cp.Status = apiv1.MKSClusterStatus{Status: "MKS_CLUSTER_STATUS_RUNNING", Reason: "Cluster is ready", Action: "deploy"}
	m.f.mksClusters[m.key(cp.Metadata.Name)] = cp
	return cp.DeepCopyObject().(*apiv1.MKSCluster)
}

func (m *fakeMKSClusters) Create(_ context.Context, obj *apiv1.MKSCluster, _ gpupaas.CreateOptions) (*apiv1.MKSCluster, error) {
	if _, ok := m.f.mksClusters[m.key(obj.Metadata.Name)]; ok {
		return nil, &gpupaas.APIError{StatusCode: 409, Message: fmt.Sprintf("mks cluster %q already exists", obj.Metadata.Name)}
	}
	return m.store(obj, false), nil
}

func (m *fakeMKSClusters) Update(_ context.Context, obj *apiv1.MKSCluster, _ gpupaas.UpdateOptions) (*apiv1.MKSCluster, error) {
	existing, ok := m.f.mksClusters[m.key(obj.Metadata.Name)]
	if !ok {
		return m.store(obj, false), nil
	}
	cp := obj.DeepCopyObject().(*apiv1.MKSCluster)
	cp.Version = existing.Version
	return m.store(cp, true), nil
}

func (m *fakeMKSClusters) Get(_ context.Context, name string, _ gpupaas.GetOptions) (*apiv1.MKSCluster, error) {
	if x, ok := m.f.mksClusters[m.key(name)]; ok {
		return x.DeepCopyObject().(*apiv1.MKSCluster), nil
	}
	return nil, notFound("mks cluster", name)
}

func (m *fakeMKSClusters) List(_ context.Context, _ gpupaas.ListOptions) (*apiv1.MKSClusterList, error) {
	out := &apiv1.MKSClusterList{TypeMeta: apiv1.TypeMeta{APIVersion: "paas.k8smgmt.io/v3", Kind: "ClusterList"}}
	for _, x := range m.f.mksClusters {
		if x.Metadata.Project == m.project && (m.workspace == "" || x.Metadata.Workspace == m.workspace) {
			out.Items = append(out.Items, *x.DeepCopyObject().(*apiv1.MKSCluster))
		}
	}
	return out, nil
}

func (m *fakeMKSClusters) Delete(_ context.Context, name string, opts gpupaas.DeleteOptions) error {
	if _, ok := m.f.mksClusters[m.key(name)]; !ok {
		if opts.IgnoreNotFound {
			return nil
		}
		return notFound("mks cluster", name)
	}
	delete(m.f.mksClusters, m.key(name))
	return nil
}

// Kubeconfig returns a fixed kubeconfig for an existing cluster.
func (m *fakeMKSClusters) Kubeconfig(_ context.Context, name string) ([]byte, error) {
	if _, ok := m.f.mksClusters[m.key(name)]; !ok {
		return nil, notFound("mks cluster", name)
	}
	return []byte("apiVersion: v1\nkind: Config\ncurrent-context: " + name + "\n"), nil
}

// The dedicated day-2, status and audit calls are exercised against the gpupaas-go cluster mock by the
// provider acceptance tests. The fake keeps them out of reach with an explicit error.
var errFakeUnsupported = &gpupaas.APIError{StatusCode: 501, Message: "not supported by the fake client"}

func (m *fakeMKSClusters) AddNodePool(context.Context, string, apiv1.MKSNodeGroup) (*apiv1.MKSCluster, error) {
	return nil, errFakeUnsupported
}
func (m *fakeMKSClusters) RemoveNodePool(context.Context, string, string) (*apiv1.MKSCluster, error) {
	return nil, errFakeUnsupported
}
func (m *fakeMKSClusters) Upgrade(context.Context, string, apiv1.MKSUpgradeRequest) (*apiv1.MKSCluster, error) {
	return nil, errFakeUnsupported
}
func (m *fakeMKSClusters) UpdateMetadata(context.Context, string, apiv1.MKSMetadataPatch) (*apiv1.MKSCluster, error) {
	return nil, errFakeUnsupported
}
func (m *fakeMKSClusters) Redeploy(context.Context, string) (*apiv1.MKSCluster, error) {
	return nil, errFakeUnsupported
}
func (m *fakeMKSClusters) Status(context.Context, string) (*apiv1.MKSClusterStatusView, error) {
	return nil, errFakeUnsupported
}
func (m *fakeMKSClusters) AuditEvents(context.Context, string, typed.MKSAuditEventFilter) (*apiv1.MKSAuditEventList, error) {
	return nil, errFakeUnsupported
}

// ---- MKS cluster profiles and nodes ---------------------------------------

type fakeMKSProfiles struct {
	f       *fakeV1alpha1
	project string
}

func (p *fakeMKSProfiles) Get(_ context.Context, name string, _ gpupaas.GetOptions) (*apiv1.MKSClusterProfile, error) {
	if x, ok := p.f.mksProfiles[p.project+"/"+name]; ok {
		return x.DeepCopyObject().(*apiv1.MKSClusterProfile), nil
	}
	return nil, notFound("mks cluster profile", name)
}

func (p *fakeMKSProfiles) List(_ context.Context, _ gpupaas.ListOptions) (*apiv1.MKSClusterProfileList, error) {
	out := &apiv1.MKSClusterProfileList{}
	for _, x := range p.f.mksProfiles {
		if x.Metadata.Project == p.project {
			out.Items = append(out.Items, *x.DeepCopyObject().(*apiv1.MKSClusterProfile))
		}
	}
	return out, nil
}

func (p *fakeMKSProfiles) CreateOptions(context.Context, string) (*apiv1.MKSCreateOptions, error) {
	return nil, errFakeUnsupported
}
func (p *fakeMKSProfiles) AvailableCapacity(context.Context, string) (*apiv1.MKSAvailableCapacity, error) {
	return nil, errFakeUnsupported
}

type fakeMKSMatchmaker struct{}

func (fakeMKSMatchmaker) KubernetesVersions(context.Context) (*apiv1.MKSKubernetesVersions, error) {
	return nil, errFakeUnsupported
}
func (fakeMKSMatchmaker) AvailableOS(context.Context, typed.MKSMatchQuery) (*apiv1.MKSAvailableOS, error) {
	return nil, errFakeUnsupported
}
func (fakeMKSMatchmaker) AvailableImages(context.Context, typed.MKSMatchQuery) ([]apiv1.MKSImageOption, error) {
	return nil, errFakeUnsupported
}
func (fakeMKSMatchmaker) AvailableSpecs(context.Context, typed.MKSMatchQuery) ([]apiv1.MKSSpecOption, error) {
	return nil, errFakeUnsupported
}
func (fakeMKSMatchmaker) MatchSKUs(context.Context, typed.MKSMatchQuery) ([]apiv1.MKSSKURef, error) {
	return nil, errFakeUnsupported
}
func (fakeMKSMatchmaker) AvailableComputeSKUs(context.Context, typed.MKSMatchQuery) ([]apiv1.MKSInstanceType, error) {
	return nil, errFakeUnsupported
}

type fakeMKSNodes struct {
	f       *fakeV1alpha1
	project string
}

func (n *fakeMKSNodes) ListForCluster(_ context.Context, cluster string) (*apiv1.MKSNodeList, error) {
	out := &apiv1.MKSNodeList{}
	for _, x := range n.f.mksNodes {
		if x.Metadata.Project == n.project && x.Metadata.Labels["cluster_name"] == cluster {
			out.Items = append(out.Items, *x.DeepCopyObject().(*apiv1.MKSNode))
		}
	}
	return out, nil
}

// The pool operations are only recorded (PoolOps on the fake clientset).
func (n *fakeMKSNodes) CordonPool(_ context.Context, cluster, pool string) (*typed.MKSPoolOperationResult, error) {
	n.f.poolOps = append(n.f.poolOps, "cordon "+cluster+"/"+pool)
	return &typed.MKSPoolOperationResult{Requested: 1, Succeeded: 1}, nil
}

func (n *fakeMKSNodes) UncordonPool(_ context.Context, cluster, pool string) (*typed.MKSPoolOperationResult, error) {
	n.f.poolOps = append(n.f.poolOps, "uncordon "+cluster+"/"+pool)
	return &typed.MKSPoolOperationResult{Requested: 1, Succeeded: 1}, nil
}

func (n *fakeMKSNodes) DrainPool(_ context.Context, cluster, pool string, _ typed.MKSDrainOptions) (*typed.MKSPoolOperationResult, error) {
	n.f.poolOps = append(n.f.poolOps, "drain "+cluster+"/"+pool)
	return &typed.MKSPoolOperationResult{Requested: 1, Succeeded: 1}, nil
}

func (n *fakeMKSNodes) CordonNode(context.Context, string, string) error   { return errFakeUnsupported }
func (n *fakeMKSNodes) UncordonNode(context.Context, string, string) error { return errFakeUnsupported }
func (n *fakeMKSNodes) DrainNode(context.Context, string, string) error    { return errFakeUnsupported }
func (n *fakeMKSNodes) DeleteNode(context.Context, string, string) error   { return errFakeUnsupported }
func (n *fakeMKSNodes) UpdateNodeMetadata(context.Context, string, string, typed.MKSNodeMetadataPatch) error {
	return errFakeUnsupported
}
func (n *fakeMKSNodes) UpdateNodeTaints(context.Context, string, string, []apiv1.MKSTaint) error {
	return errFakeUnsupported
}

// ---- BareMetals (first-class) ---------------------------------------------

// The fake stores an instance at once in the running phase; timing-dependent behaviour is covered
// by the provider acceptance tests, which run against the gpupaas-go bare-metal mock.

type fakeBareMetals struct {
	f         *fakeV1alpha1
	project   string
	workspace string
}

func (m *fakeBareMetals) key(name string) string { return m.project + "/" + m.workspace + "/" + name }

func (m *fakeBareMetals) store(obj *apiv1.BareMetal) *apiv1.BareMetal {
	cp := obj.DeepCopyObject().(*apiv1.BareMetal)
	cp.TypeMeta = apiv1.TypeMeta{APIVersion: "dev.envmgmt.io/v1", Kind: apiv1.KindBareMetal}
	cp.Metadata.Project, cp.Metadata.Workspace = m.project, m.workspace
	cp.Spec.UserPassword = ""
	cp.Status = apiv1.BareMetalStatus{Status: "running", Reason: "Instance is running.", Action: "deploy"}
	m.f.bareMetals[m.key(cp.Metadata.Name)] = cp
	return cp.DeepCopyObject().(*apiv1.BareMetal)
}

func (m *fakeBareMetals) Create(_ context.Context, obj *apiv1.BareMetal, _ gpupaas.CreateOptions) (*apiv1.BareMetal, error) {
	if _, ok := m.f.bareMetals[m.key(obj.Metadata.Name)]; ok {
		return nil, &gpupaas.APIError{StatusCode: 409, Message: fmt.Sprintf("bare metal %q already exists", obj.Metadata.Name)}
	}
	return m.store(obj), nil
}

func (m *fakeBareMetals) Update(_ context.Context, obj *apiv1.BareMetal, _ gpupaas.UpdateOptions) (*apiv1.BareMetal, error) {
	return m.store(obj), nil
}

func (m *fakeBareMetals) Get(_ context.Context, name string, _ gpupaas.GetOptions) (*apiv1.BareMetal, error) {
	if x, ok := m.f.bareMetals[m.key(name)]; ok {
		return x.DeepCopyObject().(*apiv1.BareMetal), nil
	}
	return nil, notFound("bare metal", name)
}

func (m *fakeBareMetals) List(_ context.Context, _ gpupaas.ListOptions) (*apiv1.BareMetalList, error) {
	out := &apiv1.BareMetalList{TypeMeta: apiv1.TypeMeta{APIVersion: "dev.envmgmt.io/v1", Kind: "BareMetalList"}}
	for _, x := range m.f.bareMetals {
		if x.Metadata.Project == m.project && (m.workspace == "" || x.Metadata.Workspace == m.workspace) {
			out.Items = append(out.Items, *x.DeepCopyObject().(*apiv1.BareMetal))
		}
	}
	return out, nil
}

func (m *fakeBareMetals) Delete(_ context.Context, name string, opts gpupaas.DeleteOptions) error {
	if _, ok := m.f.bareMetals[m.key(name)]; !ok {
		if opts.IgnoreNotFound {
			return nil
		}
		return notFound("bare metal", name)
	}
	delete(m.f.bareMetals, m.key(name))
	return nil
}

func (m *fakeBareMetals) Action(_ context.Context, name, verb string, _ map[string]any) (*apiv1.BareMetal, error) {
	x, ok := m.f.bareMetals[m.key(name)]
	if !ok {
		return nil, notFound("bare metal", name)
	}
	x.Status.Action = verb
	switch verb {
	case "stop":
		x.Status.Status = "stopped"
	default:
		x.Status.Status = "running"
	}
	return x.DeepCopyObject().(*apiv1.BareMetal), nil
}

type fakeBmProfiles struct {
	f       *fakeV1alpha1
	project string
}

func (p *fakeBmProfiles) Get(_ context.Context, name string, _ gpupaas.GetOptions) (*apiv1.BmProfile, error) {
	if x, ok := p.f.bmProfiles[p.project+"/"+name]; ok {
		return x.DeepCopyObject().(*apiv1.BmProfile), nil
	}
	return nil, notFound("bm profile", name)
}

func (p *fakeBmProfiles) List(_ context.Context, _ gpupaas.ListOptions) (*apiv1.BmProfileList, error) {
	out := &apiv1.BmProfileList{}
	for _, x := range p.f.bmProfiles {
		if x.Metadata.Project == p.project {
			out.Items = append(out.Items, *x.DeepCopyObject().(*apiv1.BmProfile))
		}
	}
	return out, nil
}

// ---- VMInstances (first-class) --------------------------------------------

// The fake stores a VM at once in the running phase; timing-dependent behaviour is covered by the
// provider acceptance tests, which run against the gpupaas-go VM mock.

type fakeVMInstances struct {
	f         *fakeV1alpha1
	project   string
	workspace string
}

func (m *fakeVMInstances) key(name string) string { return m.project + "/" + m.workspace + "/" + name }

func (m *fakeVMInstances) store(obj *apiv1.VMInstance) *apiv1.VMInstance {
	cp := obj.DeepCopyObject().(*apiv1.VMInstance)
	cp.TypeMeta = apiv1.TypeMeta{APIVersion: "dev.envmgmt.io/v1", Kind: apiv1.KindVMInstance}
	cp.Metadata.Project, cp.Metadata.Workspace = m.project, m.workspace
	cp.Spec.GuestPassword = ""
	cp.Status = apiv1.VMInstanceStatus{Status: "running", Reason: "Instance is running.", Action: "deploy"}
	m.f.vmInstances[m.key(cp.Metadata.Name)] = cp
	return cp.DeepCopyObject().(*apiv1.VMInstance)
}

func (m *fakeVMInstances) Create(_ context.Context, obj *apiv1.VMInstance, _ gpupaas.CreateOptions) (*apiv1.VMInstance, error) {
	if _, ok := m.f.vmInstances[m.key(obj.Metadata.Name)]; ok {
		return nil, &gpupaas.APIError{StatusCode: 409, Message: fmt.Sprintf("virtual machine %q already exists", obj.Metadata.Name)}
	}
	return m.store(obj), nil
}

func (m *fakeVMInstances) Update(_ context.Context, obj *apiv1.VMInstance, _ gpupaas.UpdateOptions) (*apiv1.VMInstance, error) {
	return m.store(obj), nil
}

func (m *fakeVMInstances) Get(_ context.Context, name string, _ gpupaas.GetOptions) (*apiv1.VMInstance, error) {
	if x, ok := m.f.vmInstances[m.key(name)]; ok {
		return x.DeepCopyObject().(*apiv1.VMInstance), nil
	}
	return nil, notFound("virtual machine", name)
}

func (m *fakeVMInstances) List(_ context.Context, _ gpupaas.ListOptions) (*apiv1.VMInstanceList, error) {
	out := &apiv1.VMInstanceList{TypeMeta: apiv1.TypeMeta{APIVersion: "dev.envmgmt.io/v1", Kind: "VirtualMachineList"}}
	for _, x := range m.f.vmInstances {
		if x.Metadata.Project == m.project && (m.workspace == "" || x.Metadata.Workspace == m.workspace) {
			out.Items = append(out.Items, *x.DeepCopyObject().(*apiv1.VMInstance))
		}
	}
	return out, nil
}

func (m *fakeVMInstances) Delete(_ context.Context, name string, opts gpupaas.DeleteOptions) error {
	if _, ok := m.f.vmInstances[m.key(name)]; !ok {
		if opts.IgnoreNotFound {
			return nil
		}
		return notFound("virtual machine", name)
	}
	delete(m.f.vmInstances, m.key(name))
	return nil
}

func (m *fakeVMInstances) Action(_ context.Context, name, verb string, _ map[string]any) (*apiv1.VMInstance, error) {
	x, ok := m.f.vmInstances[m.key(name)]
	if !ok {
		return nil, notFound("virtual machine", name)
	}
	x.Status.Action = verb
	switch verb {
	case "stop":
		x.Status.Status = "stopped"
	default:
		x.Status.Status = "running"
	}
	return x.DeepCopyObject().(*apiv1.VMInstance), nil
}

type fakeVMProfiles struct {
	f       *fakeV1alpha1
	project string
}

func (p *fakeVMProfiles) Get(_ context.Context, name string, _ gpupaas.GetOptions) (*apiv1.VMProfile, error) {
	if x, ok := p.f.vmProfiles[p.project+"/"+name]; ok {
		return x.DeepCopyObject().(*apiv1.VMProfile), nil
	}
	return nil, notFound("vm profile", name)
}

func (p *fakeVMProfiles) List(_ context.Context, _ gpupaas.ListOptions) (*apiv1.VMProfileList, error) {
	out := &apiv1.VMProfileList{}
	for _, x := range p.f.vmProfiles {
		if x.Metadata.Project == p.project {
			out.Items = append(out.Items, *x.DeepCopyObject().(*apiv1.VMProfile))
		}
	}
	return out, nil
}
