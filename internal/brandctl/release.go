package brandctl

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/gpupaas-ai/terraform-provider-gpupaas/internal/brand"
)

// Options controls Release.
type Options struct {
	// RepoDir is the provider repository (it holds brands.yml, docs/, examples/ and main.go).
	RepoDir string
	// Out is the output directory, relative to RepoDir (tfplugindocs resolves its paths against the repository).
	Out string
	// Brands limits the run to these names; empty means every legal entry of brands.yml.
	Brands    []string
	Platforms []Platform
	Version   string
	// Tfplugindocs is the tfplugindocs executable (default: found on PATH).
	Tfplugindocs string
	// AllowLocal builds even when the release gate fails (a local replace of the SDK, uncommitted changes, an
	// untagged SDK). The result is for an engineer's inner loop and is marked local in BUILD-INFO.json.
	AllowLocal bool
	Log        io.Writer
}

func (o Options) logf(format string, a ...any) {
	if o.Log != nil {
		fmt.Fprintf(o.Log, format+"\n", a...)
	}
}

func run(dir string, env []string, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		tail := string(out)
		if len(tail) > 1500 {
			tail = tail[len(tail)-1500:]
		}
		return string(out), fmt.Errorf("%s %s: %w\n%s", name, strings.Join(args, " "), err, tail)
	}
	return string(out), nil
}

// Release builds the shared binary once per platform, then for every brand renders its examples, generates its
// docs, checks them, and packages the result in dist/<type>/ in the layout both kinds of registry take: platform
// zips with SHA256SUMS and manifest for a registry that serves plugin files, and docs/ + examples/ +
// terraform-registry-manifest.json for a registry that reads a repository.
func Release(ctx context.Context, o Options) error {
	entries, skipped, err := Load(filepath.Join(o.RepoDir, "brands.yml"))
	if err != nil {
		return err
	}
	for _, s := range skipped {
		o.logf("skipped: %s", s)
	}
	var todo []Entry
	for _, e := range entries {
		if len(o.Brands) == 0 || contains(o.Brands, e.Name) {
			todo = append(todo, e)
		}
	}
	if len(todo) == 0 {
		return fmt.Errorf("no brand to build")
	}

	// the release gate: one SDK tag, one commit, one release line (see gate.go)
	var all []string
	for _, e := range entries {
		all = append(all, e.Name)
	}
	gate, problems, err := Gate(o.RepoDir, o.Version, all)
	if err != nil {
		return err
	}
	if len(problems) > 0 {
		if !o.AllowLocal {
			return fmt.Errorf("release gate failed, %d problem(s):\n  - %s\n(a local build for your own testing: add -allow-local)",
				len(problems), strings.Join(problems, "\n  - "))
		}
		o.logf("LOCAL BUILD: the release gate would fail, so do not hand these binaries to a partner:\n  - %s", strings.Join(problems, "\n  - "))
		gate.Local = true
	}
	host := Platform{runtime.GOOS, runtime.GOARCH}
	platforms := append([]Platform(nil), o.Platforms...)
	if !hasPlatform(platforms, host) {
		platforms = append(platforms, host) // the host build is needed to check each brand
	}

	// 1. one build per platform, shared by every brand
	build := filepath.Join(o.RepoDir, o.Out, ".build")
	binaries := map[Platform]string{}
	for _, p := range platforms {
		bin := filepath.Join(build, p.String(), "terraform-provider-"+brand.Official)
		if p.OS == "windows" {
			bin += ".exe"
		}
		o.logf("build %s", p)
		if _, err := run(o.RepoDir, []string{"CGO_ENABLED=0", "GOOS=" + p.OS, "GOARCH=" + p.Arch}, "go", "build", "-trimpath",
			"-ldflags", "-X main.version="+o.Version, "-o", bin, "."); err != nil {
			return err
		}
		binaries[p] = bin
	}

	// 2. per brand
	for _, e := range todo {
		b, err := brand.NewWithSource(e.Name, e.Source)
		if err != nil {
			return err
		}
		if err := releaseBrand(ctx, o, b, host, platforms, binaries, gate); err != nil {
			return fmt.Errorf("%s: %w", b.Type, err)
		}
	}
	return nil
}

func releaseBrand(_ context.Context, o Options, b brand.Brand, host Platform, platforms []Platform, binaries map[Platform]string, build Build) error {
	rel := filepath.Join(o.Out, b.Type)
	out := filepath.Join(o.RepoDir, rel)
	if err := os.RemoveAll(out); err != nil {
		return err
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}

	// examples and docs
	docs, examples := filepath.Join(out, "docs"), filepath.Join(out, "examples")
	if b.Type == brand.Official {
		if err := CopyTree(filepath.Join(o.RepoDir, "docs"), docs); err != nil {
			return err
		}
		if err := CopyTree(filepath.Join(o.RepoDir, "examples"), examples); err != nil {
			return err
		}
	} else {
		o.logf("%s: render examples, generate docs", b.Type)
		if err := RenderExamples(filepath.Join(o.RepoDir, "examples"), examples, b); err != nil {
			return err
		}
		tool := o.Tfplugindocs
		if tool == "" {
			tool = "tfplugindocs"
		}
		if _, err := run(o.RepoDir, nil, tool, "generate", "--provider-name", b.Type,
			"--examples-dir", filepath.Join(rel, "examples"), "--rendered-website-dir", filepath.Join(rel, "docs")); err != nil {
			return err
		}
		if left, err := ScanTree(out, b); err != nil {
			return err
		} else if len(left) > 0 {
			return fmt.Errorf("the partner tree still carries the official identity:\n  %s", strings.Join(left, "\n  "))
		}
	}

	// the shared binary identifies as this brand under this brand's file name, and every registered type has a page
	scratch, err := os.MkdirTemp("", "brandctl-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(scratch)
	named := filepath.Join(scratch, PluginFile(b, o.Version, host))
	if err := copyExecutable(binaries[host], named); err != nil {
		return err
	}
	ident, err := run("", nil, named, "-brand")
	if err != nil {
		return err
	}
	got := strings.Fields(ident)
	if len(got) < 1 || got[0] != b.Type {
		return fmt.Errorf("the packaged binary identifies as %q, want %q", strings.TrimSpace(ident), b.Type)
	}
	// the binary must link the SDK of its own line: a provider vX.Y.Z carries the controller API line vX.Y
	if !build.Local && (len(got) < 3 || got[2] != build.ControllerAPI) {
		return fmt.Errorf("the packaged binary targets controller API %q, want %q (the SDK it links is not on the release line)",
			strings.Join(got[min(2, len(got)):], " "), build.ControllerAPI)
	}
	build.ControllerAPI = ""
	if len(got) >= 3 {
		build.ControllerAPI = got[2]
	}
	resources, dataSources, err := SchemaTypes(named, b)
	if err != nil {
		return err
	}
	if missing := MissingPages(docs, b, resources, dataSources); len(missing) > 0 {
		return fmt.Errorf("docs are missing %d pages, first: %s", len(missing), missing[0])
	}
	o.logf("%s: identifies as %s, %d resources + %d data sources, all documented", b.Type, b.Type, len(resources), len(dataSources))

	// package
	for _, p := range platforms {
		if p == host && !hasPlatform(o.Platforms, host) {
			continue // built only to check
		}
		if err := PackagePlatform(binaries[p], p, b, o.Version, out); err != nil {
			return err
		}
	}
	if err := WriteManifests(out, b, o.Version); err != nil {
		return err
	}
	if err := WriteChecksums(out, b, o.Version); err != nil {
		return err
	}
	if err := ZipTrees(filepath.Join(out, fmt.Sprintf("terraform-provider-%s_%s_docs.zip", b.Type, o.Version)),
		map[string]string{"docs": docs, "examples": examples}); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(out, "PUBLISHING.md"), []byte(publishing(b, o.Version)), 0o644); err != nil {
		return err
	}
	info, err := json.MarshalIndent(build, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(out, "BUILD-INFO.json"), append(info, '\n'), 0o644); err != nil {
		return err
	}
	o.logf("%s: packaged %s", b.Type, rel)
	return nil
}

func publishing(b brand.Brand, version string) string {
	return fmt.Sprintf(`# Publishing %[1]s %[2]s

Source address: %[3]s

## What brandctl did, and what you do
brandctl built the platform zips, terraform-provider-%[1]s_%[2]s_SHA256SUMS (it lists every zip and the manifest), the
manifest, docs/ and examples/. It neither signs nor uploads: signing and publishing use your own GPG key and your own
registry account.

Create an RSA or DSA key (the registries reject the default ECC type), keep the private key, and register the public key
with your registry. Sign the checksums once, when this release is final: the signature covers the exact bytes of the file,
so regenerating it, or running brandctl again, invalidates the signature. The signature must be binary, so do not add
--armor:

    gpg --detach-sign terraform-provider-%[1]s_%[2]s_SHA256SUMS

That writes terraform-provider-%[1]s_%[2]s_SHA256SUMS.sig. Then publish the files by one of the routes below.

## Any registry that serves plugin files (Artifactory, a mirror, your own service)
Upload the %[1]s zips, terraform-provider-%[1]s_%[2]s_SHA256SUMS and its signature, and the manifest, under
%[3]s. Terraform reports the provider as "unauthenticated" until the signature is registered with the registry.
Such a registry implements only the provider-registry protocol (zips, checksums, signatures), which has no place for
documentation. Publish terraform-provider-%[1]s_%[2]s_docs.zip on your own portal, or use one of the routes below, where
the registry reads docs/ from a Git repository.

## A registry that reads a repository
Commit docs/, examples/ and terraform-registry-manifest.json to your provider repository, tag v%[2]s, and attach the
zips, the SHA256SUMS file with its signature, and the manifest to the release of that tag.

## A private registry (HCP Terraform or Terraform Enterprise)
The private registry takes the files through its API, one call per step. It does not read a repository, does not pick up
releases by itself, and does not support provider documentation, so publish terraform-provider-%[1]s_%[2]s_docs.zip on
your own portal. <org> is your organization, and the token must belong to the owners team or a team with permission to
manage the private registry. Send every call to https://app.terraform.io/api/v2 or .../api/registry/private/v2 with the
headers "Authorization: Bearer <token>" and "Content-Type: application/vnd.api+json".

1. Create the provider, once: POST /api/v2/organizations/<org>/registry-providers with
   {"data":{"type":"registry-providers","attributes":{"name":"%[1]s","namespace":"<org>","registry-name":"private"}}}
2. Register your public key, once: POST /api/registry/private/v2/gpg-keys with
   {"data":{"type":"gpg-keys","attributes":{"namespace":"<org>","ascii-armor":"<public key, newlines as \n>"}}}
   The answer holds the key-id.
3. Create the version: POST /api/v2/organizations/<org>/registry-providers/private/<org>/%[1]s/versions with
   {"data":{"type":"registry-provider-versions","attributes":{"version":"%[2]s","key-id":"<key-id>","protocols":["6.0"]}}}
   The answer holds the links shasums-upload and shasums-sig-upload. Upload terraform-provider-%[1]s_%[2]s_SHA256SUMS and
   terraform-provider-%[1]s_%[2]s_SHA256SUMS.sig to them with curl -T.
4. For every platform zip: POST /api/v2/organizations/<org>/registry-providers/private/<org>/%[1]s/versions/%[2]s/platforms
   with {"data":{"type":"registry-provider-version-platforms","attributes":{"os":"linux","arch":"amd64","shasum":"<sha256 of
   the zip>","filename":"terraform-provider-%[1]s_%[2]s_linux_amd64.zip"}}}. The answer holds provider-binary-upload; upload
   the zip to it with curl -T. Repeat for each platform.

HashiCorp's guide "Publish private providers" lists the consumer address and credentials.

## The public Terraform Registry (registry.terraform.io)
The public registry renders the documentation page from docs/ in your repository, so this is the route that publishes
documentation there. Its source address is <github-org>/%[1]s, where <github-org> is the GitHub organization you publish
under: list the brand in brands.yml as "- %[1]s: <github-org>/%[1]s" and run the release again, so that the examples and
the manifest carry that address.

1. Create a public GitHub repository named terraform-provider-%[1]s in <github-org>. The repository holds docs/,
   examples/ and terraform-registry-manifest.json from this release; the registry reads it and the release assets, it does
   not build the provider.
2. Create a GPG key pair and keep the private key. Sign in to registry.terraform.io with GitHub, authorize the
   organization, and add the public key (ASCII armor) to the signing keys of that namespace.
3. Sign the checksums as described above, which writes terraform-provider-%[1]s_%[2]s_SHA256SUMS.sig.
4. Commit docs/, examples/ and terraform-registry-manifest.json, and tag v%[2]s (semantic version with a v prefix).
5. Create a GitHub release for that tag with these assets: the platform zips, terraform-provider-%[1]s_%[2]s_SHA256SUMS,
   terraform-provider-%[1]s_%[2]s_SHA256SUMS.sig and terraform-provider-%[1]s_%[2]s_manifest.json.
6. In the registry choose Publish > Provider and pick the organization and repository. It checks the tag, the assets and the
   signature against your key, and publishes every later tagged release by itself.

Everything in that repository and on the registry page is public. HashiCorp's rules for publishing providers can change;
check its "Publishing providers" guide before the first release.
`, b.Type, version, b.Source)
}

// SchemaTypes starts the binary through Terraform and returns the resource and data source types it registers.
func SchemaTypes(binary string, b brand.Brand) (resources, dataSources []string, err error) {
	tmp, err := os.MkdirTemp("", "brandctl-schema-")
	if err != nil {
		return nil, nil, err
	}
	defer os.RemoveAll(tmp)
	plugins := filepath.Join(tmp, "plugin")
	if err := os.MkdirAll(plugins, 0o755); err != nil {
		return nil, nil, err
	}
	if err := copyExecutable(binary, filepath.Join(plugins, "terraform-provider-"+b.Type)); err != nil {
		return nil, nil, err
	}
	rc := fmt.Sprintf("provider_installation {\n  dev_overrides { %q = %q }\n  direct {}\n}\n", b.Registry, plugins)
	if err := os.WriteFile(filepath.Join(tmp, "tfrc"), []byte(rc), 0o644); err != nil {
		return nil, nil, err
	}
	cfg := fmt.Sprintf("terraform {\n  required_providers {\n    %s = { source = %q }\n  }\n}\n", b.Type, b.Registry)
	if err := os.WriteFile(filepath.Join(tmp, "main.tf"), []byte(cfg), 0o644); err != nil {
		return nil, nil, err
	}
	raw, err := run(tmp, []string{"TF_CLI_CONFIG_FILE=" + filepath.Join(tmp, "tfrc")}, "terraform", "providers", "schema", "-json")
	if err != nil {
		return nil, nil, err
	}
	var doc struct {
		Schemas map[string]struct {
			Resources   map[string]json.RawMessage `json:"resource_schemas"`
			DataSources map[string]json.RawMessage `json:"data_source_schemas"`
		} `json:"provider_schemas"`
	}
	if err := json.Unmarshal([]byte(jsonPart(raw)), &doc); err != nil {
		return nil, nil, err
	}
	for _, s := range doc.Schemas {
		for n := range s.Resources {
			resources = append(resources, n)
		}
		for n := range s.DataSources {
			dataSources = append(dataSources, n)
		}
	}
	sort.Strings(resources)
	sort.Strings(dataSources)
	if len(resources)+len(dataSources) == 0 {
		return nil, nil, fmt.Errorf("the binary registers nothing")
	}
	for _, n := range append(append([]string{}, resources...), dataSources...) {
		if !strings.HasPrefix(n, b.Type+"_") {
			return nil, nil, fmt.Errorf("type %q does not carry the brand %q", n, b.Type)
		}
	}
	return resources, dataSources, nil
}

// jsonPart drops the development-override warning Terraform prints before the JSON.
func jsonPart(s string) string {
	if i := strings.Index(s, "{"); i >= 0 {
		return s[i:]
	}
	return s
}

func copyExecutable(src, dst string) error {
	raw, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dst, raw, 0o755)
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func hasPlatform(list []Platform, p Platform) bool {
	for _, x := range list {
		if x == p {
			return true
		}
	}
	return false
}
