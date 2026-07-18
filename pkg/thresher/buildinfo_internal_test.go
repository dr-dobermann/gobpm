package thresher

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// TestVersionSentinelEmpty guards FIX-024 §3.2.1: the `version` var must stay
// empty so the single source of truth is `.version` (stamped in via ldflags) /
// debug.BuildInfo. A hardcoded literal here drifts from `.version` on every
// patch bump — the exact defect FIX-024 fixed.
func TestVersionSentinelEmpty(t *testing.T) {
	if version != "" {
		t.Fatalf("thresher.version must be empty (built from .version via "+
			"ldflags); got %q — do not hardcode a version literal", version)
	}
}

// TestDotVersionIsSemver ties the release string to a checked shape: `.version`
// (the source ldflags stamps) must be a `vMAJOR.MINOR.PATCH` semver, optionally
// with a pre-release suffix (e.g. v0.8.1-rc.1).
func TestDotVersionIsSemver(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), ".version"))
	if err != nil {
		t.Fatalf("read .version: %v", err)
	}

	got := strings.TrimSpace(string(raw))

	if !regexp.MustCompile(`^v\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?$`).MatchString(got) {
		t.Fatalf(".version %q is not a vMAJOR.MINOR.PATCH[-pre] semver", got)
	}
}

// repoRoot walks up from this test file to the module root that holds .version.
func repoRoot(t *testing.T) string {
	t.Helper()

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}

	for dir := filepath.Dir(file); ; {
		if _, err := os.Stat(filepath.Join(dir, ".version")); err == nil {
			return dir
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf(".version not found walking up from %s", file)
		}

		dir = parent
	}
}
