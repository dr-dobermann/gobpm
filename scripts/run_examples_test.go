package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// writeModule lays down a throwaway Go module whose main behaves as told:
// exit 0, exit with a status, or block forever.
func writeModule(t *testing.T, root, name, body string) string {
	t.Helper()

	dir := filepath.Join(root, name)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"),
		[]byte("module x\n\ngo 1.25\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.go"),
		[]byte(body), 0o644))

	return dir
}

// TestRunExamplesScript exercises scripts/run-examples.sh the way the gate
// does — several modules at once — on a fixture with one passing, one
// failing and one hanging module (SRD-094 M7): the folds come out in the
// given order, a failure prints its log and status inside its fold, a hang
// is cut at the ceiling and named, the summary counts both, and the exit
// code is 1. A second run with only passing modules exits 0.
func TestRunExamplesScript(t *testing.T) {
	timeoutCmd, err := exec.LookPath("timeout")
	if err != nil {
		timeoutCmd, err = exec.LookPath("gtimeout")
	}

	if err != nil {
		t.Skip("GNU timeout is not installed")
	}

	script, err := filepath.Abs("run-examples.sh")
	require.NoError(t, err)

	root := t.TempDir()
	good := writeModule(t, root, "good", "package main\n\nfunc main() {}\n")
	bad := writeModule(t, root, "bad",
		"package main\n\nimport (\n\t\"fmt\"\n\t\"os\"\n)\n\n"+
			"func main() { fmt.Println(\"boom-out\"); os.Exit(3) }\n")
	hang := writeModule(t, root, "hang",
		"package main\n\nimport \"time\"\n\n"+
			"func main() { time.Sleep(time.Hour) }\n")

	run := func(ceiling string, dirs ...string) (string, int) {
		t.Helper()

		cmd := exec.Command(script, dirs...)
		cmd.Env = append(os.Environ(),
			"EXAMPLE_TIMEOUT="+timeoutCmd,
			"EXAMPLE_RUN_TIMEOUT="+ceiling,
			"EXAMPLE_JOBS=3")

		out, runErr := cmd.CombinedOutput()

		code := 0

		var exitErr *exec.ExitError
		if runErr != nil {
			require.ErrorAs(t, runErr, &exitErr)
			code = exitErr.ExitCode()
		}

		return string(out), code
	}

	// A generous ceiling for the pass/fail pair: a cold `go run` compile
	// must not be mistaken for a hang.
	t.Run("pass and fail", func(t *testing.T) {
		out, code := run("120s", good, bad)
		require.Equal(t, 1, code, out)

		require.Regexp(t, regexp.MustCompile(
			`(?s)::group::run `+regexp.QuoteMeta(good)+`\n::endgroup::\n`+
				`::group::run `+regexp.QuoteMeta(bad)+`\n.*boom-out.*`+
				`run-examples: `+regexp.QuoteMeta(bad)+` exited 1\n::endgroup::`),
			out, "folds in the given order; the failure's log inside its fold")
		require.Contains(t, out, "run-examples: 1 ok, 1 failed (jobs=3)")
	})

	t.Run("a hang is cut and named", func(t *testing.T) {
		// Build once so the short ceiling below measures the run, not
		// the compile.
		build := exec.Command("go", "build", "-o", os.DevNull, ".")
		build.Dir = hang
		require.NoError(t, build.Run())

		out, code := run("3s", hang)
		require.Equal(t, 1, code, out)
		require.Contains(t, out, "run-examples: "+hang+" timed out after 3s")
		require.Contains(t, out, "run-examples: 0 ok, 1 failed")
	})

	t.Run("all passing exits 0", func(t *testing.T) {
		out, code := run("120s", good)
		require.Equal(t, 0, code, out)
		require.True(t, strings.HasSuffix(strings.TrimSpace(out),
			"run-examples: 1 ok, 0 failed (jobs=3)"), out)
	})

	t.Run("no dirs is a usage error", func(t *testing.T) {
		out, code := run("3s")
		require.Equal(t, 64, code, out)
	})
}
