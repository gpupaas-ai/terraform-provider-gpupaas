package brandctl_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gpupaas-ai/terraform-provider-gpupaas/internal/brandctl"
)

func TestLineAndVersion(t *testing.T) {
	for v, want := range map[string]string{"0.4.0": "0.4", "v0.2.1": "0.2", "1.10.3-rc.1": "1.10"} {
		got, err := brandctl.Line(v)
		if err != nil || got != want {
			t.Errorf("Line(%q) = %q, %v; want %q", v, got, err, want)
		}
	}
	for _, v := range []string{"", "controller-2.4", "latest", "0.4", "v0.4.0.1"} {
		if _, err := brandctl.Line(v); err == nil {
			t.Errorf("Line(%q) accepted", v)
		}
	}
	for _, v := range []string{"0.4.0", "0.0.1-test.3", "1.0.0-rc.1"} {
		if err := brandctl.CheckVersion(v); err != nil {
			t.Errorf("CheckVersion(%q): %v", v, err)
		}
	}
	for _, v := range []string{"v0.4.0", "controller-2.4", "latest", "0.4", ""} {
		if err := brandctl.CheckVersion(v); err == nil {
			t.Errorf("CheckVersion(%q) accepted", v)
		}
	}
}

func TestSDKPinNeedsAnImmutableTagAndNoReplace(t *testing.T) {
	for _, v := range []string{"v0.4.0", "v0.2.1", "v1.0.0-rc.1"} {
		if p := brandctl.CheckSDKPin(v, ""); len(p) != 0 {
			t.Errorf("%s rejected: %v", v, p)
		}
	}
	cases := map[string]struct{ v, replace, want string }{
		"pseudo version":      {"v0.0.0-00010101000000-000000000000", "", "pseudo-version"},
		"real pseudo version": {"v0.3.1-0.20261029101500-abcdef123456", "", "pseudo-version"},
		"replace":             {"v0.4.0", "../gpupaas-go", "replaces"},
		"alias":               {"controller-2.4", "", "not an SDK tag"},
		"missing":             {"", "", "does not require"},
	}
	for name, c := range cases {
		p := brandctl.CheckSDKPin(c.v, c.replace)
		if len(p) == 0 || !strings.Contains(strings.Join(p, "\n"), c.want) {
			t.Errorf("%s: problems %v, want one mentioning %q", name, p, c.want)
		}
	}
}

func TestProviderAndSDKShareTheLine(t *testing.T) {
	if err := brandctl.CheckLine("0.4.2", "v0.4.0"); err != nil {
		t.Errorf("same line rejected: %v", err)
	}
	if err := brandctl.CheckLine("0.4.0", "v0.2.1"); err == nil {
		t.Error("a provider on line 0.4 with an SDK on line 0.2 was accepted")
	}
}

func TestBranchRules(t *testing.T) {
	brands := []string{"gpupaas", "acme"}
	ok := map[string]string{"main": "0.4", "release/v0.2": "0.2"}
	for b, line := range ok {
		if err := brandctl.CheckBranch(b, line, brands); err != nil {
			t.Errorf("%s on line %s: %v", b, line, err)
		}
	}
	bad := map[string][2]string{
		"release/acme":       {"0.2", "named after a brand"},
		"release/gpupaas":    {"0.2", "named after a brand"},
		"release/v0.2":       {"0.4", "holds line 0.2"},
		"feature/xyz":        {"0.2", "not from branch"},
		"insecure-skip-sslx": {"0.2", "not from branch"},
	}
	for b, c := range bad {
		err := brandctl.CheckBranch(b, c[0], brands)
		if err == nil || !strings.Contains(err.Error(), c[1]) {
			t.Errorf("%s on line %s: %v, want an error mentioning %q", b, c[0], err, c[1])
		}
	}
}

func TestMatrixNeedsARowForTheLine(t *testing.T) {
	readme := "# x\n\n## Controller compatibility\n\n| Line | Controller |\n|---|---|\n| v0.1.x | dev |\n| v0.2.x | 4.2 |\n\n## Other\n\n| v0.9.x | nope |\n"
	for _, line := range []string{"0.1", "0.2"} {
		if err := brandctl.CheckMatrix(readme, line); err != nil {
			t.Errorf("line %s: %v", line, err)
		}
	}
	// a row of another section does not count; neither does a missing section
	if err := brandctl.CheckMatrix(readme, "0.9"); err == nil {
		t.Error("a row outside the section was accepted")
	}
	if err := brandctl.CheckMatrix(readme, "0.3"); err == nil {
		t.Error("a missing line was accepted")
	}
	if err := brandctl.CheckMatrix("# nothing\n", "0.1"); err == nil {
		t.Error("a README without the section was accepted")
	}
	// a heading of another level is not the section, and a row under it does not satisfy the gate
	sub := "# x\n\n### Controller compatibility\n\n| v0.1.x | dev |\n"
	if err := brandctl.CheckMatrix(sub, "0.1"); err == nil {
		t.Error("a level-3 heading was accepted")
	}
	// the section ends at the next heading of level 1 or 2, and a level-3 heading inside it does not end it
	inner := "# x\n\n## Controller compatibility\n\n### Notes\n\n| v0.3.x | dev |\n\n## Other\n"
	if err := brandctl.CheckMatrix(inner, "0.3"); err != nil {
		t.Errorf("a row under a level-3 heading inside the section: %v", err)
	}
	if err := brandctl.CheckMatrix("## Controller compatibility extras\n\n| v0.1.x | dev |\n", "0.1"); err == nil {
		t.Error("a heading that merely starts with the words was accepted")
	}
}

// repo builds a git checkout holding go.mod, README.md and brands.yml, and returns its directory.
func repo(t *testing.T, goMod, branch string) string {
	t.Helper()
	dir := t.TempDir()
	write(t, filepath.Join(dir, "go.mod"), goMod)
	write(t, filepath.Join(dir, "README.md"), "# p\n\n## Controller compatibility\n\n| Line | Controller |\n|---|---|\n| v0.4.x | 4.2 |\n")
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q", "-b", branch)
	git("add", "-A")
	git("commit", "-q", "-m", "init")
	return dir
}

const tagged = "module example.com/p\n\ngo 1.24\n\nrequire github.com/gpupaas-ai/gpupaas-go v0.4.0\n"

func TestGatePassesOnACleanTaggedMainCheckout(t *testing.T) {
	dir := repo(t, tagged, "main")
	b, problems, err := brandctl.Gate(dir, "0.4.1", []string{"gpupaas", "acme"})
	if err != nil || len(problems) != 0 {
		t.Fatalf("problems %v, err %v", problems, err)
	}
	if b.SDK != "v0.4.0" || b.Branch != "main" || b.ControllerAPI != "v0.4" || b.Commit == "" || b.Dirty {
		t.Fatalf("build info %+v", b)
	}
}

func TestGateListsEveryProblemAtOnce(t *testing.T) {
	bad := "module example.com/p\n\ngo 1.24\n\nrequire github.com/gpupaas-ai/gpupaas-go v0.0.0-00010101000000-000000000000\n\nreplace github.com/gpupaas-ai/gpupaas-go => ../gpupaas-go\n"
	dir := repo(t, bad, "release/acme")
	write(t, filepath.Join(dir, "dirty.txt"), "x")
	_, problems, err := brandctl.Gate(dir, "0.4.0", []string{"gpupaas", "acme"})
	if err != nil {
		t.Fatal(err)
	}
	all := strings.Join(problems, "\n")
	for _, want := range []string{"replaces", "pseudo-version", "uncommitted", "named after a brand"} {
		if !strings.Contains(all, want) {
			t.Errorf("no problem mentions %q:\n%s", want, all)
		}
	}
}

func TestGateRejectsALineThatTheMatrixDoesNotDocument(t *testing.T) {
	dir := repo(t, strings.Replace(tagged, "v0.4.0", "v0.5.0", 1), "main")
	_, problems, err := brandctl.Gate(dir, "0.5.0", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(problems, "\n"), "no row for v0.5.x") {
		t.Fatalf("problems %v", problems)
	}
}

func TestGateRejectsADetachedHeadThatIsNotTheTag(t *testing.T) {
	dir := repo(t, tagged, "main")
	if out, err := exec.Command("git", "-C", dir, "checkout", "-q", "--detach").CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	_, problems, _ := brandctl.Gate(dir, "0.4.0", nil)
	if !strings.Contains(strings.Join(problems, "\n"), "not the tag v0.4.0") {
		t.Fatalf("problems %v", problems)
	}
	if out, err := exec.Command("git", "-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.com", "tag", "v0.4.0").CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	_, problems, _ = brandctl.Gate(dir, "0.4.0", nil)
	if len(problems) != 0 {
		t.Fatalf("a checkout of the tag failed the gate: %v", problems)
	}
}
