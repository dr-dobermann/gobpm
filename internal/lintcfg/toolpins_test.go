package lintcfg

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// stripComment drops a YAML comment from one line. A '#' starts a comment
// only outside quotes and at the start of the line or after whitespace, as
// YAML has it, so `echo "# x" && go install …` keeps its install command and
// `uses: x@<sha> # v7.0.1` loses the release note dependabot keeps there.
func stripComment(line string) string {
	var quote rune

	for i, r := range line {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			}
		case r == '\'' || r == '"':
			quote = r
		case r == '#' && (i == 0 || line[i-1] == ' ' || line[i-1] == '\t'):
			return line[:i]
		}
	}

	return line
}

// activeLines returns a workflow's lines with comments stripped and
// surrounding space trimmed, so a commented-out step reads as absent.
func activeLines(content string) []string {
	lines := strings.Split(content, "\n")

	for i, line := range lines {
		lines[i] = strings.TrimSpace(stripComment(line))
	}

	return lines
}

// readWorkflows returns every workflow file by base name. GitHub runs
// `.yaml` files exactly as it runs `.yml` ones, so both are read.
func readWorkflows(t *testing.T) map[string]string {
	t.Helper()

	dir := filepath.Join(repoRoot(t), ".github", "workflows")
	files := map[string]string{}

	for _, ext := range []string{"*.yml", "*.yaml"} {
		paths, err := filepath.Glob(filepath.Join(dir, ext))
		require.NoError(t, err)

		for _, p := range paths {
			b, err := os.ReadFile(p)
			require.NoError(t, err)

			files[filepath.Base(p)] = string(b)
		}
	}

	return files
}

// makefilePin matches a `NAME_VERSION := value` pin and, loosely, the
// malformed forms (`=` instead of `:=`) TestPinBotCoversEveryPin rejects.
var makefilePin = regexp.MustCompile(
	`(?m)^([A-Z][A-Z0-9_]*_VERSION)[ \t]*:?=[ \t]*(\S*)`)

// makefilePins returns the Makefile's pin values, the literals no workflow
// may repeat.
func makefilePins(t *testing.T) []string {
	t.Helper()

	b, err := os.ReadFile(filepath.Join(repoRoot(t), "Makefile"))
	require.NoError(t, err)

	var values []string

	for _, m := range makefilePin.FindAllStringSubmatch(string(b), -1) {
		values = append(values, m[2])
	}

	return values
}

// forbiddenInWorkflow matches what a workflow line may not contain once its
// comment is stripped: a command that installs a tool itself, or a v-prefixed
// X.Y.Z version. Actions are pinned by SHA, so a vX.Y.Z outside a comment is
// a version pin, whatever key it sits under.
var forbiddenInWorkflow = regexp.MustCompile(
	`\bgo\s+install\b|\bgo\s+run\s+\S+@|` +
		`\b(pip[0-9.]*|pipx|uv)\b.*\binstall\b|install\.sh|` +
		`\bv[0-9]+\.[0-9]+\.[0-9]+`)

// workflowViolations returns "name:line: text" for every line of a workflow
// that matches forbiddenInWorkflow or names one of pins as a whole word — the
// latter catches a pin with no `v` in front, like the mkdocs ones.
func workflowViolations(name, content string, pins []string) []string {
	literal := make([]*regexp.Regexp, 0, len(pins))
	for _, p := range pins {
		literal = append(literal, regexp.MustCompile(
			`(^|[^\w.])`+regexp.QuoteMeta(p)+`($|[^\w.])`))
	}

	var found []string

	for i, line := range strings.Split(content, "\n") {
		code := stripComment(line)

		bad := forbiddenInWorkflow.MatchString(code)
		for _, re := range literal {
			bad = bad || re.MatchString(code)
		}

		if bad {
			found = append(found, fmt.Sprintf("%s:%d: %s",
				name, i+1, strings.TrimSpace(line)))
		}
	}

	return found
}

// TestStripComment pins the comment rule the other workflow helpers share.
func TestStripComment(t *testing.T) {
	for line, want := range map[string]string{
		"  # whole line":                    "  ",
		"  - uses: a@3d3c42e # v7.0.1":      "  - uses: a@3d3c42e ",
		`  run: echo "# x" && go install a`: `  run: echo "# x" && go install a`,
		"  run: echo 'a # b' # c":           "  run: echo 'a # b' ",
		"  url: https://x/y#anchor":         "  url: https://x/y#anchor",
		"jobs:":                             "jobs:",
	} {
		require.Equal(t, want, stripComment(line), "line %q", line)
	}
}

// TestWorkflowViolations pins the rule itself on synthetic lines, so
// TestWorkflowsInstallThroughMake cannot pass merely because the rule went
// blind.
func TestWorkflowViolations(t *testing.T) {
	pins := []string{"9.7.7", "v2.11.4"}

	tests := []struct {
		name    string
		line    string
		flagged bool
	}{
		{"go install command", "  run: go install example.com/tool@latest", true},
		{"go run at a version", "  run: go run example.com/tool@latest", true},
		{"version in env", "    TOOL_VERSION: v1.2.3", true},
		{"version in action input", "    version: v2.11.4", true},
		{"unprefixed pin in env", "    MKDOCS_MATERIAL_VERSION: 9.7.7", true},
		{"pip install command", "  run: pip install pkg==1.0.0", true},
		{"pip3 install command", "  run: pip3 install pkg", true},
		{"python -m pip install", "  run: python3 -m pip -q install pkg", true},
		{"pipx install", "  run: pipx install pkg", true},
		{"uv tool install", "  run: uv tool install pkg", true},
		{"installer script", "  run: curl -sSfL https://x/v2/install.sh | sh", true},
		{"install hidden after a quoted hash",
			`  run: echo "# x" && go install a@latest`, true},
		{"release note after a pinned action",
			"  - uses: actions/checkout@3d3c42e # v7.0.1", false},
		{"whole-line comment", "  # go install example.com/tool@v1.2.3", false},
		{"install through make", "  run: make tools", false},
		{"go run of a local package", "  run: go run ./cmd/x", false},
		{"pin as part of a longer version", "    image: x:19.7.70", false},
		{"toolchain read from go.mod", "    go-version-file: go.mod", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := workflowViolations("w.yml", tt.line, pins)
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
		}, workflowViolations("w.yml", content, nil))
	})
}

// jobKey matches a job's own key line under `jobs:` — two spaces of indent,
// then the job id.
var jobKey = regexp.MustCompile(`^  ([A-Za-z0-9_-]+):$`)

// makeRunsByJob counts, per job, the `run: make <target>` steps of a
// workflow. Comments and trailing space are stripped first, so a
// commented-out install step does not count and a `jobs: # …` header still
// opens the block. A job runs from its key line to the next one, and the
// `jobs:` block ends at the next unindented key.
func makeRunsByJob(content, target string) map[string]int {
	run := regexp.MustCompile(`^\s*(-\s*)?run:\s*["']?make ` +
		regexp.QuoteMeta(target) + `["']?$`)

	counts := map[string]int{}

	inJobs, job := false, ""

	for _, line := range strings.Split(content, "\n") {
		code := strings.TrimRight(stripComment(line), " \t\r")

		switch {
		case code == "":
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

// TestMakeRunsByJob pins the per-job count on synthetic workflows: both
// installs inside one job, a commented-out install, a `jobs:` header with a
// comment, trailing space or CRLF, a quoted command, and a `run:` that is
// not a step key.
func TestMakeRunsByJob(t *testing.T) {
	content := "on:\n" +
		"  push:\n" +
		"jobs: # CI\r\n" +
		"  check:  \r\n" +
		"    steps:\n" +
		"      - run: make tools\n" +
		"      - name: x\n" +
		"        run: \"make tools\"\n" +
		"  examples:\n" +
		"    steps:\n" +
		"      # - run: make tools\n" +
		"      - run: make tools-lint\n" +
		"      - name: dry run: make tools\n" +
		"permissions:\n" +
		"  - run: make tools\n"

	require.Equal(t, map[string]int{"check": 2, "examples": 0},
		makeRunsByJob(content, "tools"))
}

// TestWorkflowsInstallThroughMake keeps the Makefile the only place a tool
// pin is written. It fails on a workflow line that installs a tool itself, or
// that names a version in any other form (an action's `with: version:`, an
// `env:` entry, a Makefile pin value), because each is a second pin the weekly
// bump cannot reach.
//
// The pins used to live in two places — the Makefile and the workflow's own
// install step — and a test kept the two in step. That arrangement broke the
// bump outright: it had to edit a workflow file, and the GITHUB_TOKEN it
// pushes with may never do that (FIX-042). So the workflows now install
// through `make tools` / `make docs-tools`.
func TestWorkflowsInstallThroughMake(t *testing.T) {
	files := readWorkflows(t)
	pins := makefilePins(t)

	var violations []string

	for name, content := range files {
		violations = append(violations, workflowViolations(name, content, pins)...)
	}

	slices.Sort(violations)

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
		require.True(t, ok, "%s not found — have the workflows moved?", want.file)

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

// botTools matches one entry of check-tool-pins.sh's `tools` table, capturing
// the Makefile variable it bumps.
var botTools = regexp.MustCompile(
	`(?m)^\s*"[^"|]+\|([A-Z][A-Z0-9_]*_VERSION)\|`)

// wellFormedPin is the one pin form check-tool-pins.sh's sed rewrites.
var wellFormedPin = regexp.MustCompile(
	`^[A-Z][A-Z0-9_]*_VERSION[ \t]*:=[ \t]*\S+$`)

// pinTableProblems compares a Makefile's pins with a check-tool-pins.sh
// `tools` table, and returns each pin line in a form the script's sed cannot
// rewrite, each pin the table misses, and each table entry with no pin.
func pinTableProblems(makefile, script string) []string {
	var problems []string

	pinned := map[string]bool{}

	for _, line := range strings.Split(makefile, "\n") {
		m := makefilePin.FindStringSubmatch(line)
		if m == nil {
			continue
		}

		pinned[m[1]] = true

		if !wellFormedPin.MatchString(line) {
			problems = append(problems, "malformed pin: "+line)
		}
	}

	listed := map[string]bool{}
	for _, m := range botTools.FindAllStringSubmatch(script, -1) {
		listed[m[1]] = true
	}

	for v := range pinned {
		if !listed[v] {
			problems = append(problems, "not in the tools table: "+v)
		}
	}

	for v := range listed {
		if !pinned[v] {
			problems = append(problems, "not pinned in the Makefile: "+v)
		}
	}

	slices.Sort(problems)

	return problems
}

// TestPinTableProblems pins the comparison on synthetic files.
func TestPinTableProblems(t *testing.T) {
	script := `tools=(
	"a|A_VERSION|example.com/a"
	"b|B_VERSION|pypi:b"
	"c|C_VERSION|example.com/c"
)`

	require.Empty(t, pinTableProblems(
		"A_VERSION := v1.0.0\nB_VERSION    := 2.0.0\nC_VERSION:=v3.0.0\n",
		script))

	require.Equal(t, []string{
		"malformed pin: A_VERSION = v1.0.0",
		"malformed pin: B_VERSION := 2.0.0 # pinned",
		"not in the tools table: D_VERSION",
		"not pinned in the Makefile: C_VERSION",
	}, pinTableProblems(
		"A_VERSION = v1.0.0\nB_VERSION := 2.0.0 # pinned\nD_VERSION := v4.0.0\n",
		script))
}

// TestPinBotCoversEveryPin keeps the Makefile's pins and the weekly bump's
// tool table in step. A pin the table does not list is never bumped, and
// the table cannot be derived from the Makefile because it also carries each
// tool's upstream module. Every pin must also be a `NAME := value` line, the
// one form the script's sed rewrites — on any other it substitutes nothing.
func TestPinBotCoversEveryPin(t *testing.T) {
	root := repoRoot(t)

	mk, err := os.ReadFile(filepath.Join(root, "Makefile"))
	require.NoError(t, err)

	script, err := os.ReadFile(
		filepath.Join(root, "scripts", "check-tool-pins.sh"))
	require.NoError(t, err)

	// Five Go tools and two docs packages today; fewer means a parser broke.
	pins := makefilePin.FindAllString(string(mk), -1)
	require.GreaterOrEqual(t, len(pins), 5, "found pins %v", pins)

	problems := pinTableProblems(string(mk), string(script))
	require.Empty(t, problems,
		"the Makefile's *_VERSION pins and scripts/check-tool-pins.sh's `tools` "+
			"table must name the same variables, each pin as `NAME := value`:\n  %s",
		strings.Join(problems, "\n  "))
}

// dispatchProblems checks a bot workflow's comment-stripped lines for what
// gets its bump PR a CI run: a single permissions block granting
// `actions: write` (a job-level block would replace it), the branch pushed
// before the PR is opened, and each of dispatched — dispatched after the PR
// and from the step that pushed, where `$branch` is set.
func dispatchProblems(lines []string, dispatched ...string) []string {
	var problems []string

	blocks := 0

	for _, line := range lines {
		if strings.HasPrefix(line, "permissions:") {
			blocks++
		}
	}

	if blocks != 1 {
		problems = append(problems, fmt.Sprintf(
			"%d permissions blocks; a job-level one replaces the workflow's", blocks))
	}

	if !slices.Contains(lines, "actions: write") {
		problems = append(problems, "no `actions: write` for `gh workflow run`")
	}

	push := slices.Index(lines, `git push -u origin "$branch"`)
	pr := slices.Index(lines, `gh pr create \`)

	switch {
	case push < 0 || pr < 0:
		return append(problems, "lost the `git push` or `gh pr create` line")
	case pr < push:
		problems = append(problems, "the PR is opened before the branch is pushed")
	}

	for _, wf := range dispatched {
		d := slices.Index(lines, `gh workflow run `+wf+` --ref "$branch"`)

		switch {
		case d < 0:
			problems = append(problems, wf+" is not dispatched")
		case d < pr:
			problems = append(problems, wf+" is dispatched before the PR is opened")
		case slices.ContainsFunc(lines[push:d], func(l string) bool {
			return strings.HasPrefix(l, "- ")
		}):
			problems = append(problems,
				wf+" is dispatched from a later step than the push")
		}
	}

	return problems
}

// TestDispatchProblems pins the dispatch contract on synthetic workflows.
func TestDispatchProblems(t *testing.T) {
	good := []string{
		"permissions:", "contents: write", "actions: write",
		"jobs:", "update:", "steps:", "- name: Open a bump PR", "run: |",
		`git push -u origin "$branch"`, `gh pr create \`, `--title "x"`,
		`gh workflow run check.yml --ref "$branch"`,
	}
	require.Empty(t, dispatchProblems(good, "check.yml"))

	edit := func(f func([]string) []string) []string {
		return f(slices.Clone(good))
	}

	for name, tt := range map[string]struct {
		lines []string
		want  string
	}{
		"job-level permissions": {edit(func(l []string) []string {
			return slices.Insert(l, 4, "permissions:")
		}), "2 permissions blocks; a job-level one replaces the workflow's"},
		"no actions: write": {edit(func(l []string) []string {
			return slices.Delete(l, 2, 3)
		}), "no `actions: write` for `gh workflow run`"},
		"PR before push": {edit(func(l []string) []string {
			l[8], l[9] = l[9], l[8]
			return l
		}), "the PR is opened before the branch is pushed"},
		"dispatch from a later step": {edit(func(l []string) []string {
			return slices.Insert(l, 11, "- name: dispatch", "run: |")
		}), "check.yml is dispatched from a later step than the push"},
		"dispatch missing": {edit(func(l []string) []string {
			return l[:11]
		}), "check.yml is not dispatched"},
	} {
		require.Contains(t, dispatchProblems(tt.lines, "check.yml"), tt.want, name)
	}
}

// TestBotDispatchesCI pins the contract that gets the bot's bump PR its CI. A
// PR opened with GITHUB_TOKEN starts no pull_request run, so tool-pins.yml
// dispatches check.yml and docs.yml on the bump branch itself (FIX-042). That
// needs a `workflow_dispatch` trigger on both targets and the dispatchProblems
// contract in tool-pins.yml. Dropping any part of it leaves the PR without its
// required check, silently — the weekly run itself still goes green.
func TestBotDispatchesCI(t *testing.T) {
	files := readWorkflows(t)

	for _, name := range []string{"check.yml", "docs.yml"} {
		require.Contains(t, activeLines(files[name]), "workflow_dispatch:",
			"%s must accept workflow_dispatch, or the bot cannot run it", name)
	}

	problems := dispatchProblems(activeLines(files["tool-pins.yml"]),
		"check.yml", "docs.yml")
	require.Empty(t, problems, "tool-pins.yml:\n  %s",
		strings.Join(problems, "\n  "))
}
