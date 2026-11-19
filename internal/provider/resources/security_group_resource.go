package resources

import (
	"context"

	gpupaas "github.com/gpupaas-ai/gpupaas-go"
	apiv1 "github.com/gpupaas-ai/gpupaas-go/apis/v1alpha1"
	typed "github.com/gpupaas-ai/gpupaas-go/clientset/typed/v1alpha1"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/objectplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/gpupaas-ai/terraform-provider-gpupaas/internal/client"
)

var (
	_ resource.Resource                = (*securityGroupResource)(nil)
	_ resource.ResourceWithImportState = (*securityGroupResource)(nil)
	_ resource.ResourceWithConfigure   = (*securityGroupResource)(nil)
)

// NewSecurityGroupResource constructs the gpupaas_security_group resource.
func NewSecurityGroupResource() resource.Resource { return &securityGroupResource{} }

type securityGroupResource struct{ pd *client.ProviderData }

// securityRuleModel is one access rule.
type securityRuleModel struct {
	SourceCIDR      types.String `tfsdk:"source_cidr"`
	Application     types.String `tfsdk:"application"`
	ApplicationPort types.String `tfsdk:"application_port"`
	Protocol        types.String `tfsdk:"protocol"`
	Action          types.String `tfsdk:"action"`
}

type securityGroupSpecModel struct {
	Rules   []securityRuleModel `tfsdk:"rules"`
	Sharing *SharingModel       `tfsdk:"sharing"`
}

type securityGroupResourceModel struct {
	ID       types.String           `tfsdk:"id"`
	Metadata scopedMetadataModel    `tfsdk:"metadata"`
	Spec     securityGroupSpecModel `tfsdk:"spec"`
	Status   sshKeyStatusModel      `tfsdk:"status"`
}

func (r *securityGroupResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_security_group"
}

func (r *securityGroupResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	sharing := SharingResourceAttribute()
	sharing.PlanModifiers = []planmodifier.Object{objectplanmodifier.RequiresReplace()}
	resp.Schema = schema.Schema{
		MarkdownDescription: "A security group (`dev.envmgmt.io/v1` `SecurityGroup`): the set of rules that decides which traffic may reach the instances that use it. At project or workspace scope. " +
			"The platform has no update for a security group: changing any argument deletes the group and creates it again, so detach it from instances first. " +
			"Name it from a VM, bare-metal machine or cluster with `security_group`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "`<project>/<workspace>/<name>`, or `<project>/<name>` for a project-scoped group.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"metadata": scopedMetadataAttribute("security group"),
			"spec": schema.SingleNestedAttribute{
				Required: true,
				Attributes: map[string]schema.Attribute{
					"rules": schema.ListNestedAttribute{
						Required:            true,
						MarkdownDescription: "The access rules. Changing a rule, or adding or removing one, replaces the group. Listing the same rules in a different order changes nothing.",
						PlanModifiers: []planmodifier.List{listplanmodifier.RequiresReplaceIf(rulesChanged,
							"Replaces the group when a rule is added, removed or changed. A new order of the same rules does not.",
							"Replaces the group when a rule is added, removed or changed. A new order of the same rules does not.")},
						NestedObject: schema.NestedAttributeObject{
							Attributes: map[string]schema.Attribute{
								"source_cidr":      schema.StringAttribute{Required: true, MarkdownDescription: "Source address range, such as `0.0.0.0/0` or `10.0.0.0/8`."},
								"application":      schema.StringAttribute{Required: true, MarkdownDescription: "The service the rule is for, such as `ssh`, `http` or `https`."},
								"application_port": schema.StringAttribute{Optional: true, MarkdownDescription: "Port, when the service needs one that is not its default."},
								"protocol":         schema.StringAttribute{Required: true, MarkdownDescription: "Transport protocol, such as `tcp`."},
								"action":           schema.StringAttribute{Required: true, MarkdownDescription: "`allow` or `deny`."},
							},
						},
					},
					"sharing": sharing,
				},
			},
			"status": schema.SingleNestedAttribute{
				Computed: true,
				Attributes: map[string]schema.Attribute{
					"status": schema.StringAttribute{Computed: true, MarkdownDescription: "Platform status, `success` once the group is stored."},
					"reason": schema.StringAttribute{Computed: true},
				},
			},
		},
	}
}

func (r *securityGroupResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.pd = providerData(req.ProviderData, &resp.Diagnostics)
}

func (r *securityGroupResource) groups(project, workspace string) typed.SecurityGroupInterface {
	if workspace == "" {
		return r.pd.Clientset.V1alpha1().SecurityGroups(project)
	}
	return r.pd.Clientset.V1alpha1().Workspaces(project).SecurityGroups(workspace)
}

func (r *securityGroupResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	if r.pd == nil {
		resp.Diagnostics.AddError("Provider not configured", "Provider data is nil; check provider configuration.")
		return
	}
	var plan securityGroupResourceModel
	resp.Diagnostics.Append(decodePlan(ctx, req.Plan, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	project, workspace := plan.Metadata.Project.ValueString(), stringOr(plan.Metadata.Workspace, "")
	obj := &apiv1.SecurityGroup{
		TypeMeta: apiv1.TypeMeta{APIVersion: apiv1.APIVersion, Kind: apiv1.KindSecurityGroup},
		Metadata: apiv1.ObjectMeta{Name: plan.Metadata.Name.ValueString(), Project: project, Workspace: workspace},
		Spec: apiv1.SecurityGroupSpec{
			Rules:   rulesToSDK(plan.Spec.Rules),
			Sharing: sharingToSDK(ctx, plan.Spec.Sharing, &resp.Diagnostics),
		},
	}
	if resp.Diagnostics.HasError() {
		return
	}
	out, err := r.groups(project, workspace).Create(ctx, obj, gpupaas.CreateOptions{})
	if err != nil {
		handleAPIError(&resp.Diagnostics, err, "Create security group", nil)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, securityGroupModelFromServer(ctx, plan, out, &resp.Diagnostics))...)
}

func (r *securityGroupResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	if r.pd == nil {
		return
	}
	var state securityGroupResourceModel
	resp.Diagnostics.Append(decodeState(ctx, req.State, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	project, workspace := state.Metadata.Project.ValueString(), stringOr(state.Metadata.Workspace, "")
	out, err := r.groups(project, workspace).Get(ctx, state.Metadata.Name.ValueString(), gpupaas.GetOptions{})
	if err != nil {
		handleAPIError(&resp.Diagnostics, err, "Read security group", func() { resp.State.RemoveResource(ctx) })
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, securityGroupModelFromServer(ctx, state, out, &resp.Diagnostics))...)
}

// Update is reached only when the same rules were listed in a new order: every real change forces a replacement.
// The platform has nothing to change, so the new order is only recorded in the state.
func (r *securityGroupResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state securityGroupResourceModel
	resp.Diagnostics.Append(decodePlan(ctx, req.Plan, &plan)...)
	resp.Diagnostics.Append(decodeState(ctx, req.State, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !sameRulesAnyOrder(plan.Spec.Rules, state.Spec.Rules) {
		resp.Diagnostics.AddError("Security groups cannot be updated", "The platform has no update for a security group; change it by replacing it. This is a provider bug: the change should have planned a replacement.")
		return
	}
	plan.ID = state.ID
	plan.Status = state.Status
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *securityGroupResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	if r.pd == nil {
		return
	}
	var state securityGroupResourceModel
	resp.Diagnostics.Append(decodeState(ctx, req.State, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	project, workspace := state.Metadata.Project.ValueString(), stringOr(state.Metadata.Workspace, "")
	if err := r.groups(project, workspace).Delete(ctx, state.Metadata.Name.ValueString(), gpupaas.DeleteOptions{IgnoreNotFound: true}); err != nil {
		handleAPIError(&resp.Diagnostics, err, "Delete security group", nil)
	}
}

func (r *securityGroupResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	project, workspace, name, err := ParseFlexibleScopedImportID(req.ID)
	if err != nil {
		resp.Diagnostics.AddError("Invalid import ID", "Use <project>/<workspace>/<name> or <project>/<name>: "+err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("metadata").AtName("project"), project)...)
	if workspace != "" {
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("metadata").AtName("workspace"), workspace)...)
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("metadata").AtName("name"), name)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), scopedID(project, workspace, name))...)
}

func rulesToSDK(in []securityRuleModel) []apiv1.Rule {
	out := make([]apiv1.Rule, 0, len(in))
	for _, r := range in {
		out = append(out, apiv1.Rule{
			SourceCIDR:      r.SourceCIDR.ValueString(),
			Application:     r.Application.ValueString(),
			ApplicationPort: stringOr(r.ApplicationPort, ""),
			Protocol:        r.Protocol.ValueString(),
			Action:          r.Action.ValueString(),
		})
	}
	return out
}

func ruleModelFromSDK(r apiv1.Rule) securityRuleModel {
	return securityRuleModel{
		SourceCIDR:      nullableString(r.SourceCIDR),
		Application:     nullableString(r.Application),
		ApplicationPort: nullableString(r.ApplicationPort),
		Protocol:        nullableString(r.Protocol),
		Action:          nullableString(r.Action),
	}
}

// sameRule reports whether the configured rule and the platform's rule are the same rule. The platform may fill a port
// the configuration left out (a named service has its default port), which is not a change; a port the
// configuration did set must match.
func sameRule(cfg securityRuleModel, srv apiv1.Rule) bool {
	if cfg.SourceCIDR.ValueString() != srv.SourceCIDR || cfg.Application.ValueString() != srv.Application ||
		cfg.Protocol.ValueString() != srv.Protocol || cfg.Action.ValueString() != srv.Action {
		return false
	}
	return !set(cfg.ApplicationPort) || cfg.ApplicationPort.ValueString() == srv.ApplicationPort
}

// rulesChanged is the replace condition of the rules: true when a rule was added, removed or changed. A list of
// the same rules in another order is not a change (the platform has no order to keep).
func rulesChanged(ctx context.Context, req planmodifier.ListRequest, resp *listplanmodifier.RequiresReplaceIfFuncResponse) {
	if req.PlanValue.IsUnknown() || req.PlanValue.IsNull() || req.StateValue.IsNull() || req.StateValue.IsUnknown() {
		resp.RequiresReplace = true
		return
	}
	var plan, state []securityRuleModel
	if req.PlanValue.ElementsAs(ctx, &plan, false).HasError() || req.StateValue.ElementsAs(ctx, &state, false).HasError() {
		resp.RequiresReplace = true
		return
	}
	resp.RequiresReplace = !sameRulesAnyOrder(plan, state)
}

// sameRulesAnyOrder reports whether a and b hold the same rules, each as many times, in any order.
func sameRulesAnyOrder(a, b []securityRuleModel) bool {
	if len(a) != len(b) {
		return false
	}
	used := make([]bool, len(b))
	for _, ra := range a {
		found := false
		for j, rb := range b {
			if !used[j] && sameRuleModel(ra, rb) {
				used[j], found = true, true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func sameRuleModel(a, b securityRuleModel) bool {
	return a.SourceCIDR.ValueString() == b.SourceCIDR.ValueString() &&
		a.Application.ValueString() == b.Application.ValueString() &&
		a.ApplicationPort.ValueString() == b.ApplicationPort.ValueString() &&
		a.Protocol.ValueString() == b.Protocol.ValueString() &&
		a.Action.ValueString() == b.Action.ValueString()
}

// serverRulesMatchAnyOrder reports whether the platform holds exactly the configured rules, in any order. A
// configured rule matches a stored one the way sameRule does (a port left out matches any port).
func serverRulesMatchAnyOrder(cfg []securityRuleModel, srv []apiv1.Rule) bool {
	if len(cfg) != len(srv) {
		return false
	}
	used := make([]bool, len(srv))
	for _, c := range cfg {
		found := false
		for j, sr := range srv {
			if !used[j] && sameRule(c, sr) {
				used[j], found = true, true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// securityGroupModelFromServer builds the state from what the platform returned. base holds the configured
// values (plan or prior state): rules that are the same as the configured ones keep the configured form, so a
// default the platform fills in does not look like drift, while a rule that differs is shown as it is.
func securityGroupModelFromServer(ctx context.Context, base securityGroupResourceModel, srv *apiv1.SecurityGroup, diags *diag.Diagnostics) securityGroupResourceModel {
	out := base
	project, workspace := base.Metadata.Project.ValueString(), stringOr(base.Metadata.Workspace, "")
	out.ID = types.StringValue(scopedID(project, workspace, srv.Metadata.Name))
	out.Metadata.Name = types.StringValue(srv.Metadata.Name)
	if srv.Metadata.Name == "" {
		out.Metadata.Name = base.Metadata.Name
	}
	rules := make([]securityRuleModel, 0, len(srv.Spec.Rules))
	if serverRulesMatchAnyOrder(base.Spec.Rules, srv.Spec.Rules) {
		// The same rules, whatever their order: keep the configured list, or a reorder would show as a change.
		rules = append(rules, base.Spec.Rules...)
	} else {
		for i, sr := range srv.Spec.Rules {
			if i < len(base.Spec.Rules) && sameRule(base.Spec.Rules[i], sr) {
				rules = append(rules, base.Spec.Rules[i])
				continue
			}
			rules = append(rules, ruleModelFromSDK(sr))
		}
	}
	out.Spec.Rules = rules
	if isDefaultSharing(srv.Spec.Sharing) && !sharingConfigured(base.Spec.Sharing) {
		out.Spec.Sharing = nil
	} else {
		out.Spec.Sharing = sharingFromSDK(ctx, srv.Spec.Sharing, base.Spec.Sharing, diags)
	}
	out.Status = sshKeyStatusModel{Status: nullableString(srv.Status.Status), Reason: nullableString(srv.Status.Reason)}
	return out
}
