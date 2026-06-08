package covercheck

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseProfiles(t *testing.T) {
	in := strings.Join([]string{
		"mode: atomic",
		"github.com/x/y/a.go:10.2,12.16 2 1",
		"github.com/x/y/a.go:15.2,15.20 1 0",
		"github.com/x/y/b.go:3.10,5.4 1 7",
		"", // blank line tolerated
	}, "\n")

	got, err := ParseProfiles(strings.NewReader(in))
	require.NoError(t, err)
	require.Len(t, got, 2)

	require.Equal(t, []Block{
		{StartLine: 10, EndLine: 12, NumStmts: 2, Count: 1},
		{StartLine: 15, EndLine: 15, NumStmts: 1, Count: 0},
	}, got["github.com/x/y/a.go"])
	require.Equal(t, []Block{
		{StartLine: 3, EndLine: 5, NumStmts: 1, Count: 7},
	}, got["github.com/x/y/b.go"])
}

func TestParseProfilesIgnoresMalformed(t *testing.T) {
	in := "mode: set\nnot a real line\ngithub.com/x/y/a.go:1.1,2.2 notnum 1\n"

	got, err := ParseProfiles(strings.NewReader(in))
	require.NoError(t, err)
	require.Empty(t, got)
}

func TestParseDiff(t *testing.T) {
	in := strings.Join([]string{
		"diff --git a/internal/z/f.go b/internal/z/f.go",
		"--- a/internal/z/f.go",
		"+++ b/internal/z/f.go",
		"@@ -10,0 +11,2 @@ func F() {",
		"+\ta := 1",
		"+\treturn a",
		"@@ -20 +22 @@",
		"+\tx := 2",
		"diff --git a/old.go b/old.go",
		"--- a/old.go",
		"+++ /dev/null",
		"@@ -1,3 +0,0 @@",
	}, "\n")

	got, err := ParseDiff(strings.NewReader(in))
	require.NoError(t, err)

	require.Equal(t, []int{11, 12, 22}, got["internal/z/f.go"])
	require.NotContains(t, got, "old.go", "pure deletion contributes no new lines")
}

func TestEvaluatePatchCoverage(t *testing.T) {
	profiles := map[string][]Block{
		"github.com/x/y/internal/z/f.go": {
			{StartLine: 11, EndLine: 12, Count: 1}, // covered
			{StartLine: 22, EndLine: 22, Count: 0}, // not covered
		},
	}
	changed := map[string][]int{
		"internal/z/f.go": {11, 12, 22, 30}, // 30 is a comment/blank → not coverable
	}

	res := Evaluate(profiles, changed, DefaultExcluded)

	require.Equal(t, 3, res.Coverable, "lines 11,12,22 are statements; 30 is not")
	require.Equal(t, 2, res.Covered, "11,12 covered; 22 not")
	require.InDelta(t, 2.0/3.0, res.Ratio(), 1e-9)
	require.Equal(t, FileResult{Covered: 2, Coverable: 3}, res.PerFile["internal/z/f.go"])
}

func TestEvaluateExcludesAndEmpty(t *testing.T) {
	profiles := map[string][]Block{
		"github.com/x/y/internal/z/f_test.go": {{StartLine: 1, EndLine: 1, Count: 0}},
	}
	changed := map[string][]int{
		"internal/z/f_test.go": {1},      // excluded (test file)
		"examples/demo/main.go": {1, 2},  // excluded
		"generated/mock.go":     {1},     // excluded
		"cmd/tool/main.go":      {1},     // excluded
	}

	res := Evaluate(profiles, changed, DefaultExcluded)

	require.Equal(t, 0, res.Coverable)
	require.Equal(t, 1.0, res.Ratio(), "no coverable changed lines => pass")
	require.Empty(t, res.PerFile)
}

func TestParseProfilesRejectsMalformed(t *testing.T) {
	// Each malformed line exercises a distinct reject branch in
	// parseProfileLine / parseRange / lineOf; only the final valid line parses.
	in := strings.Join([]string{
		"mode: count",
		"a.go:1.1,2.2 1",      // != 3 fields
		"noColon 1 1",         // no colon
		"a.go:1.1 1 1",        // range has no comma
		"a.go:x.1,2.2 1 1",    // start line not numeric (lineOf atoi)
		"a.go:1.1,y.2 1 1",    // end line not numeric
		"a.go:1,2.2 1 1",      // start has no dot (lineOf)
		"a.go:1.1,2.2 nn 1",   // numStmts not numeric
		"a.go:1.1,2.2 1 cc",   // count not numeric
		"a.go:3.1,4.9 2 5",    // valid
	}, "\n")

	got, err := ParseProfiles(strings.NewReader(in))
	require.NoError(t, err)
	require.Equal(t, map[string][]Block{
		"a.go": {{StartLine: 3, EndLine: 4, NumStmts: 2, Count: 5}},
	}, got)
}

func TestParseDiffEdges(t *testing.T) {
	in := strings.Join([]string{
		"@@ -1 +1 @@",       // hunk before any +++ header -> ignored
		"+++ b/f.go\told",   // path carries a trailing tab-quoted suffix
		"@@ no plus here @@", // malformed hunk (no +) -> ignored
		"@@ -2 +x @@",        // new start not numeric (atoi) -> ignored
		"@@ -0,0 +5 @@",     // new count omitted -> a single line (5)
	}, "\n")

	got, err := ParseDiff(strings.NewReader(in))
	require.NoError(t, err)
	require.Equal(t, map[string][]int{"f.go": {5}}, got)
}

func TestEvaluateMatchedButNoCoverableLines(t *testing.T) {
	// The file matches a profile, but its changed lines fall outside every
	// block (comments/blank) -> Coverable 0 -> the file is skipped entirely.
	profiles := map[string][]Block{
		"github.com/x/y/a.go": {{StartLine: 10, EndLine: 12, Count: 1}},
	}
	changed := map[string][]int{"a.go": {99, 100}}

	res := Evaluate(profiles, changed, DefaultExcluded)
	require.Equal(t, 0, res.Coverable)
	require.Empty(t, res.PerFile)
}

func TestEvaluateUnmatchedProfileSkipped(t *testing.T) {
	// A changed file with no matching profile (e.g. a package with no tests)
	// contributes nothing — it hits the no-profile branch.
	profiles := map[string][]Block{
		"github.com/x/y/other.go": {{StartLine: 1, EndLine: 1, Count: 1}},
	}
	changed := map[string][]int{"internal/z/untested.go": {1, 2, 3}}

	res := Evaluate(profiles, changed, DefaultExcluded)
	require.Equal(t, 0, res.Coverable)
	require.Empty(t, res.PerFile)
}

func TestEvaluateExactPathMatch(t *testing.T) {
	// Profile keyed by the exact repo-relative path (no module prefix) still
	// matches — exercises matchProfile's equality branch.
	profiles := map[string][]Block{
		"a.go": {{StartLine: 1, EndLine: 1, Count: 1}},
	}
	changed := map[string][]int{"a.go": {1}}

	res := Evaluate(profiles, changed, nil) // nil exclude => no filtering
	require.Equal(t, FileResult{Covered: 1, Coverable: 1}, res.PerFile["a.go"])
}

func TestSplitList(t *testing.T) {
	require.Equal(t, []string{"a", "b", "c"}, splitList("a, b ,,c"))
	require.Nil(t, splitList("  ,, "))
}

func TestPct(t *testing.T) {
	require.Equal(t, 100.0, pct(0, 0))
	require.Equal(t, 50.0, pct(1, 2))
}

func TestReadProfiles(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "c.out")
	require.NoError(t, os.WriteFile(p,
		[]byte("mode: set\ngithub.com/x/a.go:1.1,2.2 1 1\n"), 0o600))

	// existing + missing path: the missing one is skipped, the existing parsed.
	got, err := ReadProfiles([]string{p, filepath.Join(dir, "missing.out")})
	require.NoError(t, err)
	require.Contains(t, got, "github.com/x/a.go")

	// all paths missing -> error (the gate must not pass vacuously).
	_, err = ReadProfiles([]string{filepath.Join(dir, "nope.out")})
	require.Error(t, err)

	// a path under a regular file -> open error that is NOT "not exist".
	_, err = ReadProfiles([]string{filepath.Join(p, "child")})
	require.Error(t, err)

	// a directory opens but fails to scan -> ParseProfiles error surfaces.
	_, err = ReadProfiles([]string{dir})
	require.Error(t, err)
}

func TestReport(t *testing.T) {
	res := Result{
		Covered:   1,
		Coverable: 2,
		PerFile:   map[string]FileResult{"a.go": {Covered: 1, Coverable: 2}},
	}

	var pass bytes.Buffer

	Report(&pass, res, 40)
	require.Contains(t, pass.String(), "a.go")
	require.Contains(t, pass.String(), "PASS")

	var fail bytes.Buffer

	Report(&fail, res, 90)
	require.Contains(t, fail.String(), "FAIL")
}

func TestGitDiff(t *testing.T) {
	// HEAD...HEAD is valid but empty -> no error, empty diff.
	out, err := GitDiff("HEAD")
	require.NoError(t, err)
	require.Empty(t, out)

	// an unresolvable ref -> error.
	_, err = GitDiff("this-ref-does-not-exist-zzz")
	require.Error(t, err)
}

func TestRunGate(t *testing.T) {
	dir := t.TempDir()
	prof := filepath.Join(dir, "coverage.txt")
	require.NoError(t, os.WriteFile(prof, []byte("mode: set\n"), 0o600))

	var buf bytes.Buffer

	// HEAD...HEAD => no changed lines => ratio 100% => pass.
	code, err := RunGate(&buf, 70, "HEAD", prof)
	require.NoError(t, err)
	require.Equal(t, 0, code)
	require.Contains(t, buf.String(), "PASS")

	// an unreachable floor fails even when nothing is coverable.
	code, err = RunGate(&buf, 101, "HEAD", prof)
	require.NoError(t, err)
	require.Equal(t, 1, code)

	// missing profile => error, not a vacuous pass.
	_, err = RunGate(&buf, 70, "HEAD", filepath.Join(dir, "none.txt"))
	require.Error(t, err)

	// unresolvable base ref => git diff error.
	_, err = RunGate(&buf, 70, "this-ref-does-not-exist-zzz", prof)
	require.Error(t, err)
}

func TestGate(t *testing.T) {
	// a single diff line larger than the scanner buffer makes ParseDiff error,
	// which Gate surfaces.
	huge := "+++ b/f.go\n+" + strings.Repeat("x", 1<<20+1) + "\n"

	_, err := Gate(&bytes.Buffer{}, 70, huge, nil)
	require.Error(t, err)
}

func TestDefaultExcluded(t *testing.T) {
	cases := map[string]bool{
		"internal/instance/instance.go":      false,
		"internal/instance/instance_test.go": true,
		"generated/mockx/mock.go":            true,
		"pkg/foo/generated/x.go":             true,
		"examples/basic/main.go":             true,
		"cmd/covercheck/main.go":             true,
		"pkg/model/flow/flow.go":             false,
	}

	for path, want := range cases {
		require.Equalf(t, want, DefaultExcluded(path), "DefaultExcluded(%q)", path)
	}
}
