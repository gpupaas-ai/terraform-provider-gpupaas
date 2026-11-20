package brandctl_test

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gpupaas-ai/terraform-provider-gpupaas/internal/brand"
	"github.com/gpupaas-ai/terraform-provider-gpupaas/internal/brandctl"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadKeepsLegalEntriesAndReportsTheRest(t *testing.T) {
	f := filepath.Join(t.TempDir(), "brands.yml")
	write(t, f, "# comment\n- gpupaas\n- acme: registry.acme.example/acme-corp/acme\n- Bad-Name\n- 9lives\n- contoso\n- acme\n")
	entries, skipped, err := brandctl.Load(f)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name)
	}
	if strings.Join(names, ",") != "gpupaas,acme,contoso" {
		t.Fatalf("legal entries %v", names)
	}
	if entries[1].Source != "registry.acme.example/acme-corp/acme" || entries[0].Source != "" {
		t.Fatalf("sources %+v", entries)
	}
	if len(skipped) != 3 { // Bad-Name, 9lives, the duplicate acme
		t.Fatalf("skipped %v", skipped)
	}
}

func TestParsePlatforms(t *testing.T) {
	ps, err := brandctl.ParsePlatforms("linux/amd64, darwin/arm64 windows/amd64")
	if err != nil || len(ps) != 3 || ps[2].String() != "windows_amd64" {
		t.Fatalf("%v %v", ps, err)
	}
	for _, bad := range []string{"", "linux", "linux/", "a/b/c"} {
		if _, err := brandctl.ParsePlatforms(bad); err == nil {
			t.Errorf("%q must be rejected", bad)
		}
	}
}

func TestRenderExamplesRenamesDirectoriesAndContent(t *testing.T) {
	src, dst := filepath.Join(t.TempDir(), "examples"), filepath.Join(t.TempDir(), "out")
	write(t, filepath.Join(src, "resources", "gpupaas_project", "resource.tf"), `resource "gpupaas_project" "demo" {}`+"\n")
	write(t, filepath.Join(src, "resources", "gpupaas_project", "import.sh"), "terraform import gpupaas_project.demo demo\n")
	write(t, filepath.Join(src, "provider", "provider.tf"), "terraform {\n  required_providers {\n    gpupaas = { source = \"gpupaas-ai/gpupaas\" }\n  }\n}\nprovider \"gpupaas\" {}\n# GPUPAAS_API_KEY\n")
	b, _ := brand.NewWithSource("acme", "registry.acme.example/acme-corp/acme")
	if err := brandctl.RenderExamples(src, dst, b); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dst, "resources", "acme_project", "resource.tf"))
	if err != nil || !strings.Contains(string(got), `resource "acme_project" "demo"`) {
		t.Fatalf("%s %v", got, err)
	}
	prov, _ := os.ReadFile(filepath.Join(dst, "provider", "provider.tf"))
	for _, want := range []string{`acme = { source = "registry.acme.example/acme-corp/acme" }`, `provider "acme"`, "# ACME_API_KEY"} {
		if !strings.Contains(string(prov), want) {
			t.Errorf("provider.tf lacks %q:\n%s", want, prov)
		}
	}
	if left, err := brandctl.ScanTree(dst, b); err != nil || len(left) != 0 {
		t.Fatalf("leftovers %v %v", left, err)
	}
	if left, _ := brandctl.ScanTree(src, b); len(left) == 0 {
		t.Fatal("the official tree must be reported by the scan")
	}
}

func TestMissingPages(t *testing.T) {
	docs := t.TempDir()
	write(t, filepath.Join(docs, "resources", "project.md"), "x")
	b := brand.MustNew("acme")
	missing := brandctl.MissingPages(docs, b, []string{"acme_project", "acme_workspace"}, []string{"acme_project"})
	if len(missing) != 2 || !strings.HasSuffix(missing[0], "resources/workspace.md") || !strings.HasSuffix(missing[1], "data-sources/project.md") {
		t.Fatalf("%v", missing)
	}
}

func TestPackageIsTheRegistryLayout(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "shared-binary")
	write(t, bin, "binary-bytes")
	b := brand.MustNew("acme")
	for _, p := range []brandctl.Platform{{OS: "linux", Arch: "amd64"}, {OS: "windows", Arch: "amd64"}} {
		if err := brandctl.PackagePlatform(bin, p, b, "1.2.3", dir); err != nil {
			t.Fatal(err)
		}
	}
	if err := brandctl.WriteChecksums(dir, b, "1.2.3"); err == nil {
		t.Fatal("checksums were written before the manifest exists")
	}
	if err := brandctl.WriteManifests(dir, b, "1.2.3"); err != nil {
		t.Fatal(err)
	}
	if err := brandctl.WriteChecksums(dir, b, "1.2.3"); err != nil {
		t.Fatal(err)
	}

	zr, err := zip.OpenReader(filepath.Join(dir, "terraform-provider-acme_1.2.3_windows_amd64.zip"))
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	if len(zr.File) != 1 || zr.File[0].Name != "terraform-provider-acme_v1.2.3.exe" || zr.File[0].Mode()&0o111 == 0 {
		t.Fatalf("zip content %v", zr.File[0].Name)
	}
	rc, _ := zr.File[0].Open()
	body, _ := io.ReadAll(rc)
	rc.Close()
	if string(body) != "binary-bytes" {
		t.Fatalf("binary bytes changed: %q", body)
	}

	sums, _ := os.ReadFile(filepath.Join(dir, "terraform-provider-acme_1.2.3_SHA256SUMS"))
	zraw, _ := os.ReadFile(filepath.Join(dir, "terraform-provider-acme_1.2.3_linux_amd64.zip"))
	h := sha256.Sum256(zraw)
	if !strings.Contains(string(sums), hex.EncodeToString(h[:])+"  terraform-provider-acme_1.2.3_linux_amd64.zip\n") {
		t.Fatalf("SHA256SUMS:\n%s", sums)
	}
	mraw, _ := os.ReadFile(filepath.Join(dir, "terraform-provider-acme_1.2.3_manifest.json"))
	mh := sha256.Sum256(mraw)
	if !strings.Contains(string(sums), hex.EncodeToString(mh[:])+"  terraform-provider-acme_1.2.3_manifest.json\n") {
		t.Fatalf("SHA256SUMS does not list the manifest:\n%s", sums)
	}
	if n := strings.Count(string(sums), "\n"); n != 3 {
		t.Fatalf("SHA256SUMS has %d lines, want 2 zips + the manifest:\n%s", n, sums)
	}
	for _, f := range []string{"terraform-provider-acme_1.2.3_manifest.json", "terraform-registry-manifest.json"} {
		m, err := os.ReadFile(filepath.Join(dir, f))
		if err != nil || !strings.Contains(string(m), `"protocol_versions": ["6.0"]`) {
			t.Fatalf("%s: %s %v", f, m, err)
		}
	}
}

func TestPublishingGuideCoversEveryRegistryRoute(t *testing.T) {
	b, err := brand.NewWithSource("acme", "acme-corp/acme")
	if err != nil {
		t.Fatal(err)
	}
	got := brandctl.Publishing(b, "1.2.3")
	for _, want := range []string{
		"Source address: ",
		"## Any registry that serves plugin files",
		"terraform-provider-acme_1.2.3_docs.zip",
		"## What brandctl did, and what you do",
		"neither signs nor uploads",
		"do not add\n--armor",
		"## A registry that reads a repository",
		"## A private registry (HCP Terraform or Terraform Enterprise)",
		`"namespace":"<org>","registry-name":"private"`,
		`"protocols":["6.0"]`,
		"registry-providers/private/<org>/acme/versions/1.2.3/platforms",
		"does not support provider documentation",
		"## The public Terraform Registry (registry.terraform.io)",
		"- acme: <github-org>/acme",
		"terraform-provider-acme_1.2.3_SHA256SUMS.sig",
		"terraform-provider-acme_1.2.3_manifest.json",
		"Publish > Provider",
		"tag v1.2.3",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("PUBLISHING.md lacks %q", want)
		}
	}
	if strings.Contains(got, "This registry does not serve documentation") {
		t.Error("the guide still claims the partner's registry cannot serve documentation")
	}
	for _, leak := range []string{"gpupaas", "GPUPAAS"} {
		if strings.Contains(got, leak) {
			t.Errorf("PUBLISHING.md names the official brand %q", leak)
		}
	}
}
