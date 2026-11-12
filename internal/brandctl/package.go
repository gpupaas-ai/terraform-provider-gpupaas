package brandctl

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gpupaas-ai/terraform-provider-gpupaas/internal/brand"
)

// PluginFile is the file name of the provider inside a release zip.
func PluginFile(b brand.Brand, version string, p Platform) string {
	name := fmt.Sprintf("terraform-provider-%s_v%s", b.Type, version)
	if p.OS == "windows" {
		name += ".exe"
	}
	return name
}

// ZipName is the registry file name of a platform zip.
func ZipName(b brand.Brand, version string, p Platform) string {
	return fmt.Sprintf("terraform-provider-%s_%s_%s.zip", b.Type, version, p)
}

// PackagePlatform writes dir/<ZipName> holding the shared binary under this brand's plugin file name.
func PackagePlatform(binary string, p Platform, b brand.Brand, version, dir string) error {
	f, err := os.Create(filepath.Join(dir, ZipName(b, version, p)))
	if err != nil {
		return err
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	in, err := os.Open(binary)
	if err != nil {
		return err
	}
	defer in.Close()
	hdr := &zip.FileHeader{Name: PluginFile(b, version, p), Method: zip.Deflate}
	hdr.SetMode(0o755)
	w, err := zw.CreateHeader(hdr)
	if err != nil {
		return err
	}
	if _, err := io.Copy(w, in); err != nil {
		return err
	}
	return zw.Close()
}

// WriteChecksums writes dir/terraform-provider-<type>_<version>_SHA256SUMS for the platform zips in dir and for
// the release manifest, which WriteManifests must have written first: the Terraform Registry expects the checksum
// file to list every zip and the manifest. The partner signs this file with its own GPG key.
func WriteChecksums(dir string, b brand.Brand, version string) error {
	files, err := filepath.Glob(filepath.Join(dir, "terraform-provider-"+b.Type+"_"+version+"_*_*.zip"))
	if err != nil {
		return err
	}
	manifest := filepath.Join(dir, fmt.Sprintf("terraform-provider-%s_%s_manifest.json", b.Type, version))
	if _, err := os.Stat(manifest); err != nil {
		return fmt.Errorf("the release manifest must exist before the checksums are written: %w", err)
	}
	files = append(files, manifest)
	sort.Strings(files)
	var sb strings.Builder
	for _, z := range files {
		raw, err := os.ReadFile(z)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(raw)
		fmt.Fprintf(&sb, "%s  %s\n", hex.EncodeToString(sum[:]), filepath.Base(z))
	}
	return os.WriteFile(filepath.Join(dir, fmt.Sprintf("terraform-provider-%s_%s_SHA256SUMS", b.Type, version)), []byte(sb.String()), 0o644)
}

const registryManifest = "{\n  \"version\": 1,\n  \"metadata\": {\n    \"protocol_versions\": [\"6.0\"]\n  }\n}\n"

// WriteManifests writes the registry manifest twice: as the release asset
// terraform-provider-<type>_<version>_manifest.json, and as terraform-registry-manifest.json for a repository
// that a VCS-backed registry reads.
func WriteManifests(dir string, b brand.Brand, version string) error {
	if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("terraform-provider-%s_%s_manifest.json", b.Type, version)), []byte(registryManifest), 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "terraform-registry-manifest.json"), []byte(registryManifest), 0o644)
}

// ZipTrees writes a zip holding the given directories under their archive names (for example docs, examples).
func ZipTrees(dst string, trees map[string]string) error {
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	names := make([]string, 0, len(trees))
	for n := range trees {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		root := trees[name]
		err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			rel, _ := filepath.Rel(root, p)
			w, err := zw.Create(filepath.ToSlash(filepath.Join(name, rel)))
			if err != nil {
				return err
			}
			raw, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			_, err = w.Write(raw)
			return err
		})
		if err != nil {
			return err
		}
	}
	return zw.Close()
}

// CopyTree copies a directory.
func CopyTree(src, dst string) error {
	if err := os.RemoveAll(dst); err != nil {
		return err
	}
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, raw, 0o644)
	})
}
