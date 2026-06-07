// Command covercheck is the diff-coverage gate (SRD-002): it fails when the
// source lines a change adds or modifies are covered below a threshold, judging
// only changed lines so the untouched-code coverage backlog never blocks it.
//
// Usage:
//
//	covercheck -min 70 -base origin/master -profiles "coverage.txt,runtime/coverage.txt"
//
// It diffs the working tree against the merge-base with -base, reads the given
// Go coverage profiles, and reports patch coverage. Exit code 1 if below -min.
// CLI wiring only; the logic lives in internal/covercheck (unit-tested).
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"

	"github.com/dr-dobermann/gobpm/internal/covercheck"
)

func main() {
	minPct := flag.Float64("min", 70, "minimum patch coverage percent")
	base := flag.String("base", "origin/master", "base ref to diff against")
	profiles := flag.String("profiles", "coverage.txt",
		"comma-separated coverage profile paths")
	flag.Parse()

	code, err := run(*minPct, *base, splitList(*profiles))
	if err != nil {
		fmt.Fprintln(os.Stderr, "covercheck:", err)
		os.Exit(2)
	}

	os.Exit(code)
}

// run computes patch coverage and returns the process exit code.
func run(minPct float64, base string, profilePaths []string) (int, error) {
	changedText, err := gitDiff(base)
	if err != nil {
		return 0, err
	}

	changed, err := covercheck.ParseDiff(strings.NewReader(changedText))
	if err != nil {
		return 0, err
	}

	profiles, err := readProfiles(profilePaths)
	if err != nil {
		return 0, err
	}

	res := covercheck.Evaluate(profiles, changed, covercheck.DefaultExcluded)
	report(res, minPct)

	if res.Ratio()*100 < minPct {
		return 1, nil
	}

	return 0, nil
}

// gitDiff returns the unified=0 Go-file diff from merge-base(base, HEAD) to
// HEAD — i.e. the committed changes this branch introduces. Using committed
// state (not the working tree) keeps local `make cover-check` and CI in lockstep
// (CI runs on the committed PR head). The `base...HEAD` form resolves the
// merge-base itself.
func gitDiff(base string) (string, error) {
	spec := base + "...HEAD"

	// #nosec G204 -- base is a trusted CLI flag (a git ref), not external input.
	out, err := exec.Command("git", "diff", "--unified=0", spec, "--", "*.go").
		Output()
	if err != nil {
		return "", fmt.Errorf("git diff %s: %w", spec, err)
	}

	return string(out), nil
}

// readProfiles parses and merges the given coverage profiles. It errors if none
// of the paths exist — that means the gate ran without a coverage profile (run
// `make test-all` first), and silently passing would defeat the gate.
func readProfiles(paths []string) (map[string][]covercheck.Block, error) {
	merged := map[string][]covercheck.Block{}
	found := false

	for _, p := range paths {
		// #nosec G304 -- p is a trusted CLI flag (a coverage-profile path).
		f, err := os.Open(p)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}

			return nil, err
		}

		found = true

		blocks, err := covercheck.ParseProfiles(f)
		_ = f.Close()

		if err != nil {
			return nil, err
		}

		for k, v := range blocks {
			merged[k] = append(merged[k], v...)
		}
	}

	if !found {
		return nil, fmt.Errorf(
			"no coverage profile found among %v — run `make test-all` first",
			paths)
	}

	return merged, nil
}

// report prints the per-file and total patch coverage.
func report(res covercheck.Result, minPct float64) {
	files := make([]string, 0, len(res.PerFile))
	for f := range res.PerFile {
		files = append(files, f)
	}

	sort.Strings(files)

	for _, f := range files {
		fr := res.PerFile[f]
		fmt.Printf("  %6.1f%%  %s (%d/%d changed lines)\n",
			pct(fr.Covered, fr.Coverable), f, fr.Covered, fr.Coverable)
	}

	verdict := "PASS"
	if res.Ratio()*100 < minPct {
		verdict = "FAIL"
	}

	fmt.Printf("diff-coverage: %.1f%% of %d changed coverable lines "+
		"(min %.0f%%) — %s\n",
		res.Ratio()*100, res.Coverable, minPct, verdict)
}

func pct(covered, coverable int) float64 {
	if coverable == 0 {
		return 100
	}

	return float64(covered) / float64(coverable) * 100
}

// splitList splits a comma-separated list, dropping empties and whitespace.
func splitList(s string) []string {
	var out []string

	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}

	return out
}
