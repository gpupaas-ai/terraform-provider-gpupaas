package brand_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Log output is not rewritten (that would need a redirect of the process's stderr), so log messages must be
// written without the official identity in the first place. This keeps it so.
func TestLogCallsNameNoOfficialIdentity(t *testing.T) {
	logCall := regexp.MustCompile(`\btflog\.\w+\(|\blog\.(Print|Fatal|Panic)\w*\(`)
	var bad []string
	err := filepath.WalkDir("..", func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") || strings.Contains(p, "/brand/") || strings.Contains(p, "/brandctl/") {
			return err
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(raw), "\n") {
			if logCall.MatchString(line) && strings.Contains(strings.ToLower(line), "gpupaas") {
				bad = append(bad, p+":"+itoa(i+1))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(bad) > 0 {
		t.Fatalf("log calls that name the official identity (build the message from the brand instead): %v", bad)
	}
}

func itoa(n int) string {
	s := ""
	for ; n > 0; n /= 10 {
		s = string(rune('0'+n%10)) + s
	}
	return s
}
