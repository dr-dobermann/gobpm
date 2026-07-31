package linkcheck

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The two exclusions below are requirements paid for by FIX-031, not
// conveniences: without them the checker is either useless (eight false
// positives from Go generics in inline code) or wrong (every link to a file
// whose name contains a space reports broken).

func TestExtractSkipsWhatIsNotALink(t *testing.T) {
	src := "" +
		"[real](./doc.md)\n" +
		"```go\n" +
		"[fenced](./nope.md)\n" +
		"```\n" +
		"a generic `values.NewArray[T](vals…)` in prose\n" +
		"[anchor](#section)\n" +
		"[external](https://example.com/x.md)\n" +
		"[protocol-relative](//example.com/x.md)\n" +
		"[mail](mailto:someone@example.com)\n" +
		"[titled](./titled.md \"a title\")\n" +
		"[ref]: ./reference.md\n"

	got := mustPatterns(t).extract(src)

	targets := make([]string, 0, len(got))
	for _, l := range got {
		targets = append(targets, l.target)
	}

	require.Equal(t,
		[]string{"./doc.md", "./titled.md", "./reference.md"}, targets,
		"fenced blocks, inline code, anchors and absolute URLs are not links "+
			"this checker resolves")

	require.Equal(t, 1, got[0].line, "the line number is reported for the fix")
}

func TestExtractDecodesPercentEncodedTargets(t *testing.T) {
	got := mustPatterns(t).extract("[roadmap](docs/gobpm%20Development%20Roadmap.md)\n")

	require.Len(t, got, 1)
	require.Equal(t, "docs/gobpm Development Roadmap.md", got[0].target,
		"an encoded href names a real file and must not report as broken")
}

func TestExtractHandlesAngleBracketTargets(t *testing.T) {
	got := mustPatterns(t).extract("[spaced](<a file.md>)\n")

	require.Len(t, got, 1)
	require.Equal(t, "a file.md", got[0].target)
}

func TestCheckFileResolvesRelativeToTheDocument(t *testing.T) {
	dir := t.TempDir()

	require.NoError(t, os.MkdirAll(filepath.Join(dir, "sub"), 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "sub", "there.md"), []byte("x"), 0o600))

	doc := filepath.Join(dir, "sub", "here.md")
	src := "[sibling](there.md)\n[missing](gone.md)\n[up](../also-gone.md)\n"

	require.NoError(t, os.WriteFile(doc, []byte(src), 0o600))

	got := mustPatterns(t).checkFile(doc, src)

	require.Len(t, got, 2, "the sibling resolves; the other two do not")
	require.Equal(t, "gone.md", got[0].target)
	require.Equal(t, 2, got[0].line)
	require.Equal(t, "../also-gone.md", got[1].target)
}

func TestScanWalksMarkdownAndSortsItsFindings(t *testing.T) {
	dir := t.TempDir()

	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".git"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".git", "b.md"),
		[]byte("[skipped](nope.md)\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.md"),
		[]byte("[dead](nope.md)\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "ok.txt"),
		[]byte("[dead](nope.md)\n"), 0o600))

	got, err := scan(dir)
	require.NoError(t, err)

	require.Len(t, got, 1,
		"only Markdown is scanned, and .git is not documentation")
	require.Equal(t, "nope.md", got[0].target)
}

func TestScanReportsAWalkFailure(t *testing.T) {
	_, err := scan(filepath.Join(t.TempDir(), "does-not-exist"))
	require.Error(t, err)
}

// run carries the tool's contract: what it prints and which exit code it
// chooses. A CLI whose behaviour is only reachable by spawning a process tends
// to go untested — and this one is a blocking gate step, so its verdict matters.
func TestRunExitCodes(t *testing.T) {
	dir := t.TempDir()

	require.NoError(t, os.WriteFile(filepath.Join(dir, "good.md"),
		[]byte("[self](good.md)\n"), 0o600))

	t.Run("clean tree exits 0", func(t *testing.T) {
		var out, errOut strings.Builder

		code := Run([]string{"-root", dir}, &out, &errOut)

		require.Equal(t, 0, code)
		require.Contains(t, out.String(), "every relative link resolves")
	})

	t.Run("dead link exits 1 and names it", func(t *testing.T) {
		require.NoError(t, os.WriteFile(filepath.Join(dir, "bad.md"),
			[]byte("\n[gone](missing.md)\n"), 0o600))

		var out, errOut strings.Builder

		code := Run([]string{"-root", dir}, &out, &errOut)

		require.Equal(t, 1, code)
		require.Contains(t, out.String(), "bad.md:2: dead link: missing.md",
			"the report is file:line so the fix is one jump away")
		require.Contains(t, errOut.String(), "1 dead link(s)")
	})

	t.Run("unusable root exits 2", func(t *testing.T) {
		var out, errOut strings.Builder

		code := Run([]string{"-root", filepath.Join(dir, "nope")}, &out, &errOut)

		require.Equal(t, 2, code)
		require.Contains(t, errOut.String(), "linkcheck:")
	})

	t.Run("a bad flag exits 2", func(t *testing.T) {
		var out, errOut strings.Builder

		require.Equal(t, 2, Run([]string{"-nope"}, &out, &errOut))
	})
}

// mustPatterns builds the compiled matchers for a test. The constructor returns
// an error because internal/ bans panicking Must* calls (FIX-026); the tests
// assert it succeeds rather than assuming it.
func mustPatterns(t *testing.T) *patterns {
	t.Helper()

	p, err := newPatterns()
	require.NoError(t, err)

	return p
}
