// Package brand holds the identity of one provider build: its Terraform type name and everything that is
// derived from it (registry address, user-agent, environment variable prefix, default endpoint).
package brand

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Official is the name of the official provider; every other name is a partner.
const Official = "gpupaas"

// DefaultEndpoint is the API endpoint every brand uses unless the author sets one.
const DefaultEndpoint = "https://console.gpupaas.ai"

// Name pins the brand at build time with -ldflags "-X <module>/internal/brand.Name=<type>". It is normally left
// alone: one binary is built per platform and each brand ships it under its own file name (see Current).
var Name = Official

// Source is the registry address the partner publishes this provider under, `host/namespace/type` or
// `namespace/type` (host registry.terraform.io). Empty means the default `gpupaas-ai/<type>`. The release build
// sets it with -ldflags "-X <module>/internal/brand.Source=<address>".
var Source = ""

var legalName = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// Brand is the identity derived from a type name.
type Brand struct {
	// Type is the Terraform provider type name and the prefix of every resource address.
	Type string
	// Source is the provider source as written in required_providers: `namespace/type` or `host/namespace/type`.
	Source string
	// Registry is the full address `host/namespace/type` the plugin reports to Terraform.
	Registry string
	// UserAgent is the default user-agent prefix; the version is appended.
	UserAgent string
	// EnvPrefix is the type name upper-cased, plus an underscore.
	EnvPrefix string
	// Endpoint is the default API endpoint.
	Endpoint string
}

// DefaultHost is the registry host of a source address that names none.
const DefaultHost = "registry.terraform.io"

var legalSegment = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

// New derives a brand from a type name, published under the default source gpupaas-ai/<type>.
// A name that is not a legal Terraform provider name is an error.
func New(typeName string) (Brand, error) { return NewWithSource(typeName, "") }

// NewWithSource is New for a partner that publishes under its own registry address. The source is
// `namespace/type` or `host/namespace/type`; its last segment must be the type name, because Terraform
// finds the plugin by the file name terraform-provider-<type>.
func NewWithSource(typeName, source string) (Brand, error) {
	if !legalName.MatchString(typeName) {
		return Brand{}, fmt.Errorf("%q is not a legal provider type name: it must start with a lowercase letter and use only lowercase letters, digits and underscores", typeName)
	}
	if source == "" {
		source = "gpupaas-ai/" + typeName
	}
	parts := strings.Split(source, "/")
	if len(parts) < 2 || len(parts) > 3 || parts[len(parts)-1] != typeName {
		return Brand{}, fmt.Errorf("source %q must be namespace/%s or host/namespace/%s", source, typeName, typeName)
	}
	for _, seg := range parts {
		if !legalSegment.MatchString(seg) {
			return Brand{}, fmt.Errorf("source %q has an illegal segment %q", source, seg)
		}
	}
	registry := source
	if len(parts) == 2 {
		registry = DefaultHost + "/" + source
	}
	return Brand{
		Type:      typeName,
		Source:    source,
		Registry:  registry,
		UserAgent: "terraform-provider-" + typeName,
		EnvPrefix: strings.ToUpper(typeName) + "_",
		Endpoint:  DefaultEndpoint,
	}, nil
}

// MustNew is New for names that were already validated.
func MustNew(typeName string) Brand {
	b, err := New(typeName)
	if err != nil {
		panic(err)
	}
	return b
}

// pluginFile matches the file name Terraform requires for a provider plugin, with or without the version
// and the Windows extension: terraform-provider-acme, terraform-provider-acme_v0.1.0, ..._v0.1.0.exe.
// The version is only recognised in its semver form, so a name such as "my_v2" stays intact.
var pluginFile = regexp.MustCompile(`^terraform-provider-(.+?)(?:_v\d+\.\d+\.\d+[^/]*)?(?:\.exe)?$`)

// NameFromFile returns the brand name a plugin file name carries. Any path that is not a plugin file
// name (a test binary, `go run`) carries none.
func NameFromFile(path string) (string, bool) {
	m := pluginFile.FindStringSubmatch(filepath.Base(path))
	if m == nil {
		return "", false
	}
	return m[1], true
}

var (
	osArchDir  = regexp.MustCompile(`^[a-z0-9]+_[a-z0-9]+$`)
	versionDir = regexp.MustCompile(`^\d+\.\d+\.\d+`)
)

// SourceFromInstallPath returns the registry source a plugin was installed under. Terraform installs a
// provider at <host>/<namespace>/<type>/<version>/<os>_<arch>/<file> (in .terraform/providers and in the plugin
// cache), so the partner's real address is in the plugin's own path. A path of any other shape, such as a
// development override directory, carries none.
func SourceFromInstallPath(path, typeName string) (string, bool) {
	parts := strings.Split(filepath.ToSlash(strings.ReplaceAll(path, `\`, "/")), "/")
	n := len(parts)
	if n < 6 { // host/ns/type/version/os_arch/file
		return "", false
	}
	osArch, version, typ, ns, host := parts[n-2], parts[n-3], parts[n-4], parts[n-5], parts[n-6]
	if !osArchDir.MatchString(osArch) || !versionDir.MatchString(version) || typ != typeName {
		return "", false
	}
	src := host + "/" + ns + "/" + typ
	if _, err := NewWithSource(typeName, src); err != nil {
		return "", false
	}
	return src, true
}

// Resolve picks the brand of a process started as `executable`: a build-time pin wins, otherwise the
// plugin file name, otherwise the official provider. A plugin file name with an illegal brand name is an
// error, so a mistyped file name fails loudly instead of becoming another brand.
func Resolve(executable string) (Brand, error) {
	if Name != Official || Source != "" {
		return NewWithSource(Name, Source)
	}
	if n, ok := NameFromFile(executable); ok {
		if src, found := SourceFromInstallPath(executable, n); found {
			return NewWithSource(n, src)
		}
		return New(n)
	}
	return New(Official)
}

// Current is the brand this process runs as. It reads os.Args[0], not os.Executable: the latter resolves
// symlinks on Linux, which would lose the name a symlinked or renamed plugin was started under.
func Current() (Brand, error) { return Resolve(os.Args[0]) }

// Env is the environment variable name for a setting, such as Env("API_KEY") = "ACME_API_KEY".
func (b Brand) Env(suffix string) string { return b.EnvPrefix + suffix }

// Addr is the Terraform address of a resource or data source kind, such as Addr("project") = "acme_project".
func (b Brand) Addr(kind string) string { return b.Type + "_" + kind }

// Rewrite replaces the official identity in a text (a schema description or an error) with this brand's:
// resource addresses `gpupaas_<kind>` and environment variables `GPUPAAS_<NAME>`. The words "GPU PaaS",
// the registry namespace `gpupaas-ai` and the SDK name `gpupaas-go` are left alone: they contain a
// hyphen, not an underscore, after "gpupaas".
func (b Brand) Rewrite(s string) string {
	if b.Type == Official {
		return s
	}
	s = strings.ReplaceAll(s, strings.ToUpper(Official)+"_", b.EnvPrefix)
	return strings.ReplaceAll(s, Official+"_", b.Type+"_")
}
