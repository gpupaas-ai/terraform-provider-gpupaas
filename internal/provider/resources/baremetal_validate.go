package resources

import (
	"context"
	"fmt"
	"strings"

	gpupaas "github.com/gpupaas-ai/gpupaas-go"
	apiv1 "github.com/gpupaas-ai/gpupaas-go/apis/v1alpha1"
	typed "github.com/gpupaas-ai/gpupaas-go/clientset/typed/v1alpha1"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// readBmProfile looks the SKU up in the instance's project and then in the system catalog. It
// returns nil when it cannot be read: validation is then skipped and the API remains the authority.
func (r *bareMetalResource) readBmProfile(ctx context.Context, project, name string) *apiv1.BmProfile {
	return readBmProfile(ctx, r.pd.Clientset.V1alpha1(), project, name)
}

func readBmProfile(ctx context.Context, cs typed.Interface, project, name string) *apiv1.BmProfile {
	if name == "" {
		return nil
	}
	prof, err := getBmProfile(ctx, cs, project, name)
	if err != nil {
		return nil
	}
	return prof
}

// getBmProfile reads a SKU from the given project and, when the project has none of that name,
// from the system catalog. It returns the error of the first lookup when both fail, so a caller can
// show why the SKU could not be read.
func getBmProfile(ctx context.Context, cs typed.Interface, project, name string) (*apiv1.BmProfile, error) {
	projects := []string{project}
	if project != systemCatalogProject {
		projects = append(projects, systemCatalogProject)
	}
	var first error
	for _, p := range projects {
		if p == "" {
			continue
		}
		prof, err := cs.BmProfiles(p).Get(ctx, name, gpupaas.GetOptions{})
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

// ModifyPlan checks the planned instance against its SKU, so a wrong image, datacenter or option
// fails at plan time with the allowed values instead of minutes into an apply.
//
// A value is an error only on create and when it changed: Terraform also plans with the
// configuration during destroy, and an instance whose SKU was edited since it was created must
// stay destroyable, so an unchanged value that no longer fits is only a warning.
func (r *bareMetalResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() || r.pd == nil {
		return
	}
	var plan bareMetalResourceModel
	resp.Diagnostics.Append(decodePlan(ctx, req.Plan, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var prior *bareMetalSpecModel
	if !req.State.Raw.IsNull() {
		var state bareMetalResourceModel
		resp.Diagnostics.Append(decodeState(ctx, req.State, &state)...)
		if resp.Diagnostics.HasError() {
			return
		}
		prior = &state.Spec
	}
	var sshKey, password types.String
	resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, path.Root("spec").AtName("ssh_key"), &sshKey)...)
	resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, path.Root("spec").AtName("user_password"), &password)...)
	if prior == nil && !set(sshKey) && !set(password) && !planCredentialPending(ctx, req.Plan, "ssh_key", "user_password") {
		resp.Diagnostics.AddAttributeError(path.Root("spec"), "No way to log in",
			"Set spec.ssh_key, spec.user_password, or both: the platform needs at least one to create the machine.")
	}
	if profile := r.readBmProfile(ctx, plan.Metadata.Project.ValueString(), plan.Spec.Type.ValueString()); profile != nil {
		validateBareMetalAgainstProfile(plan.Spec, prior, profile, &resp.Diagnostics)
	}
}

// validateBareMetalAgainstProfile checks image, datacenter, public IP and contract term against
// what the SKU allows. prior is nil on create.
func validateBareMetalAgainstProfile(s bareMetalSpecModel, prior *bareMetalSpecModel, p *apiv1.BmProfile, diags *diag.Diagnostics) {
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
	changedStr := func(now types.String, was func(bareMetalSpecModel) types.String) bool {
		return prior == nil || now.ValueString() != was(*prior).ValueString()
	}

	checkIn := func(attr, label string, v types.String, allowed []string, was func(bareMetalSpecModel) types.String) {
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
	checkIn("image", "image", s.Image, p.Spec.Image, func(m bareMetalSpecModel) types.String { return m.Image })
	checkIn("datacenter", "datacenter", s.Datacenter, p.Spec.Datacenter, func(m bareMetalSpecModel) types.String { return m.Datacenter })

	if ip := s.AssignPublicIP; !ip.IsNull() && !ip.IsUnknown() {
		changed := prior == nil || prior.AssignPublicIP.ValueBool() != ip.ValueBool()
		switch {
		case ip.ValueBool() && strings.EqualFold(p.Spec.AssignPublicIP, "Disabled"):
			report(root.AtName("assign_public_ip"), changed, "Public IP not allowed", fmt.Sprintf("SKU %q does not assign public IPs (policy Disabled).", name))
		case !ip.ValueBool() && strings.EqualFold(p.Spec.AssignPublicIP, "Enabled") && prior == nil:
			diags.AddAttributeWarning(root.AtName("assign_public_ip"), "Public IP is always assigned",
				fmt.Sprintf("SKU %q always assigns a public IP (policy Enabled); false has no effect.", name))
		}
	}

	if t := s.ContractTerm; !t.IsNull() && !t.IsUnknown() && t.ValueInt64() != 0 && len(p.Spec.MRCContractTerm) > 0 {
		ok := false
		var terms []string
		for _, v := range p.Spec.MRCContractTerm {
			terms = append(terms, fmt.Sprint(v))
			if int64(v) == t.ValueInt64() {
				ok = true
			}
		}
		if !ok {
			changed := prior == nil || prior.ContractTerm.ValueInt64() != t.ValueInt64()
			report(root.AtName("contract_term"), changed, "Contract term not offered",
				fmt.Sprintf("SKU %q offers these contract terms (months): %s; got %d.", name, strings.Join(terms, ", "), t.ValueInt64()))
		}
	}

	// A SKU without a healthy orchestration agent cannot provision: the instance would fail with
	// "no healthy agents found" after the create call. The agent list is not a health check, so
	// only an empty list is reported.
	if prior == nil && p.Status.GlobalSettings != nil && len(p.Status.GlobalSettings.Agents) == 0 {
		diags.AddAttributeWarning(root.AtName("type"), "SKU has no orchestration agent",
			fmt.Sprintf("SKU %q lists no agents in its settings, so provisioning is likely to fail with \"no healthy agents found\". Check that a healthy agent is assigned to the SKU's datacenter.", name))
	}
}
