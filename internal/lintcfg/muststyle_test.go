package lintcfg

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// mustCall matches a Must* constructor call site.
var mustCall = regexp.MustCompile(`\bMust[A-Z]\w*\(`)

// sanctioned are the two provably-infallible literal forms FIX-026 §3.1
// permits in library code: the ARGLESS calls only — with zero options /
// fields there is no error path in the underlying New* constructor. Any
// argument-carrying form is banned like every other Must*.
var sanctioned = map[string]bool{
	"MustBaseElement(": true,
	"MustRecord(":      true,
}

// TestNoMustCallsInLibrary guards FIX-026 §3.2.16: library runtime code
// (pkg/ + internal/, non-test files) must not CALL panicking Must*
// constructors — a bad runtime input has to fail with a classified error
// through the fault machinery, never crash the engine. Defining Must* twins
// (fixture surface) stays legal, as do the two sanctioned argless literal
// forms above. Tests and examples are structurally out of scope (the walk
// covers only pkg/ and internal/ and skips *_test.go).
//
// A failure names the offending path:line — convert the call to the New*
// constructor and propagate the error (the FIX-026 reference pattern:
// pkg/model/activities/brule_task.go commitResult).
func TestNoMustCallsInLibrary(t *testing.T) {
	root := repoRoot(t)

	var offenders []string

	for _, dir := range []string{"pkg", "internal"} {
		err := filepath.WalkDir(filepath.Join(root, dir),
			func(path string, d os.DirEntry, err error) error {
				if err != nil {
					return err
				}

				if d.IsDir() || !strings.HasSuffix(path, ".go") ||
					strings.HasSuffix(path, "_test.go") {
					return nil
				}

				rel, err := filepath.Rel(root, path)
				if err != nil {
					rel = path
				}

				offenders = append(offenders,
					mustCallSites(t, path, rel)...)

				return nil
			})
		if err != nil {
			t.Fatalf("walking %s: %v", dir, err)
		}
	}

	if len(offenders) > 0 {
		t.Fatalf("Must* call site(s) in library code (FIX-026: use the "+
			"error-returning New* constructors; Must* is for "+
			"tests/fixtures):\n  %s",
			strings.Join(offenders, "\n  "))
	}
}

// mustCallSites returns the banned Must* call sites of one file as
// "rel:line: MustX" entries.
func mustCallSites(t *testing.T, path, rel string) []string {
	t.Helper()

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close() //nolint:errcheck // read-only

	var out []string

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1024*1024), 1024*1024)

	line := 0
	for sc.Scan() {
		line++
		text := strings.TrimSpace(sc.Text())

		// comments and Must*-definition lines (func/method signatures) are
		// not call sites.
		if strings.HasPrefix(text, "//") || strings.HasPrefix(text, "func ") {
			continue
		}

		for _, m := range mustCall.FindAllString(text, -1) {
			// the sanctioned forms pass only as the exact argless call.
			if sanctioned[m] && strings.Contains(text, m+")") {
				continue
			}

			out = append(out, rel+":"+strconv.Itoa(line)+": "+m+"...)")
		}
	}

	if err := sc.Err(); err != nil {
		t.Fatalf("scan %s: %v", path, err)
	}

	return out
}
