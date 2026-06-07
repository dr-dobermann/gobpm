package covercheck

import (
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
