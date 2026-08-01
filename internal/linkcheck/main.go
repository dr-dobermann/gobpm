// Package linkcheck verifies that every relative Markdown link in a directory
// tree resolves to a file that exists.
//
// It exists because nothing did: FIX-031 swept up 78 dead cross-references that
// had accumulated across several refactors, including in both READMEs and the
// SAD, and two of its three causes — a retired document and a renamed ADR — are
// exactly what a checker catches for free (FIX-034 §1.3).
//
// It is deliberately a small Go package rather than an off-the-shelf tool: the
// repository's parity rule requires every CI tool to be pinned and installed by
// `make tools`, and adding a Node or Rust toolchain for this would also add a
// network dependency that makes the gate flaky for reasons unrelated to the
// change under test. Relative links only, offline, deterministic.
package linkcheck

import (
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// problem is one link that does not resolve.
type problem struct {
	file   string
	target string
	line   int
}

// skipDir names directories that hold no authored documentation.
var skipDir = map[string]bool{
	".git":         true,
	"node_modules": true,
	"bin":          true,
}

// Run is the command's body with its I/O and exit code as values, so the
// tool's behavior — what it prints, and which of its three exit codes it
// chooses — is testable without spawning a process, and is measured by the
// coverage gate. 0 clean, 1 dead links, 2 unusable root.
func Run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("linkcheck", flag.ContinueOnError)
	flags.SetOutput(stderr)

	root := flags.String("root", ".", "directory to scan")

	if err := flags.Parse(args); err != nil {
		return 2
	}

	problems, err := scan(*root)
	if err != nil {
		emitf(stderr, "linkcheck: %v\n", err)

		return 2
	}

	for _, p := range problems {
		emitf(stdout, "%s:%d: dead link: %s\n", p.file, p.line, p.target)
	}

	if len(problems) > 0 {
		emitf(stderr, "\nlinkcheck: %d dead link(s)\n", len(problems))

		return 1
	}

	emitf(stdout, "linkcheck: every relative link resolves\n")

	return 0
}

// scan walks root's Markdown and returns the links that do not resolve, in a
// stable order so the output is diffable.
func scan(root string) ([]problem, error) {
	var out []problem

	pats, err := newPatterns()
	if err != nil {
		return nil, err
	}

	err = filepath.WalkDir(root, func(
		path string, d fs.DirEntry, err error,
	) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			if skipDir[d.Name()] {
				return fs.SkipDir
			}

			return nil
		}

		if !strings.EqualFold(filepath.Ext(path), ".md") {
			return nil
		}

		// The path comes from this walk over an operator-chosen root — there
		// is no untrusted input to sanitize, and reading the files it finds is
		// the tool's entire purpose.
		src, rerr := os.ReadFile(path) //nolint:gosec // see above
		if rerr != nil {
			return rerr
		}

		out = append(out, pats.checkFile(path, string(src))...)

		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].file != out[j].file {
			return out[i].file < out[j].file
		}

		return out[i].line < out[j].line
	})

	return out, nil
}

// checkFile reports the links in one document that do not resolve, relative to
// that document's own directory.
func (p *patterns) checkFile(path, src string) []problem {
	var out []problem

	dir := filepath.Dir(path)

	for _, l := range p.extract(src) {
		if _, err := os.Stat(filepath.Join(dir, l.target)); err != nil {
			out = append(out, problem{file: path, line: l.line, target: l.target})
		}
	}

	return out
}

// emitf writes one diagnostic line, deliberately ignoring the write error. Every
// byte this tool produces is a report FOR the operator, so a failed write to
// their terminal cannot be reported anywhere they would see it — the same
// carve-out the console driver carries (FIX-028 §6.1).
func emitf(w io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(w, format, args...) //nolint:errcheck // see above
}
