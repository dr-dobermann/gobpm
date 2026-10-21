package lintcfg

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// forbiddenInWorkflow matches what a workflow line may not contain once its
// comment is stripped: an install command, or a v-prefixed X.Y.Z version.
var forbiddenInWorkflow = regexp.MustCompile(
	`go install|pip install|install\.sh|\bv[0-9]+\.[0-9]+\.[0-9]+`)

// workflowViolations returns "name:line: text" for every line of a workflow
// that matches forbiddenInWorkflow. A comment runs from '#' to the end of the
// line and is dropped first, so the release note dependabot keeps after a
// SHA-pinned action (`uses: x@<sha> # v7.0.1`) is not mistaken for a pin.
func workflowViolations(name, content string) []string {
	var found []string

	for i, line := range strings.Split(content, "\n") {
		code, _, _ := strings.Cut(line, "#")
		if forbiddenInWorkflow.MatchString(code) {
			found = append(found, fmt.Sprintf("%s:%d: %s",
				name, i+1, strings.TrimSpace(line)))
		}
	}

	return found
}

// TestWorkflowViolations pins the rule itself on synthetic lines, so
// TestWorkflowsInstallThroughMake cannot pass merely because the rule went
// blind.
func TestWorkflowViolations(t *testing.T) {
	tests := []struct {
		name    string
		line    string
		flagged bool
	}{
		{"go install command", "  run: go install example.com/tool@latest", true},
		{"version in env", "    TOOL_VERSION: v1.2.3", true},
		{"version in action input", "    version: v2.11.4", true},
		{"pip install command", "  run: pip install pkg==1.0.0", true},
		{"installer script", "  run: curl -sSfL https://x/v2/install.sh | sh", true},
		{"release note after a pinned action",
			"  - uses: actions/checkout@3d3c42e # v7.0.1", false},
		{"whole-line comment", "  # go install example.com/tool@v1.2.3", false},
		{"install through make", "  run: make tools", false},
		{"toolchain version without v", "    go-version: '1.26.9'", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := workflowViolations("w.yml", tt.line)
			if tt.flagged {
				require.Equal(t,
					[]string{"w.yml:1: " + strings.TrimSpace(tt.line)}, got)
			} else {
				require.Empty(t, got)
			}
		})
	}

	t.Run("reports every violating line by its own number", func(t *testing.T) {
		content := "steps:\n" +
			"  - run: go install example.com/a@latest\n" +
			"  - run: make tools\n" +
			"    env: {V: v1.2.3}\n"

		require.Equal(t, []string{
			"w.yml:2: - run: go install example.com/a@latest",
			"w.yml:4: env: {V: v1.2.3}",
		}, workflowViolations("w.yml", content))
	})
}

// jobKey matches a job's own key line under `jobs:` — two spaces of indent,
// then the job id.
var jobKey = regexp.MustCompile(`^  ([A-Za-z0-9_-]+):\s*$`)

// makeRunsByJob counts, per job, the `run: make <target>` lines of a
// workflow. Comments are stripped first, so a commented-out install step does
// not count. A job runs from its key line to the next one, and the `jobs:`
// block ends at the next unindented key.
func makeRunsByJob(content, target string) map[string]int {
	run := regexp.MustCompile(
		`\brun:\s*make ` + regexp.QuoteMeta(target) + `\s*$`)

	counts := map[string]int{}

	inJobs, job := false, ""

	for _, line := range strings.Split(content, "\n") {
		code, _, _ := strings.Cut(line, "#")

		switch {
		case strings.TrimSpace(code) == "":
			continue
		case code == "jobs:":
			inJobs = true
			continue
		case !strings.HasPrefix(code, " "):
			inJobs, job = false, ""
			continue
		}

		if !inJobs {
			continue
		}

		if m := jobKey.FindStringSubmatch(code); m != nil {
			job = m[1]
			counts[job] = 0

			continue
		}

		if job != "" && run.MatchString(code) {
			counts[job]++
		}
	}

	return counts
}

// TestMakeRunsByJob pins the per-job count on a synthetic workflow, including
// the case a per-file count missed: both installs inside one job.
func TestMakeRunsByJob(t *testing.T) {
	content := "on:\n" +
		"  push:\n" +
		"jobs:\n" +
		"  check:\n" +
		"    steps:\n" +
		"      - run: make tools\n" +
		"      - run: make tools\n" +
		"  examples:\n" +
		"    steps:\n" +
		"      # - run: make tools\n" +
		"      - run: make tools-lint\n" +
		"permissions:\n" +
		"  - run: make tools\n"

	require.Equal(t, map[string]int{"check": 2, "examples": 0},
		makeRunsByJob(content, "tools"))
}

// TestWorkflowsInstallThroughMake keeps the Makefile the only place a tool
// pin is written. It fails on a workflow line that installs a tool itself, or
// that names a version in any other form (an action's `with: version:`, an
// `env:` entry), because either one is a second pin the weekly bump cannot
// reach.
//
// The pins used to live in two places — the Makefile and the workflow's own
// install step — and a test kept the two in step. That arrangement broke the
// bump outright: it had to edit a workflow file, and the GITHUB_TOKEN it
// pushes with may never do that (FIX-042). So the workflows now install
// through `make tools` / `make docs-tools`.
func TestWorkflowsInstallThroughMake(t *testing.T) {
	dir := filepath.Join(repoRoot(t), ".github", "workflows")

	paths, err := filepath.Glob(filepath.Join(dir, "*.yml"))
	require.NoError(t, err)

	files := make(map[string]string, len(paths))

	var violations []string

	for _, p := range paths {
		b, err := os.ReadFile(p)
		require.NoError(t, err)

		name := filepath.Base(p)
		files[name] = string(b)
		violations = append(violations, workflowViolations(name, string(b))...)
	}

	// A guard that finds nothing and passes is worse than no guard: it reports
	// success while measuring an empty set. Every job that needs tools must
	// install them through make exactly once — check.yml's `check` and
	// `examples`, docs.yml's `build` — so a moved directory, or one job's
	// install step dropped, commented out or moved into another job, fails
	// here instead.
	for _, want := range []struct {
		file, target string
		jobs         []string
	}{
		{"check.yml", "tools", []string{"check", "examples"}},
		{"docs.yml", "docs-tools", []string{"build"}},
	} {
		content, ok := files[want.file]
		require.True(t, ok,
			"%s not found in %s — have the workflows moved?", want.file, dir)

		counts := makeRunsByJob(content, want.target)
		for _, job := range want.jobs {
			require.Equal(t, 1, counts[job],
				"%s job %q should run `make %s` exactly once",
				want.file, job, want.target)
		}
	}

	require.Empty(t, violations,
		"these workflow lines install a tool or name a version themselves; tool "+
			"pins live only in the Makefile, and workflows install through "+
			"`make tools` / `make docs-tools` (FIX-042):\n  %s",
		strings.Join(violations, "\n  "))
}
