// Package provider implements the GPU PaaS Terraform provider using the
// Terraform Plugin Framework. It wraps the github.com/gpupaas-ai/gpupaas-go
// SDK and exposes a Kubernetes-style declarative resource model.
package provider

import (
	"context"
	"os"
	"strings"

	gpupaas "github.com/gpupaas-ai/gpupaas-go"
	"github.com/gpupaas-ai/gpupaas-go/clientset"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	"github.com/gpupaas-ai/terraform-provider-gpupaas/internal/client"
	"github.com/gpupaas-ai/terraform-provider-gpupaas/internal/provider/resources"
)

// GPUProvider is the root Plugin Framework provider for GPU PaaS.
type GPUProvider struct {
	version string
	// clientsetFactory lets unit tests inject a fake clientset.
	clientsetFactory func(cfg gpupaas.Config) (clientset.Interface, error)
}

// providerModel matches the provider configuration block in HCL.
type providerModel struct {
	Endpoint  types.String `tfsdk:"endpoint"`
	Token     types.String `tfsdk:"token"`
	UserAgent types.String `tfsdk:"user_agent"`
}

const (
	envEndpoint  = "GPUPAAS_ENDPOINT"
	envToken     = "GPUPAAS_TOKEN"
	envAPIKey    = "GPUPAAS_API_KEY"
	envUserAgent = "GPUPAAS_USER_AGENT"
)

// New constructs the provider factory used by main.go.
func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &GPUProvider{version: version}
	}
}

// NewWithClientset returns a provider factory that injects a custom clientset
// (used by acceptance/unit tests that drive against the fake).
func NewWithClientset(version string, cs clientset.Interface) func() provider.Provider {
	return func() provider.Provider {
		return &GPUProvider{
			version: version,
			clientsetFactory: func(_ gpupaas.Config) (clientset.Interface, error) {
				return cs, nil
			},
		}
	}
}

// Metadata returns the provider type name reported to Terraform.
func (p *GPUProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "gpupaas"
	resp.Version = p.version
}

// Schema returns the provider-level configuration schema.
func (p *GPUProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Provider for the GPU PaaS platform. Configure endpoint and credentials, then manage projects, workspaces, virtual machines, storage, security groups, SSH keys, and workspace collaborators.",
		Attributes: map[string]schema.Attribute{
			"endpoint": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Base URL of the GPU PaaS API. Defaults to `https://console.gpupaas.ai`. Falls back to `GPUPAAS_ENDPOINT`.",
			},
			"token": schema.StringAttribute{
				Optional:            true,
				Sensitive:           true,
				MarkdownDescription: "API token used for authentication. Falls back to `GPUPAAS_API_KEY`, then `GPUPAAS_TOKEN`.",
			},
			"user_agent": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Custom User-Agent suffix appended to outbound API requests. Falls back to `GPUPAAS_USER_AGENT`.",
			},
		},
	}
}

// Configure wires a clientset based on provider configuration + environment.
func (p *GPUProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var config providerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if config.Endpoint.IsUnknown() {
		resp.Diagnostics.AddAttributeError(
			path.Root("endpoint"),
			"Unknown endpoint",
			"The provider cannot create the API client because the endpoint is unknown. Either set it explicitly or via GPUPAAS_ENDPOINT.",
		)
	}
	if config.Token.IsUnknown() {
		resp.Diagnostics.AddAttributeError(
			path.Root("token"),
			"Unknown token",
			"The provider cannot create the API client because the token is unknown. Either set it explicitly or via GPUPAAS_API_KEY/GPUPAAS_TOKEN.",
		)
	}
	if resp.Diagnostics.HasError() {
		return
	}

	endpoint := stringOrEnv(config.Endpoint, envEndpoint, "https://console.gpupaas.ai")
	token := firstNonEmpty(stringOrEmpty(config.Token), os.Getenv(envAPIKey), os.Getenv(envToken))
	userAgent := stringOrEnv(config.UserAgent, envUserAgent, "terraform-provider-gpupaas/"+p.version)

	if token == "" {
		resp.Diagnostics.AddAttributeError(
			path.Root("token"),
			"Missing API token",
			"Set the `token` attribute or one of the GPUPAAS_API_KEY / GPUPAAS_TOKEN environment variables.",
		)
		return
	}

	cfg := gpupaas.NewConfig(endpoint, token)
	cfg.UserAgent = userAgent

	tflog.Debug(ctx, "configuring gpupaas clientset", map[string]any{
		"endpoint":   cfg.Endpoint,
		"user_agent": cfg.UserAgent,
	})

	factory := p.clientsetFactory
	if factory == nil {
		factory = func(c gpupaas.Config) (clientset.Interface, error) {
			return clientset.NewForConfig(c)
		}
	}
	cs, err := factory(cfg)
	if err != nil {
		resp.Diagnostics.AddError("Failed to create gpupaas clientset", err.Error())
		return
	}

	pd := &client.ProviderData{
		Clientset: cs,
		Endpoint:  cfg.Endpoint,
		UserAgent: cfg.UserAgent,
	}
	resp.DataSourceData = pd
	resp.ResourceData = pd
}

// Resources returns the list of managed resources exposed by the provider.
func (p *GPUProvider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		resources.NewProjectResource,
		resources.NewWorkspaceResource,
		resources.NewWorkspaceCollaboratorResource,
		resources.NewVirtualMachineResource,
		resources.NewStorageResource,
		resources.NewSecurityGroupResource,
		resources.NewSshKeyResource,
		resources.NewBaremetalMachineResource,
		resources.NewMKSClusterResource,
	}
}

// DataSources returns the list of data sources exposed by the provider.
func (p *GPUProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		resources.NewProjectDataSource,
		resources.NewWorkspaceDataSource,
		resources.NewVirtualMachineDataSource,
		resources.NewVirtualMachineStatusDataSource,
		resources.NewBaremetalMachineDataSource,
		resources.NewBaremetalMachineStatusDataSource,
		resources.NewMKSClusterDataSource,
	}
}

// ---- helpers --------------------------------------------------------------

func stringOrEnv(v types.String, env, fallback string) string {
	if !v.IsNull() && !v.IsUnknown() {
		val := strings.TrimSpace(v.ValueString())
		if val != "" {
			return val
		}
	}
	if envVal := strings.TrimSpace(os.Getenv(env)); envVal != "" {
		return envVal
	}
	return fallback
}

func stringOrEmpty(v types.String) string {
	if v.IsNull() || v.IsUnknown() {
		return ""
	}
	return strings.TrimSpace(v.ValueString())
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
