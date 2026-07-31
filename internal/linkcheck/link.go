package linkcheck

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// link is one relative Markdown link target and the line it appeared on.
type link struct {
	target string
	line   int
}

// patterns holds the compiled Markdown matchers. They are built through an
// error-returning constructor rather than regexp.MustCompile because this
// package lives under internal/, where FIX-026 bans panicking Must* calls: a
// library must fail with a classified error, never crash the program that
// embedded it. The patterns are constants, so the error is unreachable in
// practice — but the rule is about the shape of the failure, not its odds.
type patterns struct {
	// inlineLink matches [text](target) and the reference form [id]: target.
	// The target group stops at the first closing paren or whitespace, so a
	// title — [t](path "title") — is not swallowed.
	inlineLink *regexp.Regexp
	refLink    *regexp.Regexp
	fenceLine  *regexp.Regexp
	inlineCode *regexp.Regexp
	hasScheme  *regexp.Regexp
}

// newPatterns compiles the matchers, reporting the first that fails.
func newPatterns() (*patterns, error) {
	var (
		p   patterns
		err error
	)

	for _, c := range []struct {
		into **regexp.Regexp
		expr string
	}{
		{&p.inlineLink, `\[[^\]]*\]\(([^)]*)\)`},
		{&p.refLink, `^\s{0,3}\[[^\]]+\]:\s+(\S+)`},
		{&p.fenceLine, "^\\s{0,3}(```|~~~)"},
		{&p.inlineCode, "`[^`]*`"},
		{&p.hasScheme, `^[a-zA-Z][a-zA-Z0-9+.\-]*:`},
	} {
		if *c.into, err = regexp.Compile(c.expr); err != nil {
			return nil, fmt.Errorf("linkcheck: pattern %q: %w", c.expr, err)
		}
	}

	return &p, nil
}

// extract returns every link target in src that names a path this repository
// could resolve. Two exclusions are requirements, not conveniences, both learned
// from FIX-031 §4.1:
//
//   - fenced and inline code is skipped. A Go generic signature such as
//     `values.NewArray[T](vals…)` is indistinguishable from a Markdown link to
//     a regex, and eight such spans exist in the docs today.
//   - targets are percent-DECODED, or every correct link to a file whose name
//     contains a space — "gobpm Development Roadmap.md" — reports as broken.
//
// Absolute URLs, protocol-relative URLs, mailto: and same-document anchors are
// not this checker's business: they rot for reasons outside the repository, and
// checking them would make the gate depend on the network.
func (p *patterns) extract(src string) []link {
	var (
		out    []link
		inCode bool
	)

	for i, raw := range strings.Split(src, "\n") {
		if p.fenceLine.MatchString(raw) {
			inCode = !inCode

			continue
		}

		if inCode {
			continue
		}

		line := p.inlineCode.ReplaceAllString(raw, "")

		for _, m := range p.inlineLink.FindAllStringSubmatch(line, -1) {
			if t, ok := p.cleanTarget(m[1]); ok {
				out = append(out, link{target: t, line: i + 1})
			}
		}

		if m := p.refLink.FindStringSubmatch(line); m != nil {
			if t, ok := p.cleanTarget(m[1]); ok {
				out = append(out, link{target: t, line: i + 1})
			}
		}
	}

	return out
}

// cleanTarget reduces a raw link target to the repository path it names, or
// reports false when it names something this checker does not verify.
func (p *patterns) cleanTarget(raw string) (string, bool) {
	t := strings.TrimSpace(raw)

	// <path> is the escaped form for a target containing spaces, so everything
	// inside the brackets is the path — a title split here would truncate it.
	if strings.HasPrefix(t, "<") {
		if end := strings.Index(t, ">"); end > 0 {
			t = t[1:end]
		} else {
			t = strings.TrimPrefix(t, "<")
		}
	} else if i := strings.IndexAny(t, " \t"); i >= 0 {
		// an unbracketed target ends at the title: [text](path "title")
		t = t[:i]
	}

	// drop the fragment; a pure #anchor addresses this same document.
	if i := strings.Index(t, "#"); i >= 0 {
		t = t[:i]
	}

	if t == "" || p.hasScheme.MatchString(t) || strings.HasPrefix(t, "//") {
		return "", false
	}

	decoded, err := url.PathUnescape(t)
	if err != nil {
		// A target that will not decode cannot name a file; report it as-is
		// rather than dropping it silently.
		return t, true
	}

	return decoded, true
}
