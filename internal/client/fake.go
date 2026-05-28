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
// All resources are stored keyed by (project,workspace,name).
func NewFakeClientset() clientset.Interface {
	return &fakeClientset{v1: newFakeV1alpha1()}
}

type fakeClientset struct{ v1 *fakeV1alpha1 }

func (f *fakeClientset) V1alpha1() typed.Interface { return f.v1 }

func newFakeV1alpha1() *fakeV1alpha1 {
	return &fakeV1alpha1{
		projects:          map[string]*apiv1.Project{},
		workspaces:        map[string]*apiv1.Workspace{},
		collaborators:     map[string]*apiv1.WorkspaceCollaborator{},
		vms:               map[string]*apiv1.VirtualMachine{},
		storages:          map[string]*apiv1.Storage{},
		securityGroups:    map[string]*apiv1.SecurityGroup{},
		sshKeys:           map[string]*apiv1.SshKey{},
		baremetalMachines: map[string]*apiv1.BaremetalMachine{},
	}
}

type fakeV1alpha1 struct {
	projects          map[string]*apiv1.Project
	workspaces        map[string]*apiv1.Workspace
	collaborators     map[string]*apiv1.WorkspaceCollaborator
	vms               map[string]*apiv1.VirtualMachine
	storages          map[string]*apiv1.Storage
	securityGroups    map[string]*apiv1.SecurityGroup
	sshKeys           map[string]*apiv1.SshKey
	baremetalMachines map[string]*apiv1.BaremetalMachine
}

func (f *fakeV1alpha1) Projects() typed.ProjectInterface { return &fakeProjects{f: f} }
func (f *fakeV1alpha1) Workspaces(project string) typed.WorkspaceInterface {
	return &fakeWorkspaces{f: f, project: project}
}
func (f *fakeV1alpha1) VirtualMachines(project string) typed.VirtualMachineInterface {
	return &fakeVMs{f: f, project: project}
}
func (f *fakeV1alpha1) Storages(project string) typed.StorageInterface {
	return &fakeStorages{f: f, project: project}
}
func (f *fakeV1alpha1) SecurityGroups(project string) typed.SecurityGroupInterface {
	return &fakeSGs{f: f, project: project}
}
func (f *fakeV1alpha1) SshKeys(project string) typed.SshKeyInterface {
	return &fakeSshKeys{f: f, project: project}
}
func (f *fakeV1alpha1) BaremetalMachines(project string) typed.BaremetalMachineInterface {
	return &fakeBaremetals{f: f, project: project}
}

func notFound(kind, name string) error {
	return &gpupaas.APIError{StatusCode: 404, Message: fmt.Sprintf("%s %q not found", kind, name)}
}

// ---- Projects -------------------------------------------------------------

type fakeProjects struct{ f *fakeV1alpha1 }

func (p *fakeProjects) Create(_ context.Context, obj *apiv1.Project, _ gpupaas.CreateOptions) (*apiv1.Project, error) {
	cp := obj.DeepCopyObject().(*apiv1.Project)
	cp.TypeMeta = apiv1.TypeMeta{APIVersion: apiv1.APIVersion, Kind: apiv1.KindProject}
	p.f.projects[cp.Metadata.Name] = cp
	return cp, nil
}
func (p *fakeProjects) Get(_ context.Context, name string, _ gpupaas.GetOptions) (*apiv1.Project, error) {
	if v, ok := p.f.projects[name]; ok {
		return v.DeepCopyObject().(*apiv1.Project), nil
	}
	return nil, notFound("project", name)
}
func (p *fakeProjects) Update(_ context.Context, obj *apiv1.Project, _ gpupaas.UpdateOptions) (*apiv1.Project, error) {
	if _, ok := p.f.projects[obj.Metadata.Name]; !ok {
		return nil, notFound("project", obj.Metadata.Name)
	}
	cp := obj.DeepCopyObject().(*apiv1.Project)
	p.f.projects[cp.Metadata.Name] = cp
	return cp, nil
}
func (p *fakeProjects) Delete(_ context.Context, name string, opts gpupaas.DeleteOptions) error {
	if _, ok := p.f.projects[name]; !ok {
		if opts.IgnoreNotFound {
			return nil
		}
		return notFound("project", name)
	}
	delete(p.f.projects, name)
	return nil
}
func (p *fakeProjects) List(_ context.Context, _ gpupaas.ListOptions) (*apiv1.ProjectList, error) {
	out := &apiv1.ProjectList{TypeMeta: apiv1.TypeMeta{APIVersion: apiv1.APIVersion, Kind: apiv1.KindProject + "List"}}
	for _, v := range p.f.projects {
		out.Items = append(out.Items, *v.DeepCopyObject().(*apiv1.Project))
	}
	return out, nil
}

// ---- Workspaces -----------------------------------------------------------

type fakeWorkspaces struct {
	f       *fakeV1alpha1
	project string
}

func (w *fakeWorkspaces) key(name string) string { return w.project + "/" + name }

func (w *fakeWorkspaces) Create(_ context.Context, obj *apiv1.Workspace, _ gpupaas.CreateOptions) (*apiv1.Workspace, error) {
	cp := obj.DeepCopyObject().(*apiv1.Workspace)
	cp.TypeMeta = apiv1.TypeMeta{APIVersion: apiv1.APIVersion, Kind: apiv1.KindWorkspace}
	if cp.Metadata.Project == "" {
		cp.Metadata.Project = w.project
	}
	w.f.workspaces[w.key(cp.Metadata.Name)] = cp
	return cp, nil
}
func (w *fakeWorkspaces) Get(_ context.Context, name string, _ gpupaas.GetOptions) (*apiv1.Workspace, error) {
	if v, ok := w.f.workspaces[w.key(name)]; ok {
		return v.DeepCopyObject().(*apiv1.Workspace), nil
	}
	return nil, notFound("workspace", name)
}
func (w *fakeWorkspaces) Update(ctx context.Context, obj *apiv1.Workspace, _ gpupaas.UpdateOptions) (*apiv1.Workspace, error) {
	return w.Create(ctx, obj, gpupaas.CreateOptions{})
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
func (w *fakeWorkspaces) List(_ context.Context, _ gpupaas.ListOptions) (*apiv1.WorkspaceList, error) {
	out := &apiv1.WorkspaceList{TypeMeta: apiv1.TypeMeta{APIVersion: apiv1.APIVersion, Kind: apiv1.KindWorkspace + "List"}}
	for _, v := range w.f.workspaces {
		if v.Metadata.Project == w.project {
			out.Items = append(out.Items, *v.DeepCopyObject().(*apiv1.Workspace))
		}
	}
	return out, nil
}
func (w *fakeWorkspaces) Collaborators(workspace string) typed.WorkspaceCollaboratorInterface {
	return &fakeCollabs{f: w.f, project: w.project, workspace: workspace}
}
func (w *fakeWorkspaces) VirtualMachines(workspace string) typed.VirtualMachineInterface {
	return &fakeVMs{f: w.f, project: w.project, workspace: workspace}
}
func (w *fakeWorkspaces) Storages(workspace string) typed.StorageInterface {
	return &fakeStorages{f: w.f, project: w.project, workspace: workspace}
}
func (w *fakeWorkspaces) SecurityGroups(workspace string) typed.SecurityGroupInterface {
	return &fakeSGs{f: w.f, project: w.project, workspace: workspace}
}
func (w *fakeWorkspaces) SshKeys(workspace string) typed.SshKeyInterface {
	return &fakeSshKeys{f: w.f, project: w.project, workspace: workspace}
}

// ---- WorkspaceCollaborators -----------------------------------------------

type fakeCollabs struct {
	f         *fakeV1alpha1
	project   string
	workspace string
}

func (c *fakeCollabs) key(name string) string {
	return c.project + "/" + c.workspace + "/" + name
}

func (c *fakeCollabs) Create(_ context.Context, obj *apiv1.WorkspaceCollaborator, _ gpupaas.CreateOptions) (*apiv1.WorkspaceCollaborator, error) {
	cp := obj.DeepCopyObject().(*apiv1.WorkspaceCollaborator)
	cp.TypeMeta = apiv1.TypeMeta{APIVersion: apiv1.APIVersion, Kind: apiv1.KindWorkspaceCollaborator}
	if cp.Metadata.Project == "" {
		cp.Metadata.Project = c.project
	}
	if cp.Metadata.Workspace == "" {
		cp.Metadata.Workspace = c.workspace
	}
	if cp.Metadata.Name == "" {
		cp.Metadata.Name = cp.CollaboratorUsername()
	}
	c.f.collaborators[c.key(cp.Metadata.Name)] = cp
	return cp, nil
}
func (c *fakeCollabs) Get(_ context.Context, name string, _ gpupaas.GetOptions) (*apiv1.WorkspaceCollaborator, error) {
	if v, ok := c.f.collaborators[c.key(name)]; ok {
		return v.DeepCopyObject().(*apiv1.WorkspaceCollaborator), nil
	}
	return nil, notFound("workspace collaborator", name)
}
func (c *fakeCollabs) Delete(_ context.Context, name string, opts gpupaas.DeleteOptions) error {
	if _, ok := c.f.collaborators[c.key(name)]; !ok {
		if opts.IgnoreNotFound {
			return nil
		}
		return notFound("workspace collaborator", name)
	}
	delete(c.f.collaborators, c.key(name))
	return nil
}
func (c *fakeCollabs) List(_ context.Context, _ gpupaas.ListOptions) (*apiv1.WorkspaceCollaboratorList, error) {
	out := &apiv1.WorkspaceCollaboratorList{}
	for _, v := range c.f.collaborators {
		if v.Metadata.Project == c.project && v.Metadata.Workspace == c.workspace {
			out.Items = append(out.Items, *v.DeepCopyObject().(*apiv1.WorkspaceCollaborator))
		}
	}
	return out, nil
}

// ---- Generic dev-resource helpers -----------------------------------------

func scopeKey(project, workspace, name string) string {
	return project + "/" + workspace + "/" + name
}

// ---- VirtualMachines ------------------------------------------------------

type fakeVMs struct {
	f         *fakeV1alpha1
	project   string
	workspace string
}

func (v *fakeVMs) key(name string) string { return scopeKey(v.project, v.workspace, name) }

func (v *fakeVMs) Create(_ context.Context, obj *apiv1.VirtualMachine, _ gpupaas.CreateOptions) (*apiv1.VirtualMachine, error) {
	cp := obj.DeepCopyObject().(*apiv1.VirtualMachine)
	cp.TypeMeta = apiv1.TypeMeta{APIVersion: apiv1.APIVersion, Kind: apiv1.KindVirtualMachine}
	if cp.Metadata.Project == "" {
		cp.Metadata.Project = v.project
	}
	if cp.Metadata.Workspace == "" {
		cp.Metadata.Workspace = v.workspace
	}
	v.f.vms[v.key(cp.Metadata.Name)] = cp
	return cp, nil
}
func (v *fakeVMs) Get(_ context.Context, name string, _ gpupaas.GetOptions) (*apiv1.VirtualMachine, error) {
	if x, ok := v.f.vms[v.key(name)]; ok {
		return x.DeepCopyObject().(*apiv1.VirtualMachine), nil
	}
	return nil, notFound("virtual machine", name)
}
func (v *fakeVMs) List(_ context.Context, _ gpupaas.ListOptions) (*apiv1.VirtualMachineList, error) {
	out := &apiv1.VirtualMachineList{}
	for _, x := range v.f.vms {
		if x.Metadata.Project == v.project && x.Metadata.Workspace == v.workspace {
			out.Items = append(out.Items, *x.DeepCopyObject().(*apiv1.VirtualMachine))
		}
	}
	return out, nil
}
func (v *fakeVMs) Delete(_ context.Context, name string, opts gpupaas.DeleteOptions) error {
	if _, ok := v.f.vms[v.key(name)]; !ok {
		if opts.IgnoreNotFound {
			return nil
		}
		return notFound("virtual machine", name)
	}
	delete(v.f.vms, v.key(name))
	return nil
}
func (v *fakeVMs) GetStatus(ctx context.Context, name string, opts gpupaas.GetOptions) (*apiv1.VirtualMachine, error) {
	return v.Get(ctx, name, opts)
}
func (v *fakeVMs) Start(ctx context.Context, name string, _ gpupaas.ActionOptions) (*apiv1.VirtualMachine, error) {
	x, err := v.Get(ctx, name, gpupaas.GetOptions{})
	if err != nil {
		return nil, err
	}
	x.Status.Action = "start"
	v.f.vms[v.key(name)] = x
	return x, nil
}
func (v *fakeVMs) Stop(ctx context.Context, name string, _ gpupaas.ActionOptions) (*apiv1.VirtualMachine, error) {
	x, err := v.Get(ctx, name, gpupaas.GetOptions{})
	if err != nil {
		return nil, err
	}
	x.Status.Action = "stop"
	v.f.vms[v.key(name)] = x
	return x, nil
}
func (v *fakeVMs) Reboot(ctx context.Context, name string, opts gpupaas.ActionOptions) (*apiv1.VirtualMachine, error) {
	return v.Action(ctx, name, "reboot", opts)
}
func (v *fakeVMs) Action(ctx context.Context, name, action string, _ gpupaas.ActionOptions) (*apiv1.VirtualMachine, error) {
	x, err := v.Get(ctx, name, gpupaas.GetOptions{})
	if err != nil {
		return nil, err
	}
	x.Status.Action = action
	v.f.vms[v.key(name)] = x
	return x, nil
}

// ---- Storage --------------------------------------------------------------

type fakeStorages struct {
	f         *fakeV1alpha1
	project   string
	workspace string
}

func (s *fakeStorages) key(name string) string { return scopeKey(s.project, s.workspace, name) }

func (s *fakeStorages) Create(_ context.Context, obj *apiv1.Storage, _ gpupaas.CreateOptions) (*apiv1.Storage, error) {
	cp := obj.DeepCopyObject().(*apiv1.Storage)
	cp.TypeMeta = apiv1.TypeMeta{APIVersion: apiv1.APIVersion, Kind: apiv1.KindStorage}
	if cp.Metadata.Project == "" {
		cp.Metadata.Project = s.project
	}
	if cp.Metadata.Workspace == "" {
		cp.Metadata.Workspace = s.workspace
	}
	s.f.storages[s.key(cp.Metadata.Name)] = cp
	return cp, nil
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
		if x.Metadata.Project == s.project && x.Metadata.Workspace == s.workspace {
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

// ---- SecurityGroups -------------------------------------------------------

type fakeSGs struct {
	f         *fakeV1alpha1
	project   string
	workspace string
}

func (s *fakeSGs) key(name string) string { return scopeKey(s.project, s.workspace, name) }

func (s *fakeSGs) Create(_ context.Context, obj *apiv1.SecurityGroup, _ gpupaas.CreateOptions) (*apiv1.SecurityGroup, error) {
	cp := obj.DeepCopyObject().(*apiv1.SecurityGroup)
	cp.TypeMeta = apiv1.TypeMeta{APIVersion: apiv1.APIVersion, Kind: apiv1.KindSecurityGroup}
	if cp.Metadata.Project == "" {
		cp.Metadata.Project = s.project
	}
	if cp.Metadata.Workspace == "" {
		cp.Metadata.Workspace = s.workspace
	}
	s.f.securityGroups[s.key(cp.Metadata.Name)] = cp
	return cp, nil
}
func (s *fakeSGs) Get(_ context.Context, name string, _ gpupaas.GetOptions) (*apiv1.SecurityGroup, error) {
	if x, ok := s.f.securityGroups[s.key(name)]; ok {
		return x.DeepCopyObject().(*apiv1.SecurityGroup), nil
	}
	return nil, notFound("security group", name)
}
func (s *fakeSGs) List(_ context.Context, _ gpupaas.ListOptions) (*apiv1.SecurityGroupList, error) {
	out := &apiv1.SecurityGroupList{}
	for _, x := range s.f.securityGroups {
		if x.Metadata.Project == s.project && x.Metadata.Workspace == s.workspace {
			out.Items = append(out.Items, *x.DeepCopyObject().(*apiv1.SecurityGroup))
		}
	}
	return out, nil
}
func (s *fakeSGs) Delete(_ context.Context, name string, opts gpupaas.DeleteOptions) error {
	if _, ok := s.f.securityGroups[s.key(name)]; !ok {
		if opts.IgnoreNotFound {
			return nil
		}
		return notFound("security group", name)
	}
	delete(s.f.securityGroups, s.key(name))
	return nil
}

// ---- SshKeys --------------------------------------------------------------

type fakeSshKeys struct {
	f         *fakeV1alpha1
	project   string
	workspace string
}

func (s *fakeSshKeys) key(name string) string { return scopeKey(s.project, s.workspace, name) }

func (s *fakeSshKeys) Create(_ context.Context, obj *apiv1.SshKey, _ gpupaas.CreateOptions) (*apiv1.SshKey, error) {
	cp := obj.DeepCopyObject().(*apiv1.SshKey)
	cp.TypeMeta = apiv1.TypeMeta{APIVersion: apiv1.APIVersion, Kind: apiv1.KindSshKey}
	if cp.Metadata.Project == "" {
		cp.Metadata.Project = s.project
	}
	if cp.Metadata.Workspace == "" {
		cp.Metadata.Workspace = s.workspace
	}
	s.f.sshKeys[s.key(cp.Metadata.Name)] = cp
	return cp, nil
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

// ---- BaremetalMachines ----------------------------------------------------
//
// BaremetalMachine is project-scoped only (no workspace), so the storage key
// is just (project, name).

type fakeBaremetals struct {
	f       *fakeV1alpha1
	project string
}

func (b *fakeBaremetals) key(name string) string { return b.project + "/" + name }

func (b *fakeBaremetals) Create(_ context.Context, obj *apiv1.BaremetalMachine, _ gpupaas.CreateOptions) (*apiv1.BaremetalMachine, error) {
	cp := obj.DeepCopyObject().(*apiv1.BaremetalMachine)
	cp.TypeMeta = apiv1.TypeMeta{APIVersion: apiv1.APIVersion, Kind: apiv1.KindBaremetalMachine}
	if cp.Metadata.Project == "" {
		cp.Metadata.Project = b.project
	}
	b.f.baremetalMachines[b.key(cp.Metadata.Name)] = cp
	return cp.DeepCopyObject().(*apiv1.BaremetalMachine), nil
}

func (b *fakeBaremetals) Get(_ context.Context, name string, _ gpupaas.GetOptions) (*apiv1.BaremetalMachine, error) {
	if x, ok := b.f.baremetalMachines[b.key(name)]; ok {
		return x.DeepCopyObject().(*apiv1.BaremetalMachine), nil
	}
	return nil, notFound("baremetal machine", name)
}

func (b *fakeBaremetals) List(_ context.Context, _ gpupaas.ListOptions) (*apiv1.BaremetalMachineList, error) {
	out := &apiv1.BaremetalMachineList{
		TypeMeta: apiv1.TypeMeta{APIVersion: apiv1.APIVersion, Kind: apiv1.KindBaremetalMachine + "List"},
	}
	for _, x := range b.f.baremetalMachines {
		if x.Metadata.Project == b.project {
			out.Items = append(out.Items, *x.DeepCopyObject().(*apiv1.BaremetalMachine))
		}
	}
	return out, nil
}

func (b *fakeBaremetals) Delete(_ context.Context, name string, opts gpupaas.DeleteOptions) error {
	if _, ok := b.f.baremetalMachines[b.key(name)]; !ok {
		if opts.IgnoreNotFound {
			return nil
		}
		return notFound("baremetal machine", name)
	}
	delete(b.f.baremetalMachines, b.key(name))
	return nil
}

// setOnline flips spec.online on the stored copy and returns a deep copy.
func (b *fakeBaremetals) setOnline(ctx context.Context, name string, online bool) (*apiv1.BaremetalMachine, error) {
	x, err := b.Get(ctx, name, gpupaas.GetOptions{})
	if err != nil {
		return nil, err
	}
	v := online
	x.Spec.Online = &v
	b.f.baremetalMachines[b.key(name)] = x
	return x.DeepCopyObject().(*apiv1.BaremetalMachine), nil
}

// appendCondition records an action as a status condition so tests can assert
// against observable state.
func (b *fakeBaremetals) appendCondition(ctx context.Context, name, condType, reason string) (*apiv1.BaremetalMachine, error) {
	x, err := b.Get(ctx, name, gpupaas.GetOptions{})
	if err != nil {
		return nil, err
	}
	x.Status.Conditions = append(x.Status.Conditions, apiv1.BaremetalMachineCondition{
		Type:   condType,
		Status: "Success",
		Reason: reason,
	})
	b.f.baremetalMachines[b.key(name)] = x
	return x.DeepCopyObject().(*apiv1.BaremetalMachine), nil
}

func (b *fakeBaremetals) PowerOn(ctx context.Context, name string, _ gpupaas.ActionOptions) (*apiv1.BaremetalMachine, error) {
	return b.setOnline(ctx, name, true)
}

func (b *fakeBaremetals) PowerOff(ctx context.Context, name string, _ gpupaas.ActionOptions) (*apiv1.BaremetalMachine, error) {
	return b.setOnline(ctx, name, false)
}

func (b *fakeBaremetals) Reboot(ctx context.Context, name string, _ gpupaas.ActionOptions) (*apiv1.BaremetalMachine, error) {
	return b.appendCondition(ctx, name, "Rebooted", "reboot")
}

func (b *fakeBaremetals) Provision(ctx context.Context, name string, _ gpupaas.ActionOptions) (*apiv1.BaremetalMachine, error) {
	return b.appendCondition(ctx, name, "Provisioned", "provision")
}

func (b *fakeBaremetals) ReinstallOS(ctx context.Context, name string, image *apiv1.BaremetalImage, _ gpupaas.ActionOptions) (*apiv1.BaremetalMachine, error) {
	x, err := b.Get(ctx, name, gpupaas.GetOptions{})
	if err != nil {
		return nil, err
	}
	if image != nil {
		img := *image
		x.Spec.Image = &img
	}
	x.Status.Conditions = append(x.Status.Conditions, apiv1.BaremetalMachineCondition{
		Type:   "ReinstallOS",
		Status: "Success",
		Reason: "reinstallOS",
	})
	b.f.baremetalMachines[b.key(name)] = x
	return x.DeepCopyObject().(*apiv1.BaremetalMachine), nil
}

func (b *fakeBaremetals) CreateConsoleSession(ctx context.Context, name string, req *apiv1.BaremetalConsoleSessionRequest, _ gpupaas.ActionOptions) (*apiv1.BaremetalConsoleSession, error) {
	if _, err := b.Get(ctx, name, gpupaas.GetOptions{}); err != nil {
		return nil, err
	}
	computeID := ""
	if req != nil {
		computeID = req.ComputeID
	}
	return &apiv1.BaremetalConsoleSession{
		SessionID:      "fake-session-" + name,
		AgentSessionID: "fake-agent-" + computeID,
		ConsoleURL:     fmt.Sprintf("ws://fake/console/%s/%s", b.project, name),
	}, nil
}

func (b *fakeBaremetals) GetStatusInfo(ctx context.Context, name string, _ gpupaas.GetOptions) (*apiv1.BaremetalMachineInfo, error) {
	if _, err := b.Get(ctx, name, gpupaas.GetOptions{}); err != nil {
		return nil, err
	}
	return &apiv1.BaremetalMachineInfo{
		Data: apiv1.BaremetalMachineData{
			Fields: map[string]interface{}{
				"project": b.project,
				"name":    name,
			},
		},
	}, nil
}
