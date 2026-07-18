package lintcfg

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"testing"
)

// TestDepguardEnabled guards FIX-023 §3.2.4: the architecture import boundaries
// encoded in .golangci.yml's depguard block (core ⊅ runtime/adapters, model /
// examples ⊅ internal, adapters ⊅ adapters, no io/ioutil) are enforced ONLY
// while depguard is in the enabled linters. Dropping it from the enable list
// silently turns every rule off — the exact regression FIX-023 fixed. This test
// fails if that happens.
func TestDepguardEnabled(t *testing.T) {
	cfg, err := os.ReadFile(filepath.Join(repoRoot(t), ".golangci.yml"))
	if err != nil {
		t.Fatalf("read .golangci.yml: %v", err)
	}

	// an enable-list item line "- depguard" (a commented "# - depguard" would
	// not match, since the '#' precedes the dash).
	if !regexp.MustCompile(`(?m)^\s*-\s*depguard\s*$`).Match(cfg) {
		t.Fatal("depguard is not enabled in .golangci.yml — the architecture " +
			"import rules are OFF; re-add '- depguard' to linters.enable")
	}
}

// repoRoot walks up from this test file to the module root that holds
// .golangci.yml.
func repoRoot(t *testing.T) string {
	t.Helper()

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}

	for dir := filepath.Dir(file); ; {
		if _, err := os.Stat(filepath.Join(dir, ".golangci.yml")); err == nil {
			return dir
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf(".golangci.yml not found walking up from %s", file)
		}

		dir = parent
	}
}
