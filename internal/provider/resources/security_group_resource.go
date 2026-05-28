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
	SecurityGroup    resourceRefModel       `tfsdk:"security_group"`
	Type             types.String           `tfsdk:"type"`
	IPRules          []ipRuleModel          `tfsdk:"ip_rules"`
	PortForwardRules []portForwardRuleModel `tfsdk:"port_forward_rules"`
	Rules            []ruleModel            `tfsdk:"rules"`
	Sharing          *SharingModel          `tfsdk:"sharing"`
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

func (r *securityGroupResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_security_group"
}

func (r *securityGroupResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "GPU PaaS security group (`apiVersion: " + apiv1.APIVersion + "`, `kind: SecurityGroup`). Supports project- or workspace-scoped placement via metadata.workspace.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"api_version": schema.StringAttribute{Computed: true},
			"kind":        schema.StringAttribute{Computed: true},
			"metadata":    MetadataResourceAttribute(true, true),
			"spec": schema.SingleNestedAttribute{
				Required: true,
				Attributes: map[string]schema.Attribute{
					"security_group": resourceRefSchema("Security group catalog reference."),
					"type":           schema.StringAttribute{Optional: true},
					"ip_rules": schema.ListNestedAttribute{
						Optional:            true,
						MarkdownDescription: "IP-based rules.",
						NestedObject: schema.NestedAttributeObject{
							Attributes: map[string]schema.Attribute{
								"source_cidr": schema.StringAttribute{Optional: true},
								"application": schema.StringAttribute{Optional: true},
								"action":      schema.StringAttribute{Optional: true},
							},
						},
					},
					"port_forward_rules": schema.ListNestedAttribute{
						Optional:            true,
						MarkdownDescription: "Port forwarding rules.",
						NestedObject: schema.NestedAttributeObject{
							Attributes: map[string]schema.Attribute{
								"source_cidr":      schema.StringAttribute{Optional: true},
								"application":      schema.StringAttribute{Optional: true},
								"application_port": schema.StringAttribute{Optional: true},
								"protocol":         schema.StringAttribute{Optional: true},
							},
						},
					},
					"rules": schema.ListNestedAttribute{
						Optional:            true,
						MarkdownDescription: "General rules.",
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

func securityGroupModelToSDK(ctx context.Context, m securityGroupResourceModel, diags *diag.Diagnostics) *apiv1.SecurityGroup {
	spec := apiv1.SecurityGroupSpec{
		SecurityGroup: resourceRefToSDK(m.Spec.SecurityGroup),
		Type:          stringOr(m.Spec.Type, ""),
		Sharing:       sharingToSDK(ctx, m.Spec.Sharing, diags),
	}
	for _, ip := range m.Spec.IPRules {
		spec.IPRules = append(spec.IPRules, apiv1.IpRule{
			SourceCIDR:  stringOr(ip.SourceCIDR, ""),
			Application: stringOr(ip.Application, ""),
			Action:      stringOr(ip.Action, ""),
		})
	}
	for _, p := range m.Spec.PortForwardRules {
		spec.PortForwardRules = append(spec.PortForwardRules, apiv1.PortForwardRule{
			SourceCIDR:      stringOr(p.SourceCIDR, ""),
			Application:     stringOr(p.Application, ""),
			ApplicationPort: stringOr(p.ApplicationPort, ""),
			Protocol:        stringOr(p.Protocol, ""),
		})
	}
	for _, ru := range m.Spec.Rules {
		spec.Rules = append(spec.Rules, apiv1.Rule{
			SourceCIDR:      stringOr(ru.SourceCIDR, ""),
			Application:     stringOr(ru.Application, ""),
			ApplicationPort: stringOr(ru.ApplicationPort, ""),
			Protocol:        stringOr(ru.Protocol, ""),
			Action:          stringOr(ru.Action, ""),
		})
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
			SecurityGroup: resourceRefFromSDK(s.Spec.SecurityGroup),
			Type:          nullableString(s.Spec.Type),
			Sharing:       sharingFromSDK(ctx, s.Spec.Sharing, diags),
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
	for _, ip := range s.Spec.IPRules {
		out.Spec.IPRules = append(out.Spec.IPRules, ipRuleModel{
			SourceCIDR:  nullableString(ip.SourceCIDR),
			Application: nullableString(ip.Application),
			Action:      nullableString(ip.Action),
		})
	}
	for _, p := range s.Spec.PortForwardRules {
		out.Spec.PortForwardRules = append(out.Spec.PortForwardRules, portForwardRuleModel{
			SourceCIDR:      nullableString(p.SourceCIDR),
			Application:     nullableString(p.Application),
			ApplicationPort: nullableString(p.ApplicationPort),
			Protocol:        nullableString(p.Protocol),
		})
	}
	for _, ru := range s.Spec.Rules {
		out.Spec.Rules = append(out.Spec.Rules, ruleModel{
			SourceCIDR:      nullableString(ru.SourceCIDR),
			Application:     nullableString(ru.Application),
			ApplicationPort: nullableString(ru.ApplicationPort),
			Protocol:        nullableString(ru.Protocol),
			Action:          nullableString(ru.Action),
		})
	}
	return out
}
