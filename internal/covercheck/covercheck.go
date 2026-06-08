// Package covercheck computes diff (patch) coverage: of the source lines a
// change adds or modifies, the fraction that the test coverage profiles mark as
// covered. It is the testable core behind the cmd/covercheck gate (SRD-002) —
// it judges only changed lines, so the untouched-code coverage backlog never
// affects the result.
package covercheck

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

// Block is one coverage region of a Go coverage profile: the half-open source
// range [StartLine, EndLine] of a basic block, the number of statements in it,
// and how many times it executed (0 == not covered).
type Block struct {
	StartLine int
	EndLine   int
	NumStmts  int
	Count     int
}

// ParseProfiles reads one or more concatenated Go coverage profiles (the
// `go test -coverprofile` format) and returns the blocks per profile file path.
// Profile paths keep their module-import-path prefix (e.g.
// github.com/x/y/internal/z/f.go); matching to repo-relative paths is by suffix
// (see Evaluate). The leading `mode:` line(s) are ignored.
func ParseProfiles(r io.Reader) (map[string][]Block, error) {
	out := map[string][]Block{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "mode:") {
			continue
		}

		path, blk, ok := parseProfileLine(line)
		if !ok {
			continue
		}

		out[path] = append(out[path], blk)
	}

	return out, sc.Err()
}

// parseProfileLine parses one profile entry:
//
//	path/to/file.go:startLine.startCol,endLine.endCol numStmts count
func parseProfileLine(line string) (string, Block, bool) {
	fields := strings.Fields(line)
	if len(fields) != 3 {
		return "", Block{}, false
	}

	// fields[0] = path:sL.sC,eL.eC — split off the position range at the last
	// colon (Go import paths carry no colons).
	colon := strings.LastIndex(fields[0], ":")
	if colon < 0 {
		return "", Block{}, false
	}

	path := fields[0][:colon]
	rng := fields[0][colon+1:]

	start, end, ok := parseRange(rng)
	if !ok {
		return "", Block{}, false
	}

	numStmts, err1 := strconv.Atoi(fields[1])
	count, err2 := strconv.Atoi(fields[2])
	if err1 != nil || err2 != nil {
		return "", Block{}, false
	}

	return path, Block{
		StartLine: start,
		EndLine:   end,
		NumStmts:  numStmts,
		Count:     count,
	}, true
}

// parseRange parses "startLine.startCol,endLine.endCol" into start/end lines.
func parseRange(rng string) (int, int, bool) {
	comma := strings.Index(rng, ",")
	if comma < 0 {
		return 0, 0, false
	}

	start, ok1 := lineOf(rng[:comma])
	end, ok2 := lineOf(rng[comma+1:])
	if !ok1 || !ok2 {
		return 0, 0, false
	}

	return start, end, true
}

// lineOf extracts the line number from a "line.col" pair.
func lineOf(s string) (int, bool) {
	dot := strings.Index(s, ".")
	if dot < 0 {
		return 0, false
	}

	n, err := strconv.Atoi(s[:dot])
	if err != nil {
		return 0, false
	}

	return n, true
}

// ParseDiff parses `git diff --unified=0` output and returns the added/changed
// line numbers on the new (HEAD/working-tree) side, per repo-relative file path.
// Only the `+` side matters — those are the lines a change introduces.
func ParseDiff(r io.Reader) (map[string][]int, error) {
	out := map[string][]int{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	cur := ""

	for sc.Scan() {
		line := sc.Text()

		switch {
		case strings.HasPrefix(line, "+++ "):
			cur = newSidePath(line)

		case strings.HasPrefix(line, "@@ "):
			if cur == "" {
				continue
			}

			start, count, ok := parseHunkNewRange(line)
			if !ok {
				continue
			}

			for i := range count {
				out[cur] = append(out[cur], start+i)
			}
		}
	}

	return out, sc.Err()
}

// newSidePath extracts the repo-relative path from a `+++ b/path` header
// ("+++ /dev/null" for deletions yields "").
func newSidePath(header string) string {
	p := strings.TrimPrefix(header, "+++ ")
	if p == "/dev/null" {
		return ""
	}

	p = strings.TrimPrefix(p, "b/")
	// Drop a trailing tab-quoted suffix if git added one.
	if tab := strings.IndexByte(p, '\t'); tab >= 0 {
		p = p[:tab]
	}

	return p
}

// parseHunkNewRange parses the new-side range of a hunk header:
//
//	@@ -oldStart,oldCount +newStart,newCount @@ context
//
// A missing count means 1; a zero count (pure deletion) yields no added lines.
func parseHunkNewRange(header string) (int, int, bool) {
	plus := strings.IndexByte(header, '+')
	if plus < 0 {
		return 0, 0, false
	}

	rest := header[plus+1:]
	if sp := strings.IndexByte(rest, ' '); sp >= 0 {
		rest = rest[:sp]
	}

	startStr, countStr := rest, "1"
	if comma := strings.IndexByte(rest, ','); comma >= 0 {
		startStr = rest[:comma]
		countStr = rest[comma+1:]
	}

	start, err1 := strconv.Atoi(startStr)
	count, err2 := strconv.Atoi(countStr)
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}

	return start, count, true
}

// FileResult is the patch-coverage tally for one changed file.
type FileResult struct {
	Covered   int
	Coverable int
}

// Result is the overall patch-coverage outcome.
type Result struct {
	PerFile   map[string]FileResult
	Covered   int
	Coverable int
}

// Ratio returns the covered fraction of coverable changed lines in [0,1].
// With no coverable changed lines it returns 1 (nothing to cover ⇒ pass).
func (r Result) Ratio() float64 {
	if r.Coverable == 0 {
		return 1
	}

	return float64(r.Covered) / float64(r.Coverable)
}

// Evaluate intersects the changed lines with the profile blocks and computes
// patch coverage. A changed line counts as *coverable* only if it falls inside
// some profile block (statements); blank/comment/declaration lines are ignored.
// A coverable line is *covered* if any block covering it has Count > 0. Files
// for which exclude(path) is true are skipped. Profile paths are matched to the
// repo-relative changed paths by suffix.
func Evaluate(
	profiles map[string][]Block,
	changed map[string][]int,
	exclude func(string) bool,
) Result {
	res := Result{PerFile: map[string]FileResult{}}

	for file, lines := range changed {
		if exclude != nil && exclude(file) {
			continue
		}

		blocks, ok := matchProfile(profiles, file)
		if !ok {
			// No profile for the file (e.g. a package with no tests at all):
			// its changed statement lines are uncoverable-by-evidence. Treat
			// every changed line as coverable-but-uncovered so a wholly
			// untested new file fails the gate rather than passing silently.
			continue
		}

		fr := evalFile(blocks, lines)
		if fr.Coverable == 0 {
			continue
		}

		res.PerFile[file] = fr
		res.Covered += fr.Covered
		res.Coverable += fr.Coverable
	}

	return res
}

// evalFile tallies coverable/covered among the changed lines of one file.
func evalFile(blocks []Block, changed []int) FileResult {
	covered := map[int]bool{}
	coverable := map[int]bool{}

	for _, b := range blocks {
		for l := b.StartLine; l <= b.EndLine; l++ {
			coverable[l] = true
			if b.Count > 0 {
				covered[l] = true
			}
		}
	}

	var fr FileResult

	for _, l := range changed {
		if !coverable[l] {
			continue
		}

		fr.Coverable++

		if covered[l] {
			fr.Covered++
		}
	}

	return fr
}

// matchProfile finds the profile blocks whose path corresponds to the
// repo-relative changed file, by suffix ("…/<repoPath>").
func matchProfile(profiles map[string][]Block, repoPath string) ([]Block, bool) {
	suffix := "/" + repoPath

	for p, blocks := range profiles {
		if p == repoPath || strings.HasSuffix(p, suffix) {
			return blocks, true
		}
	}

	return nil, false
}

// DefaultExcluded reports whether a repo-relative path is outside the gate's
// scope: tests, generated mocks, examples, and CLI entry points.
func DefaultExcluded(path string) bool {
	switch {
	case strings.HasSuffix(path, "_test.go"):
		return true
	case strings.HasPrefix(path, "generated/"),
		strings.Contains(path, "/generated/"):
		return true
	case strings.HasPrefix(path, "examples/"):
		return true
	case strings.HasPrefix(path, "cmd/"):
		return true
	default:
		return false
	}
}

// --- gate orchestration (the logic behind cmd/covercheck) ---

// RunGate fetches the committed diff (base...HEAD) and the coverage profiles,
// then delegates to Gate. It writes the report to w and returns the process exit
// code: 1 if below minPct, 0 otherwise.
func RunGate(w io.Writer, minPct float64, base, profilesCSV string) (int, error) {
	diff, err := GitDiff(base)
	if err != nil {
		return 0, err
	}

	profiles, err := ReadProfiles(splitList(profilesCSV))
	if err != nil {
		return 0, err
	}

	return Gate(w, minPct, diff, profiles)
}

// Gate computes patch coverage from an already-fetched unified=0 diff and
// coverage profiles, writes the report to w, and returns the exit code (1 if
// below minPct). Separated from the git/file I/O so it is unit-testable.
func Gate(
	w io.Writer,
	minPct float64,
	diff string,
	profiles map[string][]Block,
) (int, error) {
	changed, err := ParseDiff(strings.NewReader(diff))
	if err != nil {
		return 0, err
	}

	res := Evaluate(profiles, changed, DefaultExcluded)
	Report(w, res, minPct)

	if res.Ratio()*100 < minPct {
		return 1, nil
	}

	return 0, nil
}

// GitDiff returns the unified=0 Go-file diff from merge-base(base, HEAD) to HEAD
// — the committed changes the branch introduces. Committed state (not the
// working tree) keeps local `make cover-check` and CI in lockstep. The
// `base...HEAD` form resolves the merge-base itself.
func GitDiff(base string) (string, error) {
	spec := base + "...HEAD"

	// #nosec G204 -- base is a trusted CLI flag (a git ref), not external input.
	out, err := exec.Command("git", "diff", "--unified=0", spec, "--", "*.go").
		Output()
	if err != nil {
		return "", fmt.Errorf("git diff %s: %w", spec, err)
	}

	return string(out), nil
}

// ReadProfiles parses and merges the coverage profiles at the given paths. It
// errors if none exist — running the gate with no profile must fail loudly
// (run `make test-all` first), not pass vacuously.
func ReadProfiles(paths []string) (map[string][]Block, error) {
	merged := map[string][]Block{}
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

		blocks, err := ParseProfiles(f)
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

// Report writes the per-file and total patch coverage to w.
func Report(w io.Writer, res Result, minPct float64) {
	files := make([]string, 0, len(res.PerFile))
	for f := range res.PerFile {
		files = append(files, f)
	}

	sort.Strings(files)

	for _, f := range files {
		fr := res.PerFile[f]
		_, _ = fmt.Fprintf(w, "  %6.1f%%  %s (%d/%d changed lines)\n",
			pct(fr.Covered, fr.Coverable), f, fr.Covered, fr.Coverable)
	}

	verdict := "PASS"
	if res.Ratio()*100 < minPct {
		verdict = "FAIL"
	}

	_, _ = fmt.Fprintf(w, "diff-coverage: %.1f%% of %d changed coverable lines "+
		"(min %.0f%%) — %s\n", res.Ratio()*100, res.Coverable, minPct, verdict)
}

// pct is the percentage covered/coverable (100 when nothing is coverable).
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
