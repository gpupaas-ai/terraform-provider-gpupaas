package resources

import (
	"context"
	"fmt"
	"math"
	"strings"

	gpupaas "github.com/gpupaas-ai/gpupaas-go"
	apiv1 "github.com/gpupaas-ai/gpupaas-go/apis/v1alpha1"
	typed "github.com/gpupaas-ai/gpupaas-go/clientset/typed/v1alpha1"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// readVMProfile looks the SKU up in the VM's project and then in the system catalog. It returns nil when
// it cannot be read: validation is then skipped and the API remains the authority.
func (r *vmResource) readVMProfile(ctx context.Context, project, name string) *apiv1.VMProfile {
	return readVMProfile(ctx, r.pd.Clientset.V1alpha1(), project, name)
}

func readVMProfile(ctx context.Context, cs typed.Interface, project, name string) *apiv1.VMProfile {
	if name == "" {
		return nil
	}
	prof, err := getVMProfile(ctx, cs, project, name)
	if err != nil {
		return nil
	}
	return prof
}

// getVMProfile reads a SKU from the given project and, when the project has none of that name, from the
// system catalog. It returns the error of the first lookup when both fail, so a caller can show why the SKU
// could not be read.
func getVMProfile(ctx context.Context, cs typed.Interface, project, name string) (*apiv1.VMProfile, error) {
	projects := []string{project}
	if project != systemCatalogProject {
		projects = append(projects, systemCatalogProject)
	}
	var first error
	for _, p := range projects {
		if p == "" {
			continue
		}
		prof, err := cs.VMProfiles(p).Get(ctx, name, gpupaas.GetOptions{})
		if err == nil {
			return prof, nil
		}
		if first == nil {
			first = err
		}
		if !gpupaas.IsNotFound(err) {
			return nil, err // only a missing SKU falls back to the catalog
		}
	}
	return nil, first
}

// ModifyPlan checks the planned VM against its SKU, so a wrong image, size or datacenter fails at plan time
// with the allowed values instead of minutes into an apply, and fills the size from spec.preset.
//
// A value is an error only on create and when it changed: Terraform also plans with the configuration during
// destroy, and a VM whose SKU was edited since it was created must stay destroyable, so an unchanged value
// that no longer fits is only a warning.
func (r *vmResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() || r.pd == nil {
		return
	}
	var plan, cfg vmResourceModel
	resp.Diagnostics.Append(decodePlan(ctx, req.Plan, &plan)...)
	resp.Diagnostics.Append(decodeConfig(ctx, req.Config, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var prior *vmSpecModel
	if !req.State.Raw.IsNull() {
		var state vmResourceModel
		resp.Diagnostics.Append(decodeState(ctx, req.State, &state)...)
		if resp.Diagnostics.HasError() {
			return
		}
		prior = &state.Spec
	}
	root := path.Root("spec")
	var sshKey, password types.String
	resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, root.AtName("ssh_key"), &sshKey)...)
	resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, root.AtName("guest_password"), &password)...)
	if prior == nil && !set(sshKey) && !set(password) && !planCredentialPending(ctx, req.Plan, "ssh_key", "guest_password") {
		resp.Diagnostics.AddAttributeError(root, "No way to log in",
			"Set spec.ssh_key, spec.guest_password, or both: the platform needs at least one to create the VM.")
	}
	if set(cfg.Spec.Preset) && (set(cfg.Spec.CPUCount) || set(cfg.Spec.Memory) || set(cfg.Spec.GPUCount)) {
		resp.Diagnostics.AddAttributeError(root.AtName("preset"), "Preset and size both set",
			"Set spec.preset, or spec.cpu_count and spec.memory, not both: the preset already fixes the size.")
	}
	// A size input that is still unknown (it comes from something applied in this run) cannot be checked yet.
	sizeUnknown := cfg.Spec.Preset.IsUnknown() || cfg.Spec.CPUCount.IsUnknown() || cfg.Spec.Memory.IsUnknown() || cfg.Spec.GPUCount.IsUnknown()
	explicit := !set(cfg.Spec.Preset) && (set(cfg.Spec.CPUCount) || set(cfg.Spec.Memory) || set(cfg.Spec.GPUCount))
	if explicit && !sizeUnknown && !(set(cfg.Spec.CPUCount) && set(cfg.Spec.Memory)) {
		resp.Diagnostics.AddAttributeError(root.AtName("cpu_count"), "cpu_count and memory go together",
			"The platform matches the size as a whole against one preset of the SKU: set both spec.cpu_count and spec.memory (and spec.gpu_count for a GPU SKU), or use spec.preset.")
	}
	profile := r.readVMProfile(ctx, plan.Metadata.Project.ValueString(), plan.Spec.Type.ValueString())
	if profile == nil {
		return
	}
	if explicit && !sizeUnknown {
		switch {
		case !profile.Spec.GPUsEnabled && set(cfg.Spec.GPUCount):
			resp.Diagnostics.AddAttributeError(root.AtName("gpu_count"), "SKU has no GPUs", fmt.Sprintf("SKU %q is not a GPU SKU: leave spec.gpu_count unset.", profile.Metadata.Name))
		case profile.Spec.GPUsEnabled && !set(cfg.Spec.GPUCount):
			resp.Diagnostics.AddAttributeError(root.AtName("gpu_count"), "GPU count required", fmt.Sprintf("SKU %q has GPUs: set spec.preset or spec.gpu_count with spec.cpu_count and spec.memory.", profile.Metadata.Name))
		}
	}
	if set(cfg.Spec.Preset) {
		if _, found := vmPresetByName(profile, cfg.Spec.Preset.ValueString()); !found {
			var names []string
			for _, row := range profile.Spec.Sizes() {
				names = append(names, row.Name)
			}
			detail := fmt.Sprintf("SKU %q has these presets: %s; got %q.%s", profile.Metadata.Name, strings.Join(names, ", "), cfg.Spec.Preset.ValueString(), didYouMean(cfg.Spec.Preset.ValueString(), names))
			if prior == nil || prior.Preset.ValueString() != cfg.Spec.Preset.ValueString() {
				resp.Diagnostics.AddAttributeError(root.AtName("preset"), "Unknown preset", detail)
			} else {
				resp.Diagnostics.AddAttributeWarning(root.AtName("preset"), "Unknown preset", detail+" The value is unchanged, so it is not blocking this plan.")
			}
		}
	}
	// The size as planned now (preset resolved) is what gets validated and sent. With an unknown input the size
	// is left out of the checks.
	planned := plan.Spec
	if sizeUnknown {
		// Unknown, not null: a null size reads as "not configured" and a GPU SKU would be refused for a missing
		// gpu_count that is only known after apply.
		planned.CPUCount, planned.Memory, planned.GPUCount = types.StringUnknown(), types.StringUnknown(), types.StringUnknown()
	} else {
		r.planSize(cfg.Spec, plan.Spec, prior, profile, resp)
		if size, ok := plannedVMSize(cfg.Spec, profile); ok && (prior == nil || explicit || set(cfg.Spec.Preset)) {
			planned.CPUCount, planned.Memory, planned.GPUCount = types.StringValue(size.cpu), types.StringValue(size.memory), nullableString(size.gpu)
		} else {
			planned.CPUCount, planned.Memory, planned.GPUCount = types.StringNull(), types.StringNull(), types.StringNull()
		}
	}
	// The platform's own default boot disk (40 GB) can be more than the SKU or the host allows, so a new VM gets
	// the SKU's minimum unless a size is configured.
	if prior == nil && cfg.Spec.BootDiskSize.IsNull() && profile.Spec.BootDisk != nil {
		if lo, ok := numeric(profile.Spec.BootDisk.MinSize); ok && lo > 0 {
			resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, root.AtName("boot_disk_size"), types.Int64Value(int64(lo)))...)
			planned.BootDiskSize = types.Int64Value(int64(lo))
		}
	}
	validateVMAgainstProfile(planned, prior, profile, &resp.Diagnostics)
}

// plannedVMSize is the size the configuration asks for: the named preset's row, the configured cpu_count and
// memory, or the SKU's only preset when nothing was configured and the SKU has exactly one.
func plannedVMSize(cfg vmSpecModel, p *apiv1.VMProfile) (vmSize, bool) {
	if set(cfg.Preset) {
		row, ok := vmPresetByName(p, cfg.Preset.ValueString())
		if !ok {
			return vmSize{}, false
		}
		return vmSize{cpu: row.CPU, memory: row.Memory, gpu: row.GPU}, true
	}
	if set(cfg.CPUCount) || set(cfg.Memory) || set(cfg.GPUCount) {
		return vmSize{cpu: stringOr(cfg.CPUCount, ""), memory: stringOr(cfg.Memory, ""), gpu: stringOr(cfg.GPUCount, "")}, true
	}
	if sizes := p.Spec.Sizes(); len(sizes) == 1 {
		return vmSize{cpu: sizes[0].CPU, memory: sizes[0].Memory, gpu: sizes[0].GPU}, true
	}
	return vmSize{}, false
}

// planSize fills cpu_count, memory and gpu_count in the plan from spec.preset (or the SKU's only preset on
// create) and marks the VM for replacement when a changed preset changes the size of an existing VM: the
// attribute-level plan modifiers have already run on the old values by the time this runs.
//
// An existing VM keeps its size unless the configuration changed: when the SKU's row of an unchanged preset
// was edited since the VM was created, that is only a warning, never a replacement.
func (r *vmResource) planSize(cfg, plan vmSpecModel, prior *vmSpecModel, p *apiv1.VMProfile, resp *resource.ModifyPlanResponse) {
	root := path.Root("spec")
	if set(cfg.CPUCount) || set(cfg.Memory) || set(cfg.GPUCount) {
		return // explicit sizes are known in the plan and replace through the attribute modifiers
	}
	if prior != nil {
		if !set(cfg.Preset) {
			return // no size in the configuration: the VM keeps the one it has
		}
		if prior.Preset.ValueString() == cfg.Preset.ValueString() {
			if row, ok := vmPresetByName(p, cfg.Preset.ValueString()); ok && !sameVMSize(row, *prior) {
				resp.Diagnostics.AddAttributeWarning(root.AtName("preset"), "The SKU's preset changed",
					fmt.Sprintf("SKU %q now lists preset %q as cpu_count %s, memory %s; this VM keeps the size it was created with (cpu_count %s, memory %s). Change the preset to replace it.",
						p.Metadata.Name, row.Name, row.CPU, row.Memory, prior.CPUCount.ValueString(), prior.Memory.ValueString()))
			}
			return
		}
	}
	size, ok := plannedVMSize(cfg, p)
	if !ok {
		if prior == nil && !set(cfg.Preset) {
			var names []string
			for _, row := range p.Spec.Sizes() {
				names = append(names, row.Name)
			}
			resp.Diagnostics.AddAttributeError(root, "Choose a size",
				fmt.Sprintf("SKU %q offers several sizes (%s). Set spec.preset to one of them, or spec.cpu_count and spec.memory.", p.Metadata.Name, strings.Join(names, ", ")))
		}
		return
	}
	put := func(attr, value string) {
		at := root.AtName(attr)
		if value == "" {
			return
		}
		resp.Diagnostics.Append(resp.Plan.SetAttribute(context.Background(), at, types.StringValue(value))...)
		if prior != nil {
			var was types.String
			switch attr {
			case "cpu_count":
				was = prior.CPUCount
			case "memory":
				was = prior.Memory
			case "gpu_count":
				was = prior.GPUCount
			}
			if !was.IsNull() && !was.IsUnknown() && was.ValueString() != value {
				resp.RequiresReplace = append(resp.RequiresReplace, at)
			}
		}
	}
	put("cpu_count", size.cpu)
	put("memory", size.memory)
	put("gpu_count", size.gpu)
}

// sameVMSize reports whether a preset row is the size a VM was created with.
func sameVMSize(row apiv1.VMPreset, s vmSpecModel) bool {
	return row.CPU == s.CPUCount.ValueString() && row.Memory == s.Memory.ValueString() && row.GPU == s.GPUCount.ValueString()
}

// validateVMAgainstProfile checks image, datacenter, size, boot disk and public IP against what the SKU
// allows. prior is nil on create.
func validateVMAgainstProfile(s vmSpecModel, prior *vmSpecModel, p *apiv1.VMProfile, diags *diag.Diagnostics) {
	root := path.Root("spec")
	name := p.Metadata.Name
	// report adds an error for a new or changed value and a warning for an unchanged one.
	report := func(at path.Path, changed bool, summary, detail string) {
		switch {
		case changed:
			diags.AddAttributeError(at, summary, detail)
		default:
			diags.AddAttributeWarning(at, summary, detail+" The value is unchanged, so it is not blocking this plan.")
		}
	}
	changedStr := func(now types.String, was func(vmSpecModel) types.String) bool {
		return prior == nil || now.ValueString() != was(*prior).ValueString()
	}

	checkIn := func(attr, label string, v types.String, allowed []string, was func(vmSpecModel) types.String) {
		if !set(v) || len(allowed) == 0 {
			return
		}
		for _, a := range allowed {
			if strings.EqualFold(a, v.ValueString()) {
				return
			}
		}
		report(root.AtName(attr), changedStr(v, was), "Not allowed by the SKU",
			fmt.Sprintf("SKU %q allows these %s values: %s; got %q.%s", name, label, strings.Join(allowed, ", "), v.ValueString(), didYouMean(v.ValueString(), allowed)))
	}
	checkIn("image", "image", s.Image, p.Spec.Image, func(m vmSpecModel) types.String { return m.Image })
	checkIn("datacenter", "datacenter", s.Datacenter, p.Spec.Datacenter, func(m vmSpecModel) types.String { return m.Datacenter })

	// The size must match one preset row as a tuple.
	if sizes := p.Spec.Sizes(); len(sizes) > 0 && (set(s.CPUCount) || set(s.Memory)) {
		match := false
		for _, row := range sizes {
			if row.CPU == s.CPUCount.ValueString() && row.Memory == s.Memory.ValueString() && (!p.Spec.GPUsEnabled || row.GPU == s.GPUCount.ValueString()) {
				match = true
			}
		}
		if !match {
			var rows []string
			for _, row := range sizes {
				desc := fmt.Sprintf("%s (cpu_count %s, memory %s", row.Name, row.CPU, row.Memory)
				if row.GPU != "" {
					desc += ", gpu_count " + row.GPU
				}
				rows = append(rows, desc+")")
			}
			changed := prior == nil || prior.CPUCount.ValueString() != s.CPUCount.ValueString() || prior.Memory.ValueString() != s.Memory.ValueString()
			report(root.AtName("cpu_count"), changed, "Size not offered by the SKU",
				fmt.Sprintf("SKU %q offers these sizes: %s. cpu_count %q with memory %q is not one of them; use spec.preset to pick one.", name, strings.Join(rows, "; "), s.CPUCount.ValueString(), s.Memory.ValueString()))
		}
	}
	if p.Spec.GPUsEnabled && prior == nil && !set(s.GPUCount) && !s.GPUCount.IsUnknown() {
		diags.AddAttributeError(root.AtName("gpu_count"), "GPU count required", fmt.Sprintf("SKU %q has GPUs: set spec.preset or spec.gpu_count.", name))
	}

	if size := s.BootDiskSize; !size.IsNull() && !size.IsUnknown() && p.Spec.BootDisk != nil && (prior == nil || prior.BootDiskSize.ValueInt64() != size.ValueInt64()) {
		lo, okLo := numeric(p.Spec.BootDisk.MinSize)
		hi, okHi := numeric(p.Spec.BootDisk.MaxSize)
		step, okStep := numeric(p.Spec.BootDisk.Increment)
		v := float64(size.ValueInt64())
		off := false
		if okStep && okLo && step > 0 {
			rem := math.Mod(v-lo, step)
			off = rem > 1e-9 && step-rem > 1e-9
		}
		if (okLo && v < lo) || (okHi && hi > 0 && v > hi) || off {
			diags.AddAttributeError(root.AtName("boot_disk_size"), "Boot disk size not allowed by the SKU",
				fmt.Sprintf("SKU %q allows boot disks of %s to %s %s in steps of %s above the minimum; got %d.", name, p.Spec.BootDisk.MinSize, p.Spec.BootDisk.MaxSize, strings.ToUpper(p.Spec.BootDisk.Unit), p.Spec.BootDisk.Increment, size.ValueInt64()))
		}
	}

	if ip := s.AssignPublicIP; !ip.IsNull() && !ip.IsUnknown() {
		changed := prior == nil || prior.AssignPublicIP.ValueBool() != ip.ValueBool()
		switch {
		case ip.ValueBool() && strings.EqualFold(p.Spec.PublicIP, "None"):
			report(root.AtName("assign_public_ip"), changed, "Public IP not allowed", fmt.Sprintf("SKU %q does not assign public IPs (mode None).", name))
		case !ip.ValueBool() && strings.EqualFold(p.Spec.PublicIP, "Always"):
			// the platform pins the address on, so a configured false would differ from the refreshed true forever
			report(root.AtName("assign_public_ip"), changed, "Public IP is always assigned",
				fmt.Sprintf("SKU %q always assigns a public IP (mode Always): remove assign_public_ip or set it to true.", name))
		}
	}

	// A SKU without a healthy orchestration agent cannot provision: the VM would fail with "no healthy
	// agents found" after the create call. The agent list is not a health check, so only an empty list is
	// reported.
	if prior == nil && p.Status.GlobalSettings != nil && len(p.Status.GlobalSettings.Agents) == 0 {
		diags.AddAttributeWarning(root.AtName("type"), "SKU has no orchestration agent",
			fmt.Sprintf("SKU %q lists no agents in its settings, so provisioning is likely to fail with \"no healthy agents found\". Check that a healthy agent is assigned to the SKU's datacenter.", name))
	}
}
