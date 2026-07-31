package main

import (
	"net/url"
	"regexp"
	"strings"
)

// link is one relative Markdown link target and the line it appeared on.
type link struct {
	target string
	line   int
}

// inlineLink matches [text](target) and the reference form [id]: target. The
// target group is deliberately lazy and stops at the first closing paren or
// whitespace, so a title — [t](path "title") — is not swallowed.
var (
	inlineLink = regexp.MustCompile(`\[[^\]]*\]\(([^)]*)\)`)
	refLink    = regexp.MustCompile(`^\s{0,3}\[[^\]]+\]:\s+(\S+)`)
	fenceLine  = regexp.MustCompile("^\\s{0,3}(```|~~~)")
	inlineCode = regexp.MustCompile("`[^`]*`")
	hasScheme  = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.\-]*:`)
)

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
func extract(src string) []link {
	var (
		out    []link
		inCode bool
	)

	for i, raw := range strings.Split(src, "\n") {
		if fenceLine.MatchString(raw) {
			inCode = !inCode

			continue
		}

		if inCode {
			continue
		}

		line := inlineCode.ReplaceAllString(raw, "")

		for _, m := range inlineLink.FindAllStringSubmatch(line, -1) {
			if t, ok := cleanTarget(m[1]); ok {
				out = append(out, link{target: t, line: i + 1})
			}
		}

		if m := refLink.FindStringSubmatch(line); m != nil {
			if t, ok := cleanTarget(m[1]); ok {
				out = append(out, link{target: t, line: i + 1})
			}
		}
	}

	return out
}

// cleanTarget reduces a raw link target to the repository path it names, or
// reports false when it names something this checker does not verify.
func cleanTarget(raw string) (string, bool) {
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

	if t == "" || hasScheme.MatchString(t) || strings.HasPrefix(t, "//") {
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
