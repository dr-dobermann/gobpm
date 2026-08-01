package lintcfg

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The tool pins are duplicated on purpose: the Makefile keeps `make` usable
// locally, and the workflow installs its own list so CI does not depend on a
// Make target. The cost of that choice is that the two can drift, and drift is
// invisible until a build fails — adding linkcheck to `make tools` without
// adding it to the workflow produced exactly that: a green local `make ci`
// (the binary was already in PATH) and a red CI job.
//
// This test makes the duplication safe rather than removing it.
func TestMakefilePinsAppearInWorkflow(t *testing.T) {
	root := repoRoot(t)

	mk, err := os.ReadFile(filepath.Join(root, "Makefile"))
	require.NoError(t, err)

	wf, err := os.ReadFile(filepath.Join(root, ".github/workflows/check.yml"))
	require.NoError(t, err)

	pins := regexp.MustCompile(`(?m)^([A-Z_]+)_VERSION\s*:?=\s*(v[0-9][0-9.]*)`).
		FindAllStringSubmatch(string(mk), -1)

	// A guard that finds nothing and passes is worse than no guard: it reports
	// success while measuring an empty set. The count is asserted so a Makefile
	// restructure fails here rather than silently disarming the check.
	require.GreaterOrEqual(t, len(pins), 4,
		"expected at least 4 *_VERSION pins in the Makefile, found %d — "+
			"has the Makefile been restructured?", len(pins))

	workflow := string(wf)

	var missing []string

	for _, p := range pins {
		name, version := p[1], p[2]
		if !strings.Contains(workflow, version) {
			missing = append(missing, name+"_VERSION = "+version)
		}
	}

	require.Empty(t, missing,
		"these Makefile tool pins do not appear in .github/workflows/check.yml, "+
			"so CI installs a different version than `make tools` — or none at "+
			"all. Add the install line to the workflow:\n  %s",
		strings.Join(missing, "\n  "))
}
