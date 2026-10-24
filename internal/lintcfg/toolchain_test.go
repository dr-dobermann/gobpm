package lintcfg

import (
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
	// toolchainLine matches a go.mod `toolchain goX.Y.Z` directive.
	toolchainLine = regexp.MustCompile(`(?m)^toolchain\s+go(\S+)\s*$`)

	// goVersionLine matches a workflow's `go-version: 'X.Y.Z'` input.
	goVersionLine = regexp.MustCompile(
		`(?m)^\s*go-version:\s*['"]?([0-9][^'"\s]*)['"]?\s*$`)
)

// toolchainPins maps each distinct pin to the files that carry it, so a
// mismatch reports which files disagree rather than only that they do.
func toolchainPins(
	files map[string]string, line *regexp.Regexp,
) map[string][]string {
	pins := map[string][]string{}

	for name, content := range files {
		for _, m := range line.FindAllStringSubmatch(content, -1) {
			pins[m[1]] = append(pins[m[1]], name)
		}
	}

	for _, names := range pins {
		slices.Sort(names)
	}

	return pins
}

// TestToolchainPins pins the matching itself on synthetic files, so
// TestToolchainPinIsUniform cannot pass merely because neither regexp
// matches anything.
func TestToolchainPins(t *testing.T) {
	mods := map[string]string{
		"a/go.mod": "module a\n\ngo 1.25\n\ntoolchain go1.26.10\n",
		"b/go.mod": "module b\n\ntoolchain go1.26.9\n",
		"c/go.mod": "module c\n\ngo 1.25\n",
	}
	require.Equal(t, map[string][]string{
		"1.26.10": {"a/go.mod"},
		"1.26.9":  {"b/go.mod"},
	}, toolchainPins(mods, toolchainLine))

	wf := map[string]string{
		"w.yml": "        with:\n          go-version: '1.26.10'\n" +
			"          go-version: \"1.26.10\"\n" +
			"          # go-version: '1.25.0'\n",
	}
	require.Equal(t, map[string][]string{"1.26.10": {"w.yml", "w.yml"}},
		toolchainPins(wf, goVersionLine))
}

// TestToolchainPinIsUniform keeps one Go toolchain across the repository.
// The Makefile exports GOTOOLCHAIN from the ROOT go.mod's toolchain line, and
// CI installs the workflow's go-version (FIX-042). Every module's own
// toolchain line and every go-version must therefore name the same release —
// a module left behind on a bump, or a workflow pin moved alone, builds and
// scans a different stdlib from the rest, which is exactly what the parity
// rule forbids and what nothing else would report.
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

	check, err := os.ReadFile(
		filepath.Join(root, ".github", "workflows", "check.yml"))
	require.NoError(t, err)

	modPins := toolchainPins(mods, toolchainLine)
	wfPins := toolchainPins(
		map[string]string{"check.yml": string(check)}, goVersionLine)

	// A guard that finds nothing and passes is worse than no guard. The root
	// module and the two check.yml jobs must be among what was measured.
	require.GreaterOrEqual(t, len(mods), 2, "found %d go.mod files", len(mods))

	rootPin := toolchainLine.FindStringSubmatch(mods["go.mod"])
	require.NotNil(t, rootPin, "the root go.mod has no toolchain line")

	require.Len(t, wfPins[rootPin[1]], 2,
		"check.yml should set go-version: '%s' in both of its jobs", rootPin[1])

	var stray []string

	for pin, names := range modPins {
		if pin != rootPin[1] {
			stray = append(stray, "toolchain go"+pin+": "+strings.Join(names, ", "))
		}
	}

	for pin, names := range wfPins {
		if pin != rootPin[1] {
			stray = append(stray, "go-version "+pin+": "+strings.Join(names, ", "))
		}
	}

	slices.Sort(stray)
	require.Empty(t, stray,
		"the root go.mod pins go%s; these disagree — bump every go.mod's "+
			"toolchain line and check.yml's go-version together:\n  %s",
		rootPin[1], strings.Join(stray, "\n  "))

	var bare []string

	for name, content := range mods {
		if !toolchainLine.MatchString(content) {
			bare = append(bare, name)
		}
	}

	slices.Sort(bare)
	require.Empty(t, bare,
		"every go.mod must carry `toolchain go%s`; these have none:\n  %s",
		rootPin[1], strings.Join(bare, "\n  "))
}
