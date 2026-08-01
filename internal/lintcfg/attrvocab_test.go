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

// TestNoLiteralAttrKeys closes the call-sites→constants direction: a key that
// HAS an Attr* constant must be reached through it, never retyped. Before
// FIX-035 swept them, 233 sites across 43 files hand-typed a canonical key, so
// the vocabulary was a convention rather than something the compiler enforced —
// and a typo in "instance_id" would surface only when someone grepped a log and
// found nothing.
//
// Two exclusions are structural, not allowlists, so neither can rot:
//
//   - fact.go is where the constants are DECLARED, so its literals are the
//     definitions themselves.
//   - struct tags are skipped via ast.Field.Tag rather than by file or name.
//     internal/instance/checkpoint/document.go carries json:"instance_id",
//     json:"version" and json:"ordinal" — the persisted checkpoint wire format,
//     which collides with vocabulary keys by spelling alone. Rewriting those
//     would change stored documents; they are not log attributes.
//
// Test files are out of scope: a test may legitimately hardcode a key to assert
// what a log record or a persisted document actually contains.
func TestNoLiteralAttrKeys(t *testing.T) {
	root := repoRoot(t)

	values := map[string]string{} // literal value -> constant name
	for name, v := range attrConstants(t, root) {
		values[v] = name
	}

	var offenders []string

	for _, dir := range []string{"pkg", "internal"} {
		err := filepath.WalkDir(filepath.Join(root, dir),
			func(path string, d os.DirEntry, err error) error {
				if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") {
					return err
				}

				rel, relErr := filepath.Rel(root, path)
				if relErr != nil {
					return relErr
				}

				rel = filepath.ToSlash(rel)
				if strings.HasSuffix(rel, "_test.go") || rel == factFile ||
					strings.HasPrefix(rel, "generated/") {
					return nil
				}

				offenders = append(offenders, literalKeySites(t, path, rel, values)...)

				return nil
			})
		require.NoError(t, err, "walking %s", dir)
	}

	require.Empty(t, offenders,
		"these sites hand-type a key that already has an Attr* constant; use "+
			"the constant so the vocabulary is enforced by the compiler:\n  %s",
		strings.Join(offenders, "\n  "))
}

// literalKeySites returns every string literal in path whose value matches a
// canonical key, skipping struct tags.
func literalKeySites(
	t *testing.T, path, rel string, values map[string]string,
) []string {
	t.Helper()

	fset := token.NewFileSet()

	f, err := parser.ParseFile(fset, path, nil, 0)
	require.NoError(t, err, "parsing %s", rel)

	tags := map[*ast.BasicLit]bool{}

	ast.Inspect(f, func(n ast.Node) bool {
		if fld, ok := n.(*ast.Field); ok && fld.Tag != nil {
			tags[fld.Tag] = true
		}

		return true
	})

	var out []string

	ast.Inspect(f, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING || tags[lit] {
			return true
		}

		v, uerr := strconv.Unquote(lit.Value)
		if uerr != nil {
			return true
		}

		if name, canonical := values[v]; canonical {
			out = append(out, fset.Position(lit.Pos()).String()+
				": \""+v+"\" — use observability."+name)
		}

		return true
	})

	return out
}

// TestErrDetailKeysAreVocabulary closes the THIRD direction, the one FIX-035's
// first pass left open: a key that has no constant and appears nowhere in the
// ADR passes both other guards, because neither asks about keys the vocabulary
// has never heard of. That is how 28 entity-shaped keys — activity_id and
// gateway_id for a node, task_name for a node's name, event_type duplicating
// event_definition_type — accumulated across packages while every registered
// key stayed correct.
//
// The rule: errs.D's first argument is either an observability.Attr* selector,
// or a literal that ADR-022 §2.5 lists as a descriptive attribute. Anything
// else is a key nobody registered and nobody can reliably grep for.
func TestErrDetailKeysAreVocabulary(t *testing.T) {
	root := repoRoot(t)
	documented := documentedKeys(t, root)

	var offenders []string

	for _, dir := range []string{"pkg", "internal"} {
		err := filepath.WalkDir(filepath.Join(root, dir),
			func(path string, d os.DirEntry, err error) error {
				if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") {
					return err
				}

				rel, rerr := filepath.Rel(root, path)
				if rerr != nil {
					return rerr
				}

				rel = filepath.ToSlash(rel)
				if strings.HasSuffix(rel, "_test.go") ||
					strings.HasPrefix(rel, "generated/") {
					return nil
				}

				offenders = append(offenders,
					unvocabularyDetailKeys(t, path, documented)...)

				return nil
			})
		require.NoError(t, err, "walking %s", dir)
	}

	require.Empty(t, offenders,
		"errs.D is called with a key that is neither an observability.Attr* "+
			"constant nor a descriptive attribute listed in ADR-022 §2.5. Either "+
			"use the canonical constant for the entity, or register the key:\n  %s",
		strings.Join(offenders, "\n  "))
}

// entityShaped matches a key that NAMES something — one ending in _id, _name,
// _key, _path or _ref. Only these must be registered: §2.5 leaves descriptive
// attributes free-form by design, so demanding that "count" or "offset" appear
// in the ADR would contradict the rule this guard enforces.
var entityShaped = regexp.MustCompile(`^[a-z]+(_[a-z]+)*_(id|name|key|path|ref)$`)

// unvocabularyDetailKeys reports every errs.D call in path whose first argument
// is an entity-shaped string literal absent from the documented vocabulary.
func unvocabularyDetailKeys(
	t *testing.T, path string, documented map[string]bool,
) []string {
	t.Helper()

	fset := token.NewFileSet()

	f, err := parser.ParseFile(fset, path, nil, 0)
	require.NoError(t, err, "parsing %s", path)

	var out []string

	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}

		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "D" {
			return true
		}

		if pkg, ok := sel.X.(*ast.Ident); !ok || pkg.Name != "errs" {
			return true
		}

		lit, ok := call.Args[0].(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true // already a constant — the good case
		}

		v, uerr := strconv.Unquote(lit.Value)
		if uerr != nil || documented[v] || !entityShaped.MatchString(v) {
			return true
		}

		out = append(out, fset.Position(lit.Pos()).String()+": errs.D(\""+v+"\", …)")

		return true
	})

	return out
}
