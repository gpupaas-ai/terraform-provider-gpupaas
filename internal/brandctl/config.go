// Package brandctl builds and packages the branded providers listed in brands.yml.
package brandctl

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// Entry is one brand-list item: a type name and, for a partner with its own registry, its source address.
type Entry struct {
	Name   string
	Source string
}

var legalName = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// Load reads brands.yml: a list whose items are a name (`- acme`) or a one-key map from name to source
// (`- acme: registry.acme.example/acme-corp/acme`). Illegal names are returned in skipped; the legal
// entries are still returned, so one bad line never blocks the others.
func Load(path string) (entries []Entry, skipped []string, err error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	var items []any
	if err := yaml.Unmarshal(raw, &items); err != nil {
		return nil, nil, fmt.Errorf("%s: %w", path, err)
	}
	seen := map[string]bool{}
	add := func(name, source string) {
		switch {
		case !legalName.MatchString(name):
			skipped = append(skipped, fmt.Sprintf("%q is not a legal provider type name", name))
		case seen[name]:
			skipped = append(skipped, fmt.Sprintf("%q is listed twice", name))
		default:
			seen[name] = true
			entries = append(entries, Entry{Name: name, Source: source})
		}
	}
	for _, it := range items {
		switch v := it.(type) {
		case string:
			add(strings.TrimSpace(v), "")
		case map[string]any:
			for k, val := range v {
				src, _ := val.(string)
				add(strings.TrimSpace(k), strings.TrimSpace(src))
			}
		default:
			skipped = append(skipped, fmt.Sprintf("unsupported entry %v", it))
		}
	}
	return entries, skipped, nil
}

// Platform is a GOOS/GOARCH pair.
type Platform struct{ OS, Arch string }

func (p Platform) String() string { return p.OS + "_" + p.Arch }

// ParsePlatforms reads "linux/amd64,darwin/arm64" (commas or spaces).
func ParsePlatforms(s string) ([]Platform, error) {
	var out []Platform
	for _, f := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' }) {
		parts := strings.Split(f, "/")
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return nil, fmt.Errorf("platform %q must be os/arch, such as linux/amd64", f)
		}
		out = append(out, Platform{parts[0], parts[1]})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no platforms given")
	}
	return out, nil
}
