// Command brandctl builds and packages the white-label providers listed in brands.yml.
//
//	brandctl list
//	brandctl check -version 0.4.0
//	brandctl release -version 0.4.0 [-brands acme,contoso] [-platforms linux/amd64,darwin/arm64] [-out dist] [-tfplugindocs path] [-allow-local]
//
// check and release run the release gate: the SDK pinned to an immutable tag with no local replace, the provider on
// the same release line, a clean tree on main or release/vX.Y, and the controller compatibility table in README.md.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/gpupaas-ai/terraform-provider-gpupaas/internal/brandctl"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: brandctl list|check|release [flags]")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "list":
		entries, skipped, err := brandctl.Load("brands.yml")
		fail(err)
		for _, e := range entries {
			src := e.Source
			if src == "" {
				src = "(default gpupaas-ai/" + e.Name + ")"
			}
			fmt.Printf("%s\t%s\n", e.Name, src)
		}
		for _, s := range skipped {
			fmt.Fprintln(os.Stderr, "skipped:", s)
		}
	case "check":
		fs := flag.NewFlagSet("check", flag.ExitOnError)
		version := fs.String("version", "", "release version, such as 0.4.0 (required)")
		_ = fs.Parse(os.Args[2:])
		entries, _, err := brandctl.Load("brands.yml")
		fail(err)
		var names []string
		for _, e := range entries {
			names = append(names, e.Name)
		}
		build, problems, err := brandctl.Gate(".", *version, names)
		fail(err)
		if len(problems) > 0 {
			fmt.Fprintf(os.Stderr, "release gate failed, %d problem(s):\n  - %s\n", len(problems), strings.Join(problems, "\n  - "))
			os.Exit(1)
		}
		fmt.Printf("release gate passed: %s on %s at %.12s, SDK %s, controller API %s\n", build.Version, build.Branch, build.Commit, build.SDK, build.ControllerAPI)
	case "release":
		fs := flag.NewFlagSet("release", flag.ExitOnError)
		brands := fs.String("brands", "", "comma-separated names (default: all of brands.yml)")
		platforms := fs.String("platforms", runtime.GOOS+"/"+runtime.GOARCH, "comma-separated os/arch list")
		version := fs.String("version", "", "release version, such as 0.4.0 (required; with -allow-local it defaults to 0.0.0-local)")
		allowLocal := fs.Bool("allow-local", false, "build although the release gate fails (local replace, uncommitted changes); for your own testing, never for a partner")
		out := fs.String("out", "dist", "output directory, relative to the repository")
		tool := fs.String("tfplugindocs", "", "tfplugindocs executable (default: from PATH)")
		_ = fs.Parse(os.Args[2:])
		if *version == "" && *allowLocal {
			*version = "0.0.0-local"
		}
		if *version == "" {
			fail(fmt.Errorf("-version is required: a release names its version explicitly, such as -version 0.4.0"))
		}
		ps, err := brandctl.ParsePlatforms(*platforms)
		fail(err)
		var names []string
		if *brands != "" {
			names = strings.Split(*brands, ",")
		}
		fail(brandctl.Release(context.Background(), brandctl.Options{RepoDir: ".", Out: *out, Brands: names, Platforms: ps,
			Version: *version, Tfplugindocs: *tool, AllowLocal: *allowLocal, Log: os.Stderr}))
	default:
		fmt.Fprintln(os.Stderr, "usage: brandctl list|check|release [flags]")
		os.Exit(2)
	}
}

func fail(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
