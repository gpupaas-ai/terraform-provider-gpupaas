package brandctl

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"golang.org/x/mod/modfile"
)

// SDKModule is the Go SDK every provider build is pinned against.
const SDKModule = "github.com/gpupaas-ai/gpupaas-go"

// Release rules (the single-source architecture): brands and controller generations are separate axes. A brand is
// a line in brands.yml and never a branch; a controller generation is a release line, the branch release/vX.Y, and
// a partner binary is built against an immutable SDK tag of that line.
var (
	// releaseVersion is the version of a release: a semantic version without the v prefix.
	releaseVersion = regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)(-[0-9A-Za-z.-]+)?$`)
	// sdkTag is an immutable SDK tag: vX.Y.Z, or a pre-release tag such as v0.2.0-rc.1.
	sdkTag = regexp.MustCompile(`^v(\d+)\.(\d+)\.(\d+)(-[0-9A-Za-z.-]+)?$`)
	// pseudoVersion ends in a commit timestamp and hash (v0.0.0-20261029101500-abcdef123456); it names a commit, not a release.
	pseudoVersion = regexp.MustCompile(`(^|[-.])\d{14}-[0-9a-f]{12}$`)
	releaseBranch = regexp.MustCompile(`^release/v(\d+\.\d+)$`)
)

// Line returns the release line "MAJOR.MINOR" of a version, with or without the v prefix.
func Line(version string) (string, error) {
	v := strings.TrimPrefix(version, "v")
	m := releaseVersion.FindStringSubmatch(v)
	if m == nil {
		return "", fmt.Errorf("%q is not a semantic version", version)
	}
	return m[1] + "." + m[2], nil
}

// CheckVersion accepts only a semantic version without the v prefix (0.4.0, 0.4.0-rc.1), so a descriptive alias
// such as "controller-2.4" or "latest" can never name a release.
func CheckVersion(version string) error {
	if !releaseVersion.MatchString(version) {
		return fmt.Errorf("version %q is not a semantic version such as 0.4.0 (no v prefix, no alias)", version)
	}
	return nil
}

// CheckSDKPin requires the provider to build against an immutable SDK tag, with no local replace directive.
// A local replace and a pseudo-version are fine for an engineer's inner loop and never for a partner binary.
func CheckSDKPin(sdkVersion string, replacedBy string) []string {
	var problems []string
	if replacedBy != "" {
		problems = append(problems, fmt.Sprintf("go.mod replaces %s with %s: a partner binary must build against the tagged SDK "+
			"(run: make pin-sdk SDK_VERSION=vX.Y.Z)", SDKModule, replacedBy))
	}
	switch {
	case sdkVersion == "":
		problems = append(problems, fmt.Sprintf("go.mod does not require %s", SDKModule))
	case pseudoVersion.MatchString(sdkVersion):
		problems = append(problems, fmt.Sprintf("go.mod pins %s to the pseudo-version %s, which names a commit and not a release: "+
			"pin an SDK tag (run: make pin-sdk SDK_VERSION=vX.Y.Z)", SDKModule, sdkVersion))
	case !sdkTag.MatchString(sdkVersion):
		problems = append(problems, fmt.Sprintf("go.mod pins %s to %q, which is not an SDK tag vX.Y.Z", SDKModule, sdkVersion))
	}
	return problems
}

// CheckLine requires the provider and the SDK it pins to be on the same release line: a provider vX.Y.Z speaks to
// the controller line of the SDK vX.Y.*.
func CheckLine(providerVersion, sdkVersion string) error {
	pl, err := Line(providerVersion)
	if err != nil {
		return err
	}
	sl, err := Line(sdkVersion)
	if err != nil {
		return nil // reported by CheckSDKPin
	}
	if pl != sl {
		return fmt.Errorf("provider %s is on line %s but the pinned SDK %s is on line %s: a provider and its SDK share the release line",
			providerVersion, pl, sdkVersion, sl)
	}
	return nil
}

// CheckBranch requires a release to be cut from main or from the release branch of its own line. A branch is
// never named after a brand: brands are lines of brands.yml, not branches.
func CheckBranch(branch, line string, brands []string) error {
	if branch == "main" {
		return nil
	}
	if m := releaseBranch.FindStringSubmatch(branch); m != nil {
		if m[1] != line {
			return fmt.Errorf("branch %s holds line %s but the release is on line %s: cut it from release/v%s", branch, m[1], line, line)
		}
		return nil
	}
	if rest, ok := strings.CutPrefix(branch, "release/"); ok {
		for _, b := range brands {
			if rest == b {
				return fmt.Errorf("branch %s is named after a brand: brands are lines of brands.yml, never branches; "+
					"branches are controller generations (release/vX.Y)", branch)
			}
		}
	}
	return fmt.Errorf("a release is cut from main or from release/v%s, not from branch %q", line, branch)
}

// matrixHeading is the README section that maps each release line to the controller releases it was verified against.
const matrixHeading = "Controller compatibility"

var (
	matrixStart = regexp.MustCompile(`(?m)^## ` + matrixHeading + `[ \t]*$`)
	nextHeading = regexp.MustCompile(`(?m)^#{1,2} `)
)

// CheckMatrix requires the README to document the controller for this release line: a row "| vX.Y.x | ... |" in
// the table under the level-2 heading "Controller compatibility". A heading of another level, or a row in another
// section, does not count.
func CheckMatrix(readme, line string) error {
	loc := matrixStart.FindStringIndex(readme)
	if loc == nil {
		return fmt.Errorf("README.md has no %q section (a level-2 heading)", matrixHeading)
	}
	section := readme[loc[1]:]
	if end := nextHeading.FindStringIndex(section); end != nil {
		section = section[:end[0]]
	}
	want := "v" + line + ".x"
	for _, row := range strings.Split(section, "\n") {
		cells := strings.Split(strings.Trim(strings.TrimSpace(row), "|"), "|")
		if len(cells) >= 2 && strings.TrimSpace(cells[0]) == want {
			return nil
		}
	}
	return fmt.Errorf("the %q table in README.md has no row for %s", matrixHeading, want)
}

// Build records what a release was built from.
type Build struct {
	Version       string `json:"version"`
	Commit        string `json:"commit"`
	Branch        string `json:"branch"`
	Dirty         bool   `json:"dirty"`
	SDK           string `json:"sdk"`
	SDKReplacedBy string `json:"sdkReplacedBy,omitempty"`
	ControllerAPI string `json:"controllerAPI"`
	// Local marks a build that skipped the release gate; it must not be handed to a partner.
	Local bool `json:"local"`
}

// Gate checks that the repository may release version, and returns what it would be built from and every problem
// found, so that one run lists all of them.
func Gate(repoDir, version string, brands []string) (Build, []string, error) {
	b := Build{Version: version}
	var problems []string
	if err := CheckVersion(version); err != nil {
		problems = append(problems, err.Error())
	}

	raw, err := os.ReadFile(filepath.Join(repoDir, "go.mod"))
	if err != nil {
		return b, nil, err
	}
	mf, err := modfile.Parse("go.mod", raw, nil)
	if err != nil {
		return b, nil, err
	}
	for _, r := range mf.Require {
		if r.Mod.Path == SDKModule {
			b.SDK = r.Mod.Version
		}
	}
	for _, r := range mf.Replace {
		if r.Old.Path == SDKModule {
			b.SDKReplacedBy = r.New.Path
			if r.New.Version != "" {
				b.SDKReplacedBy += "@" + r.New.Version
			}
		}
	}
	problems = append(problems, CheckSDKPin(b.SDK, b.SDKReplacedBy)...)
	if err := CheckLine(version, b.SDK); err != nil && b.SDKReplacedBy == "" {
		problems = append(problems, err.Error())
	}
	if line, err := Line(version); err == nil {
		b.ControllerAPI = "v" + line
		if readme, err := os.ReadFile(filepath.Join(repoDir, "README.md")); err != nil {
			problems = append(problems, "README.md is missing: it must hold the controller compatibility table")
		} else if err := CheckMatrix(string(readme), line); err != nil {
			problems = append(problems, err.Error())
		}
	}

	commit, err := run(repoDir, nil, "git", "rev-parse", "HEAD")
	if err != nil {
		problems = append(problems, "the repository is not a git checkout: a release is built from a commit")
		return b, problems, nil
	}
	b.Commit = strings.TrimSpace(commit)
	branch, _ := run(repoDir, nil, "git", "rev-parse", "--abbrev-ref", "HEAD")
	b.Branch = strings.TrimSpace(branch)
	if status, err := run(repoDir, nil, "git", "status", "--porcelain"); err == nil && strings.TrimSpace(status) != "" {
		b.Dirty = true
		problems = append(problems, "the working tree has uncommitted changes: commit them, so that every binary comes from one commit")
	}
	if line, err := Line(version); err == nil {
		if b.Branch == "HEAD" { // a tag checkout in CI
			tag, _ := run(repoDir, nil, "git", "describe", "--exact-match", "--tags", "HEAD")
			if strings.TrimSpace(tag) != "v"+version {
				problems = append(problems, fmt.Sprintf("HEAD is detached and is not the tag v%s", version))
			}
		} else if err := CheckBranch(b.Branch, line, brands); err != nil {
			problems = append(problems, err.Error())
		}
	}
	return b, problems, nil
}
