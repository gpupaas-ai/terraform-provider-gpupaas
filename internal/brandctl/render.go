package brandctl

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/gpupaas-ai/terraform-provider-gpupaas/internal/brand"
)

// RenderExamples copies the shared examples (written with the official names) to dst for brand b: the
// directories gpupaas_<kind> become <type>_<kind>, and the identity strings in every file are rewritten.
func RenderExamples(src, dst string, b brand.Brand) error {
	if err := os.RemoveAll(dst); err != nil {
		return err
	}
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		parts := strings.Split(rel, string(filepath.Separator))
		for i, seg := range parts {
			if strings.HasPrefix(seg, brand.Official+"_") {
				parts[i] = b.Type + "_" + strings.TrimPrefix(seg, brand.Official+"_")
			}
		}
		target := filepath.Join(append([]string{dst}, parts...)...)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, []byte(b.RewriteExample(string(raw))), 0o644)
	})
}

// ScanTree lists the places in dir that still carry the official identity.
func ScanTree(dir string, b brand.Brand) ([]string, error) {
	var out []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		if strings.Contains(rel, brand.Official+"_") {
			out = append(out, rel+": path carries the official name")
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		for _, l := range b.Leftovers(string(raw)) {
			out = append(out, fmt.Sprintf("%s:%s", rel, l))
		}
		return nil
	})
	return out, err
}

// MissingPages lists the docs pages that a brand's registered resources and data sources should have
// under docsDir and do not.
func MissingPages(docsDir string, b brand.Brand, resources, dataSources []string) []string {
	var missing []string
	check := func(folder string, names []string) {
		for _, n := range names {
			page := filepath.Join(docsDir, folder, strings.TrimPrefix(n, b.Type+"_")+".md")
			if _, err := os.Stat(page); err != nil {
				missing = append(missing, page)
			}
		}
	}
	check("resources", resources)
	check("data-sources", dataSources)
	return missing
}
