// Package provider implements the GPU PaaS Terraform provider using the
// Terraform Plugin Framework. It wraps the github.com/gpupaas-ai/gpupaas-go
// SDK and exposes a Kubernetes-style declarative resource model.
package provider

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	gpupaas "github.com/gpupaas-ai/gpupaas-go"
	"github.com/gpupaas-ai/gpupaas-go/clientset"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	"github.com/gpupaas-ai/terraform-provider-gpupaas/internal/brand"
	"github.com/gpupaas-ai/terraform-provider-gpupaas/internal/client"
	"github.com/gpupaas-ai/terraform-provider-gpupaas/internal/provider/resources"
)

// GPUProvider is the root Plugin Framework provider for GPU PaaS.
type GPUProvider struct {
	version string
	// brand is the identity this build reports: type name, environment prefix, user-agent, default endpoint.
	brand brand.Brand
	// clientsetFactory lets unit tests inject a fake clientset.
	clientsetFactory func(cfg gpupaas.Config) (clientset.Interface, error)
}

// providerModel matches the provider configuration block in HCL.
type providerModel struct {
	Endpoint  types.String `tfsdk:"endpoint"`
	Token     types.String `tfsdk:"token"`
	UserAgent types.String `tfsdk:"user_agent"`
	Insecure  types.Bool   `tfsdk:"insecure"`
	// OrganizationID is sent as X-Organization-ID when set; partner-level API keys need it, organization-level keys do not.
	OrganizationID types.String `tfsdk:"organization_id"`
	// RequestTimeout limits one API request, not a whole operation.
	RequestTimeout types.String `tfsdk:"request_timeout"`
}

// Environment variable suffixes; the brand adds its prefix (GPUPAAS_, ACME_, ...).
const (
	envEndpoint  = "ENDPOINT"
	envToken     = "TOKEN"
	envAPIKey    = "API_KEY"
	envUserAgent = "USER_AGENT"
	envInsecure  = "INSECURE"
	envOrgID     = "ORGANIZATION_ID"

	envRequestTimeout     = "REQUEST_TIMEOUT"
	defaultRequestTimeout = 30 * time.Second
)

// buildHTTPClient returns the HTTP client for the provider settings, or nil to keep the SDK's default
// (30 seconds per request, TLS verified).
func buildHTTPClient(insecure bool, timeout time.Duration) *http.Client {
	switch {
	case insecure:
		return &http.Client{
			Timeout:   timeout,
			Transport: &http.Transport{Proxy: http.ProxyFromEnvironment, TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}, //nolint:gosec // opt-in via `insecure`
		}
	case timeout != defaultRequestTimeout:
		return &http.Client{Timeout: timeout}
	}
	return nil
}

// parseRequestTimeout reads the request_timeout setting: empty means the default, anything else must
// be a positive duration such as "90s" or "2m".
func parseRequestTimeout(v string) (time.Duration, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return defaultRequestTimeout, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("%q is not a duration such as \"90s\" or \"2m\": %w", v, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("%q must be greater than zero", v)
	}
	return d, nil
}

// New constructs the official provider.
func New(version string) func() provider.Provider {
	return NewBranded(version, brand.MustNew(brand.Official))
}

// NewBranded constructs the provider for one brand. One process serves one brand; tests may build several.
func NewBranded(version string, b brand.Brand) func() provider.Provider {
	return func() provider.Provider {
		return &GPUProvider{version: version, brand: b}
	}
}

// NewWithClientset returns a provider factory that injects a custom clientset
// (used by acceptance/unit tests that drive against the fake).
func NewWithClientset(version string, cs clientset.Interface) func() provider.Provider {
	return NewBrandedWithClientset(version, brand.MustNew(brand.Official), cs)
}

// NewBrandedWithClientset is NewWithClientset for one brand.
func NewBrandedWithClientset(version string, b brand.Brand, cs clientset.Interface) func() provider.Provider {
	return func() provider.Provider {
		return &GPUProvider{
			version: version,
			brand:   b,
			clientsetFactory: func(_ gpupaas.Config) (clientset.Interface, error) {
				return cs, nil
			},
		}
	}
}

// Metadata returns the provider type name reported to Terraform.
func (p *GPUProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = p.brand.Type
	resp.Version = p.version
}

// Schema returns the provider-level configuration schema.
func (p *GPUProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Provider for the GPU PaaS platform. Configure the endpoint and credentials (or leave the block empty and set the `GPUPAAS_*` environment variables), then manage virtual machines, bare-metal machines and Kubernetes (MKS) clusters from the platform's SKUs.",
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
			"organization_id": schema.StringAttribute{
				Optional: true,
				MarkdownDescription: "Organization the API key acts in: its id or its exact, case-sensitive name, sent as the `X-Organization-ID` header. " +
					"Leave it unset with an organization-level API key, which is bound to its organization (the platform ignores the value for such a key). " +
					"A partner-level API key is not bound to an organization and must set it; the organization must belong to the key's partner. " +
					"Falls back to `GPUPAAS_ORGANIZATION_ID`.",
			},
			"request_timeout": schema.StringAttribute{
				Optional: true,
				MarkdownDescription: "Time limit for one API request, as a duration such as `90s` or `2m`. Defaults to `30s`. " +
					"Raise it for very large clusters, whose responses take longer. It limits a single request: how long an " +
					"operation may run is set by the `timeouts` block of each resource. Falls back to `GPUPAAS_REQUEST_TIMEOUT`.",
			},
			"insecure": schema.BoolAttribute{
				Optional: true,
				MarkdownDescription: "Skip TLS certificate verification. Intended only for test environments with self-signed certificates. " +
					"Falls back to `GPUPAAS_INSECURE` (`true`/`1`). Defaults to `false`.",
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
			p.brand.Rewrite("The provider cannot create the API client because the endpoint is unknown. Either set it explicitly or via GPUPAAS_ENDPOINT."),
		)
	}
	if config.RequestTimeout.IsUnknown() {
		resp.Diagnostics.AddAttributeError(
			path.Root("request_timeout"),
			"Unknown request_timeout",
			"The provider cannot create the API client because request_timeout is unknown. Set it to a literal duration such as \"90s\".",
		)
	}
	// An unknown value read as unset would fall back to the environment or the default without a word: a partner key
	// would send no organization, a self-signed endpoint would be verified. Say so instead.
	for _, u := range []struct {
		name    string
		unknown bool
	}{
		{"organization_id", config.OrganizationID.IsUnknown()},
		{"insecure", config.Insecure.IsUnknown()},
		{"user_agent", config.UserAgent.IsUnknown()},
	} {
		if u.unknown {
			resp.Diagnostics.AddAttributeError(
				path.Root(u.name),
				"Unknown "+u.name,
				"The provider cannot create the API client because "+u.name+" is unknown (it comes from a value that is only known after apply). Set it to a literal or use the environment variable.",
			)
		}
	}
	if config.Token.IsUnknown() {
		resp.Diagnostics.AddAttributeError(
			path.Root("token"),
			"Unknown token",
			p.brand.Rewrite("The provider cannot create the API client because the token is unknown. Either set it explicitly or via GPUPAAS_API_KEY/GPUPAAS_TOKEN."),
		)
	}
	if resp.Diagnostics.HasError() {
		return
	}

	b := p.brand
	endpoint := stringOrEnv(config.Endpoint, b.Env(envEndpoint), b.Endpoint)
	token := firstNonEmpty(stringOrEmpty(config.Token), os.Getenv(b.Env(envAPIKey)), os.Getenv(b.Env(envToken)))
	userAgent := stringOrEnv(config.UserAgent, b.Env(envUserAgent), b.UserAgent+"/"+p.version)

	if token == "" {
		resp.Diagnostics.AddAttributeError(
			path.Root("token"),
			"Missing API token",
			b.Rewrite("Set the `token` attribute or one of the GPUPAAS_API_KEY / GPUPAAS_TOKEN environment variables."),
		)
		return
	}

	cfg := gpupaas.NewConfig(endpoint, token)
	cfg.UserAgent = userAgent
	cfg.OrganizationID = stringOrEnv(config.OrganizationID, b.Env(envOrgID), "")

	requestTimeout, err := parseRequestTimeout(stringOrEnv(config.RequestTimeout, b.Env(envRequestTimeout), ""))
	if err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("request_timeout"), "Invalid request_timeout", err.Error())
		return
	}
	insecure := boolOrEnv(config.Insecure, b.Env(envInsecure))
	if client := buildHTTPClient(insecure, requestTimeout); client != nil {
		cfg.HTTPClient = client
	}
	if insecure {
		resp.Diagnostics.AddWarning(
			"TLS verification disabled",
			"The provider `insecure` setting is enabled; server certificates are not verified. Use only for test environments.",
		)
	}

	tflog.Debug(ctx, "configuring "+b.Type+" clientset", map[string]any{
		"endpoint":       cfg.Endpoint,
		"user_agent":     cfg.UserAgent,
		"insecure":       insecure,
		"controller_api": gpupaas.ControllerAPI,
	})

	factory := p.clientsetFactory
	if factory == nil {
		factory = func(c gpupaas.Config) (clientset.Interface, error) {
			return clientset.NewForConfig(c)
		}
	}
	cs, err := factory(cfg)
	if err != nil {
		resp.Diagnostics.AddError("Failed to create "+b.Type+" clientset", err.Error())
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
		resources.NewWorkspaceResource,
		resources.NewSshKeyResource,
		resources.NewSecurityGroupResource,
		resources.NewHighSpeedStorageResource,
		resources.NewObjectStorageResource,
		resources.NewMKSClusterResource,
		resources.NewBareMetalResource,
		resources.NewVMResource,
	}
}

// DataSources returns the list of data sources exposed by the provider.
func (p *GPUProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	singular := []func() datasource.DataSource{
		resources.NewWorkspaceDataSource,
		resources.NewSshKeyDataSource,
		resources.NewSecurityGroupDataSource,
		resources.NewHighSpeedStorageDataSource,
		resources.NewObjectStorageDataSource,
		resources.NewMKSClusterDataSource,
		resources.NewMKSClusterProfileDataSource,
		resources.NewMKSKubeconfigDataSource,
		resources.NewMKSVersionsDataSource,
		resources.NewMKSSKUCreateOptionsDataSource,
		resources.NewMKSSKUCapacityDataSource,
		resources.NewMKSAuditEventsDataSource,
		resources.NewMKSClusterStatusDataSource,
		resources.NewBareMetalDataSource,
		resources.NewBareMetalProfileDataSource,
		resources.NewVMDataSource,
		resources.NewVMProfileDataSource,
	}
	return append(singular, resources.ListDataSources()...)
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

func boolOrEnv(v types.Bool, env string) bool {
	if !v.IsNull() && !v.IsUnknown() {
		return v.ValueBool()
	}
	b, err := strconv.ParseBool(strings.TrimSpace(os.Getenv(env)))
	return err == nil && b
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
