package lintcfg

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The vocabulary's two halves: the Attr* constants that ARE the vocabulary, and
// the ADR section that documents it. ADR-022 v.2 §2.5 requires them to agree,
// and this file is what makes that requirement real rather than aspirational —
// by v.2 the prose rule alone had let 28 constants enter the code unregistered.
const (
	factFile = "pkg/observability/fact.go"
	vocabDoc = "docs/design/ADR-022-error-propagation-and-logging-policy.md"
)

// attrConstants returns every Attr* constant's value, keyed by constant name.
func attrConstants(t *testing.T, root string) map[string]string {
	t.Helper()

	path := filepath.Join(root, factFile)

	f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	require.NoError(t, err, "parsing %s", factFile)

	out := map[string]string{}

	for _, d := range f.Decls {
		gen, ok := d.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}

		for _, spec := range gen.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}

			for i, name := range vs.Names {
				if !strings.HasPrefix(name.Name, "Attr") || i >= len(vs.Values) {
					continue
				}

				lit, ok := vs.Values[i].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}

				v, err := strconv.Unquote(lit.Value)
				require.NoError(t, err)

				out[name.Name] = v
			}
		}
	}

	return out
}

// documentedKeys returns every `backticked` token inside ADR-022 §2.5 — both
// the canonical table and the descriptive list, since a constant may legitimately
// be either. The section is delimited by its own headings, so a later section
// growing a code span cannot silently widen what counts as documented.
func documentedKeys(t *testing.T, root string) map[string]bool {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join(root, vocabDoc))
	require.NoError(t, err, "reading %s", vocabDoc)

	doc := string(raw)

	start := strings.Index(doc, "### 2.5 One attribute vocabulary")
	require.Positive(t, start, "§2.5 heading not found — was the ADR restructured?")

	end := strings.Index(doc[start:], "### 2.6 ")
	require.Positive(t, end, "§2.6 heading not found after §2.5")

	section := doc[start : start+end]
	out := map[string]bool{}

	for _, m := range regexp.MustCompile("`([a-z_]+)`").FindAllStringSubmatch(section, -1) {
		out[m[1]] = true
	}

	return out
}

// TestAttrConstantsAreRegistered (FIX-035 §4.1) closes the constants→doc
// direction of ADR-022 v.2 §2.5: a key that exists in code but was never
// registered in the vocabulary fails the build. Before this test, 28 of 47
// constants were in exactly that state — the registration rule was prose, and
// prose is enforced by whoever happens to remember it.
func TestAttrConstantsAreRegistered(t *testing.T) {
	root := repoRoot(t)
	consts := attrConstants(t, root)
	documented := documentedKeys(t, root)

	require.NotEmpty(t, consts, "no Attr* constants parsed — did fact.go move?")

	var missing []string

	for name, value := range consts {
		if !documented[value] {
			missing = append(missing, name+" = \""+value+"\"")
		}
	}

	require.Empty(t, missing,
		"these Attr* constants are absent from ADR-022 §2.5. Add each to the "+
			"canonical table (it names WHICH object the event is about) or to "+
			"the descriptive list (it characterises the event itself), and bump "+
			"the ADR if it is canonical:\n  %s",
		strings.Join(missing, "\n  "))
}

// TestDocumentedCanonicalKeysHaveConstants closes the reverse direction: a key
// the ADR canonizes must be reachable as a constant, or call sites retype it as
// a literal — which is exactly how `event_definition_type` and
// `event_processor_id` came to have 20 hand-written occurrences between them.
// Only the CANONICAL table is checked; descriptive attributes are free-form by
// design and deliberately need no constant.
func TestDocumentedCanonicalKeysHaveConstants(t *testing.T) {
	root := repoRoot(t)

	raw, err := os.ReadFile(filepath.Join(root, vocabDoc))
	require.NoError(t, err)

	doc := string(raw)
	start := strings.Index(doc, "**Canonical keys**")
	require.Positive(t, start, "canonical-keys heading not found")

	end := strings.Index(doc[start:], "**Descriptive attributes**")
	require.Positive(t, end, "descriptive-attributes heading not found")

	values := map[string]bool{}
	for _, v := range attrConstants(t, root) {
		values[v] = true
	}

	var orphans []string

	// Only rows of the markdown table carry keys; the prose around it does not.
	for _, line := range strings.Split(doc[start:start+end], "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "|") {
			continue
		}

		for _, m := range regexp.MustCompile("`([a-z_]+)`").FindAllStringSubmatch(line, -1) {
			if !values[m[1]] {
				orphans = append(orphans, m[1])
			}
		}
	}

	require.Empty(t, orphans,
		"ADR-022 §2.5 canonizes these keys but pkg/observability declares no "+
			"Attr* constant for them, so every call site must retype them as a "+
			"string literal:\n  %s", strings.Join(orphans, "\n  "))
}
