package lintcfg

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

var (
	// toolchainLine matches a go.mod `toolchain goX.Y.Z` directive, with or
	// without a trailing `//` comment.
	toolchainLine = regexp.MustCompile(
		`(?m)^toolchain[ \t]+go([^\s/]+)[ \t]*(//.*)?$`)

	// setupGoStep matches the line that opens an actions/setup-go step.
	setupGoStep = regexp.MustCompile(`^(-\s*)?uses:\s*actions/setup-go@`)

	// goVersionFile matches the input that makes setup-go read go.mod.
	goVersionFile = regexp.MustCompile(`^go-version-file:\s*['"]?go\.mod['"]?$`)

	// goVersionKey matches a literal Go version given to any action.
	goVersionKey = regexp.MustCompile(`^(-\s*)?go-version:`)
)

// toolchainPins maps each distinct toolchain pin to the go.mod files that
// carry it, so a mismatch reports which files disagree rather than only that
// they do.
func toolchainPins(mods map[string]string) map[string][]string {
	pins := map[string][]string{}

	for name, content := range mods {
		for _, m := range toolchainLine.FindAllStringSubmatch(content, -1) {
			pins[m[1]] = append(pins[m[1]], name)
		}
	}

	for _, names := range pins {
		slices.Sort(names)
	}

	return pins
}

// setupGoViolations returns the number of actions/setup-go steps in a
// workflow and "name:line: text" for each way it names a Go version other
// than by reading go.mod: a setup-go step without `go-version-file: go.mod`,
// or a `go-version:` key anywhere. A step runs from its `uses:` line to the
// next line that opens a step.
func setupGoViolations(name, content string) (int, []string) {
	lines := activeLines(content)
	steps := 0

	var bad []string

	for i, line := range lines {
		if goVersionKey.MatchString(line) {
			bad = append(bad, fmt.Sprintf("%s:%d: %s", name, i+1, line))
		}

		if !setupGoStep.MatchString(line) {
			continue
		}

		steps++
		reads := false

		for _, next := range lines[i+1:] {
			if strings.HasPrefix(next, "- ") {
				break
			}

			reads = reads || goVersionFile.MatchString(next)
		}

		if !reads {
			bad = append(bad, fmt.Sprintf(
				"%s:%d: %s (no go-version-file: go.mod)", name, i+1, line))
		}
	}

	return steps, bad
}

// TestToolchainPins pins the go.mod matching on synthetic files, so
// TestToolchainPinIsUniform cannot pass merely because the regexp matches
// nothing — and a `//` comment after the version is not mistaken for part of
// it.
func TestToolchainPins(t *testing.T) {
	require.Equal(t, map[string][]string{
		"1.26.10": {"a/go.mod", "d/go.mod"},
		"1.26.9":  {"b/go.mod"},
	}, toolchainPins(map[string]string{
		"a/go.mod": "module a\n\ngo 1.25\n\ntoolchain go1.26.10\n",
		"b/go.mod": "module b\n\ntoolchain go1.26.9\n",
		"c/go.mod": "module c\n\ngo 1.25\n",
		"d/go.mod": "module d\n\ntoolchain go1.26.10 // FIX-042\n",
	}))
}

// TestSetupGoViolations pins the workflow rule on synthetic steps.
func TestSetupGoViolations(t *testing.T) {
	content := "jobs:\n" +
		"  a:\n" +
		"    steps:\n" +
		"      - uses: actions/setup-go@abc # v7.0.0\n" +
		"        with:\n" +
		"          go-version-file: go.mod\n" +
		"          cache: true\n" +
		"      - uses: actions/setup-go@abc\n" +
		"        with:\n" +
		"          go-version: '1.26.10' # matches go.mod today\n" +
		"      - uses: actions/setup-go@abc\n" +
		"        with:\n" +
		"          go-version-file: go.mod\n" +
		"      - uses: actions/setup-go@abc\n" +
		"        with:\n" +
		"          # go-version-file: go.mod\n" +
		"          go-version: stable\n" +
		"      - run: make tools\n"

	// The step at line 8 must not borrow the go-version-file of the correct
	// step that follows it at line 11.
	steps, bad := setupGoViolations("w.yml", content)
	require.Equal(t, 4, steps)
	require.Equal(t, []string{
		"w.yml:8: - uses: actions/setup-go@abc (no go-version-file: go.mod)",
		"w.yml:10: go-version: '1.26.10'",
		"w.yml:14: - uses: actions/setup-go@abc (no go-version-file: go.mod)",
		"w.yml:17: go-version: stable",
	}, bad)
}

// TestToolchainPinIsUniform keeps one Go toolchain across the repository.
// The Makefile exports GOTOOLCHAIN from the ROOT go.mod's toolchain line, and
// CI's setup-go reads the same line through `go-version-file: go.mod`
// (FIX-042). Every module's own toolchain line must therefore name that
// release, and no workflow may name a Go version of its own — a module left
// behind on a bump, or a workflow pin moved alone, builds and scans a
// different stdlib from the rest, which is exactly what the parity rule
// forbids and what nothing else would report.
func TestToolchainPinIsUniform(t *testing.T) {
	root := repoRoot(t)

	mods := map[string]string{}

	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() && (d.Name() == ".git" || d.Name() == "node_modules") {
			return filepath.SkipDir
		}

		if d.Name() == "go.mod" {
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}

			rel, err := filepath.Rel(root, p)
			if err != nil {
				return err
			}

			mods[rel] = string(b)
		}

		return nil
	})
	require.NoError(t, err)

	// A guard that finds nothing and passes is worse than no guard: the root
	// module and both of check.yml's setup-go steps must be among what was
	// measured.
	require.GreaterOrEqual(t, len(mods), 2, "found %d go.mod files", len(mods))

	rootPin := toolchainLine.FindStringSubmatch(mods["go.mod"])
	require.NotNil(t, rootPin, "the root go.mod has no toolchain line")

	var stray, bare []string

	for pin, names := range toolchainPins(mods) {
		if pin != rootPin[1] {
			stray = append(stray, "toolchain go"+pin+": "+strings.Join(names, ", "))
		}
	}

	for name, content := range mods {
		if !toolchainLine.MatchString(content) {
			bare = append(bare, name)
		}
	}

	var wfBad []string

	steps := 0

	for name, content := range readWorkflows(t) {
		n, bad := setupGoViolations(name, content)
		steps += n
		wfBad = append(wfBad, bad...)
	}

	slices.Sort(stray)
	slices.Sort(bare)
	slices.Sort(wfBad)

	require.Empty(t, stray,
		"the root go.mod pins go%s; these go.mod files disagree — bump every "+
			"toolchain line together:\n  %s",
		rootPin[1], strings.Join(stray, "\n  "))
	require.Empty(t, bare,
		"every go.mod must carry `toolchain go%s`; these have none:\n  %s",
		rootPin[1], strings.Join(bare, "\n  "))
	require.GreaterOrEqual(t, steps, 2,
		"expected check.yml's two setup-go steps, found %d", steps)
	require.Empty(t, wfBad,
		"workflows must take Go from go.mod (`go-version-file: go.mod`) and "+
			"name no version of their own:\n  %s",
		strings.Join(wfBad, "\n  "))
}
