package resources

import (
	"strings"
	"testing"
)

func TestParseClusterScopedImportID(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"alpha", "alpha", false},
		{"", "", true},
		{"a/b", "", true},
	}
	for _, c := range cases {
		got, err := ParseClusterScopedImportID(c.in)
		if (err != nil) != c.wantErr {
			t.Fatalf("ParseClusterScopedImportID(%q) err=%v wantErr=%v", c.in, err, c.wantErr)
		}
		if got != c.want {
			t.Fatalf("ParseClusterScopedImportID(%q) = %q want %q", c.in, got, c.want)
		}
	}
}

func TestParseProjectScopedImportID(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in            string
		project, name string
		wantErr       bool
	}{
		{"proj/name", "proj", "name", false},
		{"only", "", "", true},
		{"proj//name", "", "", true},
		{"a/b/c", "", "", true},
	}
	for _, c := range cases {
		p, n, err := ParseProjectScopedImportID(c.in)
		if (err != nil) != c.wantErr {
			t.Fatalf("ParseProjectScopedImportID(%q) err=%v wantErr=%v", c.in, err, c.wantErr)
		}
		if !c.wantErr && (p != c.project || n != c.name) {
			t.Fatalf("ParseProjectScopedImportID(%q) = (%q,%q) want (%q,%q)", c.in, p, n, c.project, c.name)
		}
	}
}

func TestParseWorkspaceScopedImportID(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in                       string
		project, workspace, name string
		wantErr                  bool
	}{
		{"p/w/n", "p", "w", "n", false},
		{"p/n", "", "", "", true},
		{"//n", "", "", "", true},
	}
	for _, c := range cases {
		p, w, n, err := ParseWorkspaceScopedImportID(c.in)
		if (err != nil) != c.wantErr {
			t.Fatalf("ParseWorkspaceScopedImportID(%q) err=%v wantErr=%v", c.in, err, c.wantErr)
		}
		if !c.wantErr && (p != c.project || w != c.workspace || n != c.name) {
			t.Fatalf("ParseWorkspaceScopedImportID(%q) = (%q,%q,%q) want (%q,%q,%q)", c.in, p, w, n, c.project, c.workspace, c.name)
		}
	}
}

func TestParseFlexibleScopedImportID(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in                       string
		project, workspace, name string
		wantErr                  bool
	}{
		{"p/n", "p", "", "n", false},
		{"p/w/n", "p", "w", "n", false},
		{"only", "", "", "", true},
		{"a/b/c/d", "", "", "", true},
		{"//n", "", "", "", true},
	}
	for _, c := range cases {
		p, w, n, err := ParseFlexibleScopedImportID(c.in)
		if (err != nil) != c.wantErr {
			t.Fatalf("ParseFlexibleScopedImportID(%q) err=%v wantErr=%v", c.in, err, c.wantErr)
		}
		if !c.wantErr && (p != c.project || w != c.workspace || n != c.name) {
			t.Fatalf("ParseFlexibleScopedImportID(%q) = (%q,%q,%q) want (%q,%q,%q)", c.in, p, w, n, c.project, c.workspace, c.name)
		}
	}
}

func TestFirstNonEmpty(t *testing.T) {
	t.Parallel()
	if got := firstNonEmpty("", "", "x"); got != "x" {
		t.Fatalf("firstNonEmpty = %q want x", got)
	}
	if got := firstNonEmpty("", ""); got != "" {
		t.Fatalf("firstNonEmpty = %q want empty", got)
	}
	if got := firstNonEmpty("first", "second"); got != "first" {
		t.Fatalf("firstNonEmpty = %q want first", got)
	}
}

func TestNullableString(t *testing.T) {
	t.Parallel()
	if !nullableString("").IsNull() {
		t.Fatal("expected null for empty string")
	}
	if v := nullableString("value"); v.ValueString() != "value" {
		t.Fatalf("got %q want value", v.ValueString())
	}
}

// Sanity check: ensure import error messages mention the expected form.
func TestImportErrorMessages(t *testing.T) {
	t.Parallel()
	_, _, err := ParseProjectScopedImportID("bad")
	if err == nil || !strings.Contains(err.Error(), "<project>/<name>") {
		t.Fatalf("unexpected error: %v", err)
	}
	_, _, _, err = ParseWorkspaceScopedImportID("a/b")
	if err == nil || !strings.Contains(err.Error(), "<project>/<workspace>/<name>") {
		t.Fatalf("unexpected error: %v", err)
	}
}
