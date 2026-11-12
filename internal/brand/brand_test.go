package brand_test

import (
	"strings"
	"testing"

	"github.com/gpupaas-ai/terraform-provider-gpupaas/internal/brand"
)

func TestNewDerivesTheIdentity(t *testing.T) {
	b, err := brand.New("acme")
	if err != nil {
		t.Fatal(err)
	}
	if b.Type != "acme" || b.Registry != "registry.terraform.io/gpupaas-ai/acme" || b.UserAgent != "terraform-provider-acme" ||
		b.EnvPrefix != "ACME_" || b.Endpoint != "https://console.gpupaas.ai" {
		t.Fatalf("got %+v", b)
	}
	if b.Env("API_KEY") != "ACME_API_KEY" || b.Addr("project") != "acme_project" {
		t.Fatalf("Env/Addr: %s %s", b.Env("API_KEY"), b.Addr("project"))
	}
}

func TestNewRejectsIllegalNames(t *testing.T) {
	for _, n := range []string{"", " ", "Acme", "1acme", "ac-me", "acme!", "_acme", "ac me"} {
		if _, err := brand.New(n); err == nil {
			t.Errorf("%q must be rejected", n)
		}
	}
	for _, n := range []string{"gpupaas", "acme", "contoso", "a1", "my_partner"} {
		if _, err := brand.New(n); err != nil {
			t.Errorf("%q must be legal: %v", n, err)
		}
	}
}

func TestRewriteOnlyTouchesTheIdentity(t *testing.T) {
	b := brand.MustNew("acme")
	in := "Use gpupaas_project with GPUPAAS_API_KEY on the GPU PaaS platform (gpupaas-ai/gpupaas, gpupaas-go)."
	got := b.Rewrite(in)
	want := "Use acme_project with ACME_API_KEY on the GPU PaaS platform (gpupaas-ai/gpupaas, gpupaas-go)."
	if got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
	if brand.MustNew("gpupaas").Rewrite(in) != in {
		t.Fatal("the official brand must not change any text")
	}
	if strings.Contains(brand.MustNew("my_partner").Rewrite("gpupaas_x GPUPAAS_Y"), "gpupaas") {
		t.Fatal("underscored official identity must be replaced for any partner")
	}
}

func TestSourceDefaultsAndPartnerRegistries(t *testing.T) {
	b := brand.MustNew("acme")
	if b.Source != "gpupaas-ai/acme" || b.Registry != "registry.terraform.io/gpupaas-ai/acme" {
		t.Fatalf("default source: %+v", b)
	}
	b, err := brand.NewWithSource("acme", "registry.acme.example/acme-corp/acme")
	if err != nil || b.Source != "registry.acme.example/acme-corp/acme" || b.Registry != "registry.acme.example/acme-corp/acme" {
		t.Fatalf("host source: %+v %v", b, err)
	}
	b, err = brand.NewWithSource("acme", "acme-corp/acme")
	if err != nil || b.Registry != "registry.terraform.io/acme-corp/acme" {
		t.Fatalf("namespace source: %+v %v", b, err)
	}
}

func TestSourceMustEndWithTheTypeName(t *testing.T) {
	for _, src := range []string{"acme", "a/b/c/acme", "acme-corp/other", "Acme/acme", "host/ns/acme/", "/acme"} {
		if _, err := brand.NewWithSource("acme", src); err == nil {
			t.Errorf("source %q must be rejected", src)
		}
	}
}

func TestNameFromFile(t *testing.T) {
	cases := map[string]struct {
		name string
		ok   bool
	}{
		"/x/terraform-provider-acme":              {"acme", true},
		"terraform-provider-acme_v0.1.0":          {"acme", true},
		"C:/p/terraform-provider-acme_v0.1.0.exe": {"acme", true},
		"terraform-provider-acme.exe":             {"acme", true},
		"terraform-provider-my_vendor_v1.2.3":     {"my_vendor", true},
		"terraform-provider-my_v2_v0.1.0":         {"my_v2", true},
		"terraform-provider-acme_v1.0.0-beta1":    {"acme", true},
		"terraform-provider-gpupaas":              {"gpupaas", true},
		"provider.test":                           {"", false},
		"main":                                    {"", false},
		"/tmp/go-build123/b001/exe/something":     {"", false},
		"terraform-provider-Acme":                 {"Acme", true}, // found, then rejected by New
	}
	for in, want := range cases {
		got, ok := brand.NameFromFile(in)
		if got != want.name || ok != want.ok {
			t.Errorf("%q: got (%q, %v), want (%q, %v)", in, got, ok, want.name, want.ok)
		}
	}
}

func TestResolve(t *testing.T) {
	if b, err := brand.Resolve("/p/terraform-provider-acme_v0.1.0"); err != nil || b.Type != "acme" || b.EnvPrefix != "ACME_" {
		t.Fatalf("file name brand: %+v %v", b, err)
	}
	if b, err := brand.Resolve("/tmp/provider.test"); err != nil || b.Type != "gpupaas" {
		t.Fatalf("a test binary is the official provider: %+v %v", b, err)
	}
	if _, err := brand.Resolve("/p/terraform-provider-Acme"); err == nil {
		t.Fatal("an illegal brand name in the file name must fail loudly")
	}
}

func TestRewriteExample(t *testing.T) {
	b, _ := brand.NewWithSource("acme", "registry.acme.example/acme-corp/acme")
	in := `terraform {
  required_providers {
    gpupaas = { source = "gpupaas-ai/gpupaas" }
  }
}
provider "gpupaas" { token = var.gpupaas_token }
resource "gpupaas_project" "demo" {}
# set GPUPAAS_API_KEY, see https://console.gpupaas.ai and apiVersion: gpupaas.ai/v1alpha1; GPU PaaS, gpupaas-go
`
	got := b.RewriteExample(in)
	for _, want := range []string{`acme = { source = "registry.acme.example/acme-corp/acme" }`, `provider "acme" { token = var.acme_token }`,
		`resource "acme_project"`, "ACME_API_KEY", "https://console.gpupaas.ai", "gpupaas.ai/v1alpha1", "GPU PaaS, gpupaas-go"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
	if left := b.Leftovers(got); len(left) != 0 {
		t.Errorf("leftovers after rewrite: %v", left)
	}
	if len(b.Leftovers(in)) == 0 {
		t.Error("the official text must be reported as leftovers")
	}
	if two := b.RewriteExample("gpupaas gpupaas gpupaas"); two != "acme acme acme" {
		t.Errorf("adjacent words: %q", two)
	}
}

func TestSourceFromInstallPath(t *testing.T) {
	ok := map[string]string{
		"/w/.terraform/providers/registry.acme.example/acme-corp/acme/0.1.0/darwin_arm64/terraform-provider-acme_v0.1.0":        "registry.acme.example/acme-corp/acme",
		"/c/plugins/registry.terraform.io/acme-corp/acme/1.2.3/linux_amd64/terraform-provider-acme_v1.2.3":                      "registry.terraform.io/acme-corp/acme",
		`C:\w\.terraform\providers\registry.acme.example\acme-corp\acme\0.1.0\windows_amd64\terraform-provider-acme_v0.1.0.exe`: "registry.acme.example/acme-corp/acme",
	}
	for path, want := range ok {
		if got, found := brand.SourceFromInstallPath(path, "acme"); !found || got != want {
			t.Errorf("%s: got (%q, %v), want %q", path, got, found, want)
		}
	}
	for _, path := range []string{
		"/dev/override/terraform-provider-acme",                                                     // development override directory
		"/w/registry.acme.example/acme-corp/other/0.1.0/linux_amd64/terraform-provider-acme_v0.1.0", // type directory differs
		"/w/h/ns/acme/latest/linux_amd64/terraform-provider-acme",                                   // not a version
		"/w/h/ns/acme/0.1.0/linux/terraform-provider-acme",                                          // not os_arch
	} {
		if got, found := brand.SourceFromInstallPath(path, "acme"); found {
			t.Errorf("%s must not carry a source, got %q", path, got)
		}
	}
	b, err := brand.Resolve("/w/.terraform/providers/registry.acme.example/acme-corp/acme/0.1.0/darwin_arm64/terraform-provider-acme_v0.1.0")
	if err != nil || b.Registry != "registry.acme.example/acme-corp/acme" || b.Source != b.Registry {
		t.Fatalf("Resolve should take the partner address from the install path: %+v %v", b, err)
	}
}
