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

// mustCall matches a Must* constructor call site, including a generic one:
// the type-argument list of MustMap[data.Value](nil) sits between the name and
// the parenthesis, so requiring "(" immediately after the name would miss it —
// as it did, until SRD-075 §4.6 found that call by hand.
var mustCall = regexp.MustCompile(`\bMust[A-Z]\w*[([]`)

// The ban is now absolute — there are no sanctioned forms and no exempt files.
//
// FIX-026 §3.1 carried two carve-outs: the argless MustBaseElement()/MustRecord()
// literals ("provably infallible"), and pkg/convert/bpmn/bpmn.go's init()
// registrations ("no caller to return an error to"). SRD-075 removed the need
// for both rather than keeping them documented, because an explanation attached
// to a panicking call in library code makes it permanent: "provably infallible"
// is a claim about today's call graph that the next change invalidates silently.
//
// The total paths are now expressed as total functions — foundation.
// EmptyBaseElement and values.EmptyRecord return no error because, with no
// options or fields, there is nothing that can fail — and convert's
// self-registration records an init() failure against the format, which Import
// and Export return at first use (convert.RegisterImporterAtInit).
// TestNoMustCallsInLibrary guards FIX-026 §3.2.16, tightened by SRD-075:
// library runtime code (pkg/ + internal/, non-test files) must not CALL
// panicking Must* constructors — a bad runtime input has to fail with a
// classified error through the fault machinery, never crash the engine.
// Defining Must* twins (fixture surface) stays legal; calling one does not,
// with no exceptions. Tests and examples are structurally out of scope (the
// walk covers only pkg/ and internal/ and skips *_test.go).
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
			out = append(out, rel+":"+strconv.Itoa(line)+": "+m+"...)")
		}
	}

	if err := sc.Err(); err != nil {
		t.Fatalf("scan %s: %v", path, err)
	}

	return out
}
