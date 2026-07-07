package resources

import (
	"context"
	"sort"

	gpupaas "github.com/gpupaas-ai/gpupaas-go"
	apiv1 "github.com/gpupaas-ai/gpupaas-go/apis/v1alpha1"
	typed "github.com/gpupaas-ai/gpupaas-go/clientset/typed/v1alpha1"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
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

type securityGroupResourceModel struct {
	ID         types.String        `tfsdk:"id"`
	APIVersion types.String        `tfsdk:"api_version"`
	Kind       types.String        `tfsdk:"kind"`
	Metadata   MetadataModel       `tfsdk:"metadata"`
	Spec       securityGroupSpec   `tfsdk:"spec"`
	Status     securityGroupStatus `tfsdk:"status"`
}

type securityGroupSpec struct {
	SecurityGroup    resourceRefModel `tfsdk:"security_group"`
	Type             types.String     `tfsdk:"type"`
	IPRules          types.Set        `tfsdk:"ip_rules"`
	PortForwardRules types.Set        `tfsdk:"port_forward_rules"`
	Rules            types.Set        `tfsdk:"rules"`
	Sharing          *SharingModel    `tfsdk:"sharing"`
}

type securityGroupStatus struct {
	Status types.String `tfsdk:"status"`
	Reason types.String `tfsdk:"reason"`
	Action types.String `tfsdk:"action"`
}

type ipRuleModel struct {
	SourceCIDR  types.String `tfsdk:"source_cidr"`
	Application types.String `tfsdk:"application"`
	Action      types.String `tfsdk:"action"`
}

type portForwardRuleModel struct {
	SourceCIDR      types.String `tfsdk:"source_cidr"`
	Application     types.String `tfsdk:"application"`
	ApplicationPort types.String `tfsdk:"application_port"`
	Protocol        types.String `tfsdk:"protocol"`
}

type ruleModel struct {
	SourceCIDR      types.String `tfsdk:"source_cidr"`
	Application     types.String `tfsdk:"application"`
	ApplicationPort types.String `tfsdk:"application_port"`
	Protocol        types.String `tfsdk:"protocol"`
	Action          types.String `tfsdk:"action"`
}

var ipRuleObjectType = types.ObjectType{AttrTypes: map[string]attr.Type{
	"source_cidr": types.StringType,
	"application": types.StringType,
	"action":      types.StringType,
}}

var portForwardRuleObjectType = types.ObjectType{AttrTypes: map[string]attr.Type{
	"source_cidr":      types.StringType,
	"application":      types.StringType,
	"application_port": types.StringType,
	"protocol":         types.StringType,
}}

var ruleObjectType = types.ObjectType{AttrTypes: map[string]attr.Type{
	"source_cidr":      types.StringType,
	"application":      types.StringType,
	"application_port": types.StringType,
	"protocol":         types.StringType,
	"action":           types.StringType,
}}

func (r *securityGroupResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_security_group"
}

func (r *securityGroupResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "GPU PaaS security group (`apiVersion: " + apiv1.APIVersion + "`, `kind: SecurityGroup`). Supports project- or workspace-scoped placement via metadata.workspace. " +
			"`rules`/`ip_rules`/`port_forward_rules` and `sharing` are mutable Day-2 (set semantics: order never causes drift); everything else is immutable after creation.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"api_version": schema.StringAttribute{Computed: true},
			"kind":        schema.StringAttribute{Computed: true},
			"metadata":    immutableMetadataAttribute(true, true),
			"spec": schema.SingleNestedAttribute{
				Required: true,
				Attributes: map[string]schema.Attribute{
					"security_group": immutableResourceRefSchema("Security group catalog reference."),
					"type": schema.StringAttribute{
						Optional:      true,
						PlanModifiers: []planmodifier.String{immutableString()},
					},
					"ip_rules": schema.SetNestedAttribute{
						Optional:            true,
						MarkdownDescription: "IP-based rules. Mutable Day-2; order never causes drift.",
						NestedObject: schema.NestedAttributeObject{
							Attributes: map[string]schema.Attribute{
								"source_cidr": schema.StringAttribute{Optional: true},
								"application": schema.StringAttribute{Optional: true},
								"action":      schema.StringAttribute{Optional: true},
							},
						},
					},
					"port_forward_rules": schema.SetNestedAttribute{
						Optional:            true,
						MarkdownDescription: "Port forwarding rules. Mutable Day-2; order never causes drift.",
						NestedObject: schema.NestedAttributeObject{
							Attributes: map[string]schema.Attribute{
								"source_cidr":      schema.StringAttribute{Optional: true},
								"application":      schema.StringAttribute{Optional: true},
								"application_port": schema.StringAttribute{Optional: true},
								"protocol":         schema.StringAttribute{Optional: true},
							},
						},
					},
					"rules": schema.SetNestedAttribute{
						Optional:            true,
						MarkdownDescription: "General rules. Mutable Day-2; order never causes drift.",
						NestedObject: schema.NestedAttributeObject{
							Attributes: map[string]schema.Attribute{
								"source_cidr":      schema.StringAttribute{Optional: true},
								"application":      schema.StringAttribute{Optional: true},
								"application_port": schema.StringAttribute{Optional: true},
								"protocol":         schema.StringAttribute{Optional: true},
								"action":           schema.StringAttribute{Optional: true},
							},
						},
					},
					// sharing stays mutable Day-2 (D4).
					"sharing": SharingResourceAttribute(),
				},
			},
			"status": schema.SingleNestedAttribute{
				Computed: true,
				Attributes: map[string]schema.Attribute{
					"status": schema.StringAttribute{Computed: true},
					"reason": schema.StringAttribute{Computed: true},
					"action": schema.StringAttribute{Computed: true},
				},
			},
		},
	}
}

func (r *securityGroupResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.pd = providerData(req.ProviderData, &resp.Diagnostics)
}

func (r *securityGroupResource) client(project, workspace string) typed.SecurityGroupInterface {
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
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	project := plan.Metadata.Project.ValueString()
	workspace := plan.Metadata.Workspace.ValueString()
	obj := securityGroupModelToSDK(ctx, plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	out, err := r.client(project, workspace).Create(ctx, obj, gpupaas.CreateOptions{})
	if err != nil {
		handleAPIError(&resp.Diagnostics, err, "Create security group", nil)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, securityGroupSDKToModel(ctx, out, project, workspace, &resp.Diagnostics))...)
}

func (r *securityGroupResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	if r.pd == nil {
		return
	}
	var state securityGroupResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	project := state.Metadata.Project.ValueString()
	workspace := state.Metadata.Workspace.ValueString()
	out, err := r.client(project, workspace).Get(ctx, state.Metadata.Name.ValueString(), gpupaas.GetOptions{})
	if err != nil {
		handleAPIError(&resp.Diagnostics, err, "Read security group", func() { resp.State.RemoveResource(ctx) })
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, securityGroupSDKToModel(ctx, out, project, workspace, &resp.Diagnostics))...)
}

func (r *securityGroupResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	if r.pd == nil {
		return
	}
	var plan, state securityGroupResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	// Belt-and-braces: the schema-level immutable() modifiers already block
	// these edits at plan time; this is a defensive second gate against
	// modifier gaps (plan.md 3.3).
	if plan.Spec.SecurityGroup.Name.ValueString() != state.Spec.SecurityGroup.Name.ValueString() ||
		stringOr(plan.Spec.Type, "") != stringOr(state.Spec.Type, "") {
		resp.Diagnostics.AddError(
			"Immutable field changed",
			"security_group / type cannot be modified on an existing security group; destroy and recreate explicitly.",
		)
		return
	}
	project := plan.Metadata.Project.ValueString()
	workspace := plan.Metadata.Workspace.ValueString()
	obj := securityGroupModelToSDK(ctx, plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	out, err := r.client(project, workspace).Create(ctx, obj, gpupaas.CreateOptions{})
	if err != nil {
		handleAPIError(&resp.Diagnostics, err, "Update security group", nil)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, securityGroupSDKToModel(ctx, out, project, workspace, &resp.Diagnostics))...)
}

func (r *securityGroupResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	if r.pd == nil {
		return
	}
	var state securityGroupResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	project := state.Metadata.Project.ValueString()
	workspace := state.Metadata.Workspace.ValueString()
	err := r.client(project, workspace).Delete(ctx, state.Metadata.Name.ValueString(), gpupaas.DeleteOptions{IgnoreNotFound: true})
	if err != nil && !gpupaas.IsNotFound(err) {
		handleAPIError(&resp.Diagnostics, err, "Delete security group", nil)
	}
}

func (r *securityGroupResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	project, workspace, name, err := ParseFlexibleScopedImportID(req.ID)
	if err != nil {
		resp.Diagnostics.AddError("Invalid import ID", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("metadata").AtName("project"), project)...)
	if workspace != "" {
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("metadata").AtName("workspace"), workspace)...)
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("metadata").AtName("name"), name)...)
	id := project + "/" + name
	if workspace != "" {
		id = project + "/" + workspace + "/" + name
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), id)...)
}

// ---- converters ---------------------------------------------------------

// ipRuleSortKey / portForwardRuleSortKey / ruleSortKey give a stable
// canonical ordering for FromSDK output so equality tests and generated wire
// payloads are deterministic despite set semantics.

func ipRuleSortKey(r apiv1.IpRule) string {
	return r.SourceCIDR + "\x00" + r.Application + "\x00" + r.Action
}
func portFwdSortKey(r apiv1.PortForwardRule) string {
	return r.SourceCIDR + "\x00" + r.Application + "\x00" + r.ApplicationPort + "\x00" + r.Protocol
}
func ruleSortKey(r apiv1.Rule) string {
	return r.SourceCIDR + "\x00" + r.Application + "\x00" + r.ApplicationPort + "\x00" + r.Protocol + "\x00" + r.Action
}

func ipRulesFromTF(ctx context.Context, s types.Set, diags *diag.Diagnostics) []apiv1.IpRule {
	if s.IsNull() || s.IsUnknown() {
		return nil
	}
	var models []ipRuleModel
	diags.Append(s.ElementsAs(ctx, &models, false)...)
	out := make([]apiv1.IpRule, 0, len(models))
	for _, m := range models {
		out = append(out, apiv1.IpRule{
			SourceCIDR:  stringOr(m.SourceCIDR, ""),
			Application: stringOr(m.Application, ""),
			Action:      stringOr(m.Action, ""),
		})
	}
	sort.Slice(out, func(i, j int) bool { return ipRuleSortKey(out[i]) < ipRuleSortKey(out[j]) })
	return out
}

func portForwardRulesFromTF(ctx context.Context, s types.Set, diags *diag.Diagnostics) []apiv1.PortForwardRule {
	if s.IsNull() || s.IsUnknown() {
		return nil
	}
	var models []portForwardRuleModel
	diags.Append(s.ElementsAs(ctx, &models, false)...)
	out := make([]apiv1.PortForwardRule, 0, len(models))
	for _, m := range models {
		out = append(out, apiv1.PortForwardRule{
			SourceCIDR:      stringOr(m.SourceCIDR, ""),
			Application:     stringOr(m.Application, ""),
			ApplicationPort: stringOr(m.ApplicationPort, ""),
			Protocol:        stringOr(m.Protocol, ""),
		})
	}
	sort.Slice(out, func(i, j int) bool { return portFwdSortKey(out[i]) < portFwdSortKey(out[j]) })
	return out
}

func rulesFromTF(ctx context.Context, s types.Set, diags *diag.Diagnostics) []apiv1.Rule {
	if s.IsNull() || s.IsUnknown() {
		return nil
	}
	var models []ruleModel
	diags.Append(s.ElementsAs(ctx, &models, false)...)
	out := make([]apiv1.Rule, 0, len(models))
	for _, m := range models {
		out = append(out, apiv1.Rule{
			SourceCIDR:      stringOr(m.SourceCIDR, ""),
			Application:     stringOr(m.Application, ""),
			ApplicationPort: stringOr(m.ApplicationPort, ""),
			Protocol:        stringOr(m.Protocol, ""),
			Action:          stringOr(m.Action, ""),
		})
	}
	sort.Slice(out, func(i, j int) bool { return ruleSortKey(out[i]) < ruleSortKey(out[j]) })
	return out
}

func ipRulesToTFSet(rules []apiv1.IpRule, diags *diag.Diagnostics) types.Set {
	if len(rules) == 0 {
		return types.SetNull(ipRuleObjectType)
	}
	sorted := append([]apiv1.IpRule(nil), rules...)
	sort.Slice(sorted, func(i, j int) bool { return ipRuleSortKey(sorted[i]) < ipRuleSortKey(sorted[j]) })
	objs := make([]attr.Value, 0, len(sorted))
	for _, ru := range sorted {
		o, d := types.ObjectValue(ipRuleObjectType.AttrTypes, map[string]attr.Value{
			"source_cidr": nullableString(ru.SourceCIDR),
			"application": nullableString(ru.Application),
			"action":      nullableString(ru.Action),
		})
		diags.Append(d...)
		objs = append(objs, o)
	}
	set, d := types.SetValue(ipRuleObjectType, objs)
	diags.Append(d...)
	return set
}

func portForwardRulesToTFSet(rules []apiv1.PortForwardRule, diags *diag.Diagnostics) types.Set {
	if len(rules) == 0 {
		return types.SetNull(portForwardRuleObjectType)
	}
	sorted := append([]apiv1.PortForwardRule(nil), rules...)
	sort.Slice(sorted, func(i, j int) bool { return portFwdSortKey(sorted[i]) < portFwdSortKey(sorted[j]) })
	objs := make([]attr.Value, 0, len(sorted))
	for _, ru := range sorted {
		o, d := types.ObjectValue(portForwardRuleObjectType.AttrTypes, map[string]attr.Value{
			"source_cidr":      nullableString(ru.SourceCIDR),
			"application":      nullableString(ru.Application),
			"application_port": nullableString(ru.ApplicationPort),
			"protocol":         nullableString(ru.Protocol),
		})
		diags.Append(d...)
		objs = append(objs, o)
	}
	set, d := types.SetValue(portForwardRuleObjectType, objs)
	diags.Append(d...)
	return set
}

func rulesToTFSet(rules []apiv1.Rule, diags *diag.Diagnostics) types.Set {
	if len(rules) == 0 {
		return types.SetNull(ruleObjectType)
	}
	sorted := append([]apiv1.Rule(nil), rules...)
	sort.Slice(sorted, func(i, j int) bool { return ruleSortKey(sorted[i]) < ruleSortKey(sorted[j]) })
	objs := make([]attr.Value, 0, len(sorted))
	for _, ru := range sorted {
		o, d := types.ObjectValue(ruleObjectType.AttrTypes, map[string]attr.Value{
			"source_cidr":      nullableString(ru.SourceCIDR),
			"application":      nullableString(ru.Application),
			"application_port": nullableString(ru.ApplicationPort),
			"protocol":         nullableString(ru.Protocol),
			"action":           nullableString(ru.Action),
		})
		diags.Append(d...)
		objs = append(objs, o)
	}
	set, d := types.SetValue(ruleObjectType, objs)
	diags.Append(d...)
	return set
}

func securityGroupModelToSDK(ctx context.Context, m securityGroupResourceModel, diags *diag.Diagnostics) *apiv1.SecurityGroup {
	spec := apiv1.SecurityGroupSpec{
		SecurityGroup:    resourceRefToSDK(m.Spec.SecurityGroup),
		Type:             stringOr(m.Spec.Type, ""),
		Sharing:          sharingToSDK(ctx, m.Spec.Sharing, diags),
		IPRules:          ipRulesFromTF(ctx, m.Spec.IPRules, diags),
		PortForwardRules: portForwardRulesFromTF(ctx, m.Spec.PortForwardRules, diags),
		Rules:            rulesFromTF(ctx, m.Spec.Rules, diags),
	}
	return &apiv1.SecurityGroup{
		TypeMeta: apiv1.TypeMeta{APIVersion: apiv1.APIVersion, Kind: apiv1.KindSecurityGroup},
		Metadata: metadataToSDK(m.Metadata),
		Spec:     spec,
	}
}

func securityGroupSDKToModel(ctx context.Context, s *apiv1.SecurityGroup, project, workspace string, diags *diag.Diagnostics) securityGroupResourceModel {
	if s == nil {
		return securityGroupResourceModel{}
	}
	if s.Metadata.Project == "" {
		s.Metadata.Project = project
	}
	if s.Metadata.Workspace == "" {
		s.Metadata.Workspace = workspace
	}
	out := securityGroupResourceModel{
		APIVersion: types.StringValue(firstNonEmpty(s.APIVersion, apiv1.APIVersion)),
		Kind:       types.StringValue(firstNonEmpty(s.Kind, apiv1.KindSecurityGroup)),
		Metadata:   metadataFromSDK(s.Metadata),
		Spec: securityGroupSpec{
			SecurityGroup:    resourceRefFromSDK(s.Spec.SecurityGroup),
			Type:             nullableString(s.Spec.Type),
			Sharing:          sharingFromSDK(ctx, s.Spec.Sharing, diags),
			IPRules:          ipRulesToTFSet(s.Spec.IPRules, diags),
			PortForwardRules: portForwardRulesToTFSet(s.Spec.PortForwardRules, diags),
			Rules:            rulesToTFSet(s.Spec.Rules, diags),
		},
		Status: securityGroupStatus{
			Status: nullableString(s.Status.Status),
			Reason: nullableString(s.Status.Reason),
			Action: nullableString(s.Status.Action),
		},
	}
	id := s.Metadata.Project + "/" + s.Metadata.Name
	if s.Metadata.Workspace != "" {
		id = s.Metadata.Project + "/" + s.Metadata.Workspace + "/" + s.Metadata.Name
	}
	out.ID = types.StringValue(id)
	return out
}
