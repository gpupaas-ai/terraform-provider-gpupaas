package provider_test

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	gpupaas "github.com/gpupaas-ai/gpupaas-go"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/gpupaas-ai/terraform-provider-gpupaas/internal/brand"
	pkg "github.com/gpupaas-ai/terraform-provider-gpupaas/internal/provider"
)

var brandEnv = []string{"ENDPOINT", "API_KEY", "TOKEN", "USER_AGENT", "ORGANIZATION_ID", "INSECURE", "REQUEST_TIMEOUT"}

func clearBrandEnv(t *testing.T) {
	for _, p := range []string{"GPUPAAS_", "ACME_", "CONTOSO_"} {
		for _, s := range brandEnv {
			t.Setenv(p+s, "")
		}
	}
}

// configure runs Configure of a provider built for b with the given provider-block attributes and returns what the
// SDK client was configured with, or the diagnostics text when configuring failed.
func configure(t *testing.T, b brand.Brand, attrs map[string]string) (gpupaas.Config, string) {
	t.Helper()
	var got gpupaas.Config
	p := pkg.NewCapturing("9.9.9", b, func(c gpupaas.Config) { got = c })
	ctx := context.Background()
	var sch provider.SchemaResponse
	p.Schema(ctx, provider.SchemaRequest{}, &sch)
	typ := sch.Schema.Type().TerraformType(ctx).(tftypes.Object)
	vals := map[string]tftypes.Value{}
	for name, at := range typ.AttributeTypes {
		if v, ok := attrs[name]; ok {
			if at.Is(tftypes.Bool) {
				vals[name] = tftypes.NewValue(tftypes.Bool, v == "true")
			} else {
				vals[name] = tftypes.NewValue(tftypes.String, v)
			}
		} else {
			vals[name] = tftypes.NewValue(at, nil)
		}
	}
	req := provider.ConfigureRequest{Config: tfsdk.Config{Schema: sch.Schema, Raw: tftypes.NewValue(typ, vals)}}
	var resp provider.ConfigureResponse
	p.Configure(ctx, req, &resp)
	var msgs []string
	for _, d := range resp.Diagnostics.Errors() {
		msgs = append(msgs, d.Summary()+": "+d.Detail())
	}
	return got, strings.Join(msgs, "\n")
}

// catalog returns the resource and data source kinds a brand registers (the type name without the brand prefix),
// and fails the test for a type that does not carry the brand.
func catalog(t *testing.T, name string) (resources, dataSources []string) {
	t.Helper()
	b := brand.MustNew(name)
	p := pkg.NewBranded("test", b)()
	var mr provider.MetadataResponse
	p.Metadata(context.Background(), provider.MetadataRequest{}, &mr)
	if mr.TypeName != name {
		t.Fatalf("%s: provider type name %q", name, mr.TypeName)
	}
	for _, f := range p.Resources(context.Background()) {
		var r resource.MetadataResponse
		f().Metadata(context.Background(), resource.MetadataRequest{ProviderTypeName: mr.TypeName}, &r)
		if !strings.HasPrefix(r.TypeName, name+"_") {
			t.Errorf("%s: resource %q", name, r.TypeName)
		}
		resources = append(resources, strings.TrimPrefix(r.TypeName, name+"_"))
	}
	for _, f := range p.DataSources(context.Background()) {
		var r datasource.MetadataResponse
		f().Metadata(context.Background(), datasource.MetadataRequest{ProviderTypeName: mr.TypeName}, &r)
		if !strings.HasPrefix(r.TypeName, name+"_") {
			t.Errorf("%s: data source %q", name, r.TypeName)
		}
		dataSources = append(dataSources, strings.TrimPrefix(r.TypeName, name+"_"))
	}
	sort.Strings(resources)
	sort.Strings(dataSources)
	return resources, dataSources
}

// Every brand registers exactly the kinds the official provider does: one engine, one catalog.
func TestBrandedTypeNamesOfEveryResourceAndDataSource(t *testing.T) {
	wantRes, wantData := catalog(t, brand.Official)
	if len(wantRes) == 0 || len(wantData) == 0 {
		t.Fatalf("the official provider registers %d resources and %d data sources", len(wantRes), len(wantData))
	}
	for _, name := range []string{"acme", "contoso"} {
		gotRes, gotData := catalog(t, name)
		if !reflect.DeepEqual(gotRes, wantRes) {
			t.Errorf("%s resources %v, want the official %v", name, gotRes, wantRes)
		}
		if !reflect.DeepEqual(gotData, wantData) {
			t.Errorf("%s data sources %v, want the official %v", name, gotData, wantData)
		}
	}
}

func TestBrandReadsOnlyItsOwnEnvironment(t *testing.T) {
	clearBrandEnv(t)
	for _, s := range brandEnv {
		t.Setenv("GPUPAAS_"+s, "official-"+strings.ToLower(s))
	}
	t.Setenv("GPUPAAS_REQUEST_TIMEOUT", "50s")
	t.Setenv("GPUPAAS_INSECURE", "false")
	t.Setenv("ACME_ENDPOINT", "https://acme.example")
	t.Setenv("ACME_API_KEY", "acme-key")
	t.Setenv("ACME_TOKEN", "acme-token")
	t.Setenv("ACME_ORGANIZATION_ID", "acme-org")
	t.Setenv("ACME_INSECURE", "true")
	t.Setenv("ACME_REQUEST_TIMEOUT", "45s")

	cfg, errs := configure(t, brand.MustNew("acme"), nil)
	if errs != "" {
		t.Fatal(errs)
	}
	if cfg.Endpoint != "https://acme.example" || cfg.APIKey != "acme-key" || cfg.OrganizationID != "acme-org" {
		t.Fatalf("acme read %q %q %q", cfg.Endpoint, cfg.APIKey, cfg.OrganizationID)
	}
	if cfg.UserAgent != "terraform-provider-acme/9.9.9" {
		t.Fatalf("default user-agent %q", cfg.UserAgent)
	}
	if cfg.HTTPClient == nil || cfg.HTTPClient.Timeout.String() != "45s" || cfg.HTTPClient.Transport == nil {
		t.Fatalf("ACME_REQUEST_TIMEOUT / ACME_INSECURE not applied: %+v", cfg.HTTPClient)
	}
	// The official brand still reads its own variables.
	cfg, errs = configure(t, brand.MustNew("gpupaas"), nil)
	if errs != "" || cfg.Endpoint != "official-endpoint" || cfg.APIKey != "official-api_key" || cfg.OrganizationID != "official-organization_id" ||
		cfg.UserAgent != "official-user_agent" || cfg.HTTPClient.Transport != nil || cfg.HTTPClient.Timeout.String() != "50s" {
		t.Fatalf("gpupaas read %+v (%s)", cfg, errs)
	}
}

func TestBrandedDefaultsAndPrecedence(t *testing.T) {
	clearBrandEnv(t)
	t.Setenv("GPUPAAS_API_KEY", "must-not-be-used")
	t.Setenv("ACME_API_KEY", "acme-key")
	t.Setenv("ACME_TOKEN", "acme-token")
	b := brand.MustNew("acme")

	cfg, errs := configure(t, b, nil)
	if errs != "" || cfg.APIKey != "acme-key" {
		t.Fatalf("API_KEY must be preferred over TOKEN: %q %s", cfg.APIKey, errs)
	}
	if cfg.Endpoint != "https://console.gpupaas.ai" || cfg.HTTPClient == nil || cfg.HTTPClient.Transport != nil {
		t.Fatalf("defaults: endpoint %q, http client %+v (insecure must default to false)", cfg.Endpoint, cfg.HTTPClient)
	}
	cfg, _ = configure(t, b, map[string]string{"token": "attr-token", "user_agent": "custom/1", "organization_id": "attr-org"})
	if cfg.APIKey != "attr-token" || cfg.UserAgent != "custom/1" || cfg.OrganizationID != "attr-org" {
		t.Fatalf("an explicit attribute must win: %+v", cfg)
	}
}

func TestMissingTokenNamesTheBrandsVariables(t *testing.T) {
	clearBrandEnv(t)
	t.Setenv("GPUPAAS_API_KEY", "official-key") // set, but the acme brand must not fall back to it
	_, errs := configure(t, brand.MustNew("acme"), nil)
	if !strings.Contains(errs, "ACME_API_KEY") || !strings.Contains(errs, "ACME_TOKEN") || strings.Contains(strings.ToUpper(errs), "GPUPAAS_") {
		t.Fatalf("diagnostic %q", errs)
	}
}

// protocolServer builds the server a binary of this brand serves.
func protocolServer(t *testing.T, b brand.Brand) tfprotov6.ProviderServer {
	t.Helper()
	return brand.Wrap(providerserver.NewProtocol6(pkg.NewBranded("test", b)())(), b)
}

func TestBrandedSchemasMentionNoOfficialIdentity(t *testing.T) {
	ctx := context.Background()
	for _, name := range []string{"acme", "contoso"} {
		resp, err := protocolServer(t, brand.MustNew(name)).GetProviderSchema(ctx, &tfprotov6.GetProviderSchemaRequest{})
		if err != nil {
			t.Fatal(err)
		}
		var bad []string
		var walk func(where string, b *tfprotov6.SchemaBlock)
		walkAttr := func(where string, a *tfprotov6.SchemaAttribute) {}
		var attr func(where string, a *tfprotov6.SchemaAttribute)
		attr = func(where string, a *tfprotov6.SchemaAttribute) {
			if strings.Contains(a.Description, "gpupaas_") || strings.Contains(a.Description, "GPUPAAS_") {
				bad = append(bad, where+"."+a.Name)
			}
			if a.NestedType != nil {
				for _, n := range a.NestedType.Attributes {
					attr(where+"."+a.Name, n)
				}
			}
		}
		_ = walkAttr
		walk = func(where string, b *tfprotov6.SchemaBlock) {
			if strings.Contains(b.Description, "gpupaas_") || strings.Contains(b.Description, "GPUPAAS_") {
				bad = append(bad, where)
			}
			for _, a := range b.Attributes {
				attr(where, a)
			}
			for _, nb := range b.BlockTypes {
				walk(where+"."+nb.TypeName, nb.Block)
			}
		}
		walk("provider", resp.Provider.Block)
		for k, s := range resp.ResourceSchemas {
			if !strings.HasPrefix(k, name+"_") {
				t.Errorf("%s: resource type %q", name, k)
			}
			walk(k, s.Block)
		}
		for k, s := range resp.DataSourceSchemas {
			if !strings.HasPrefix(k, name+"_") {
				t.Errorf("%s: data source type %q", name, k)
			}
			walk(k, s.Block)
		}
		if len(bad) > 0 {
			t.Errorf("%s: %d schema descriptions still name the official identity, first: %v", name, len(bad), bad[:min(len(bad), 8)])
		}
	}
}

// shape describes a schema block without its text: attribute names, types and flags, nested objects and blocks.
func shape(b *tfprotov6.SchemaBlock) string {
	var attrs []string
	var attr func(a *tfprotov6.SchemaAttribute) string
	attr = func(a *tfprotov6.SchemaAttribute) string {
		s := fmt.Sprintf("%s:req=%t,opt=%t,comp=%t,sens=%t,dep=%t", a.Name, a.Required, a.Optional, a.Computed, a.Sensitive, a.Deprecated)
		if a.Type != nil {
			s += ",type=" + a.Type.String()
		}
		if a.NestedType != nil {
			var inner []string
			for _, n := range a.NestedType.Attributes {
				inner = append(inner, attr(n))
			}
			sort.Strings(inner)
			s += fmt.Sprintf(",nested(%d)[%s]", a.NestedType.Nesting, strings.Join(inner, ";"))
		}
		return s
	}
	for _, a := range b.Attributes {
		attrs = append(attrs, attr(a))
	}
	for _, nb := range b.BlockTypes {
		attrs = append(attrs, fmt.Sprintf("block %s(%d,%d..%d){%s}", nb.TypeName, nb.Nesting, nb.MinItems, nb.MaxItems, shape(nb.Block)))
	}
	sort.Strings(attrs)
	return strings.Join(attrs, "\n")
}

// brandShapes returns the structure of the provider block and of every resource and data source of a brand,
// keyed by the kind without the brand prefix.
func brandShapes(t *testing.T, name string) map[string]string {
	t.Helper()
	resp, err := protocolServer(t, brand.MustNew(name)).GetProviderSchema(context.Background(), &tfprotov6.GetProviderSchemaRequest{})
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{"provider": shape(resp.Provider.Block)}
	for k, s := range resp.ResourceSchemas {
		out["resource "+strings.TrimPrefix(k, name+"_")] = shape(s.Block)
	}
	for k, s := range resp.DataSourceSchemas {
		out["data "+strings.TrimPrefix(k, name+"_")] = shape(s.Block)
	}
	return out
}

// One engine, one schema: every brand registers the same attributes, types and flags as the official provider;
// only the names and the text differ.
func TestBrandedSchemasAreStructurallyIdentical(t *testing.T) {
	want := brandShapes(t, brand.Official)
	if len(want) < 2 {
		t.Fatalf("the official provider has %d schemas", len(want))
	}
	for _, name := range []string{"acme", "contoso"} {
		got := brandShapes(t, name)
		for k, w := range want {
			g, ok := got[k]
			if !ok {
				t.Errorf("%s has no %s", name, k)
			} else if g != w {
				t.Errorf("%s: the schema of %s differs from the official one", name, k)
			}
		}
		for k := range got {
			if _, ok := want[k]; !ok {
				t.Errorf("%s has %s, which the official provider does not", name, k)
			}
		}
	}
}
