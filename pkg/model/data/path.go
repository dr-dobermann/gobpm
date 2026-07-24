package data

import (
	"context"
	"reflect"
	"strconv"
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
)

// structuralChars are the path characters that separate a name from its
// structural navigation ('.field', '[index]'). A name never contains them
// (CheckName, SRD-042 FR-6), so a path splits unambiguously.
const structuralChars = ".["

// Step is one navigation step of a structural path (ADR-011 v.7 §2.9.2): a
// field step (Field set) descends into a Record; a key step (Key set, the
// `["key"]` form) descends into a Map; an index step (both empty, Index >= 0)
// descends into a Collection. The discrimination is unambiguous — an empty
// field name is CheckName-illegal and an empty map key is rejected by the
// lexer — and Step stays comparable (PathsOverlap matches steps with ==).
type Step struct {
	Field string
	Key   string
	Index int
}

// isField reports whether the step is a field step (an index step has an empty
// Field — an empty field name is CheckName-illegal, so the discriminator is
// unambiguous).
func (s Step) isField() bool { return s.Field != "" }

// isKey reports whether the step is a map-key step (a non-key step has an
// empty Key — an empty map key is not parseable, so the discriminator is
// unambiguous).
func (s Step) isKey() bool { return s.Key != "" }

// SplitPath splits a structural path "order.items[0].price" into its head name
// ("order") and its navigation steps ([.items, [0], .price]). A path with no
// structural characters returns (path, nil, nil) — the plain-name case. A
// malformed path (empty field, unclosed/empty/non-numeric/negative index, a
// stray name segment, a ']' in the head) returns a classified error.
func SplitPath(path string) (head string, steps []Step, err error) {
	i := strings.IndexAny(path, structuralChars)
	if i < 0 {
		return path, nil, nil
	}

	head, rest := path[:i], path[i:]
	if strings.ContainsRune(head, ']') || head == "" {
		return "", nil, pathErr("malformed path head %q", path)
	}

	for rest != "" {
		var step Step

		step, rest, err = nextStep(rest, path)
		if err != nil {
			return "", nil, err
		}

		steps = append(steps, step)
	}

	return head, steps, nil
}

// nextStep consumes one '.field' or '[index]' step from the front of rest.
func nextStep(rest, path string) (Step, string, error) {
	switch rest[0] {
	case '.':
		rest = rest[1:]

		j := strings.IndexAny(rest, structuralChars)

		name := rest
		if j >= 0 {
			name, rest = rest[:j], rest[j:]
		} else {
			rest = ""
		}

		if name == "" || strings.ContainsRune(name, ']') {
			return Step{}, "", pathErr("malformed field step in %q", path)
		}

		return Step{Field: name}, rest, nil

	case '[':
		// A leading quote is a map-key step `["key"]`; a bare number is a
		// list-index step `[i]` (SRD-047 §4.3 — the forms do not collide).
		if len(rest) > 1 && rest[1] == '"' {
			return keyStep(rest, path)
		}

		j := strings.IndexByte(rest, ']')
		if j < 0 {
			return Step{}, "", pathErr("unclosed index in %q", path)
		}

		idx, convErr := strconv.Atoi(rest[1:j])
		if convErr != nil || idx < 0 {
			return Step{}, "", pathErr("bad index %q in %q", rest[1:j], path)
		}

		return Step{Index: idx}, rest[j+1:], nil

	default:
		return Step{}, "", pathErr("unexpected character %q in %q",
			string(rest[0]), path)
	}
}

// keyStep consumes one `["key"]` map-key step from the front of rest (rest
// starts with `["`). The key is scanned byte-wise inside the quotes — safe
// for UTF-8, since both escapable bytes are ASCII — with exactly two escapes,
// `\"` and `\\`; any other backslash sequence, an unclosed quote, an empty
// key, or a missing closing `]` is a classified error (SRD-047 §4.3).
func keyStep(rest, path string) (Step, string, error) {
	var b strings.Builder

	i := 2 // past `["`
	for ; i < len(rest); i++ {
		c := rest[i]

		if c == '\\' {
			i++
			if i >= len(rest) || (rest[i] != '"' && rest[i] != '\\') {
				return Step{}, "", pathErr("bad escape in a map key in %q",
					path)
			}

			b.WriteByte(rest[i])

			continue
		}

		if c == '"' {
			break
		}

		b.WriteByte(c)
	}

	if i >= len(rest) {
		return Step{}, "", pathErr("unclosed map key in %q", path)
	}

	if i+1 >= len(rest) || rest[i+1] != ']' {
		return Step{}, "", pathErr("missing ']' after a map key in %q", path)
	}

	key := b.String()
	if key == "" {
		return Step{}, "", pathErr("an empty map key in %q", path)
	}

	return Step{Key: key}, rest[i+2:], nil
}

// keyEscaper escapes the two characters that collide with the `["key"]` path
// syntax; every other character rides verbatim (SRD-047 §4.3).
var keyEscaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`)

// KeyLabel renders a map key as its canonical escaped path-step form —
// `["key"]` with `\` and `"` backslash-escaped — the exact form the path
// lexer parses back. It is the map counterpart of the "[i]" index label,
// shared by shape walks, diff paths, and error texts. Note: an empty key
// renders `[""]`, which is not a parseable step (map keys are non-empty).
func KeyLabel(key string) string {
	return `["` + keyEscaper.Replace(key) + `"]`
}

func pathErr(format string, args ...any) error {
	return errs.New(
		errs.M(format, args...),
		errs.C(errorClass, errs.InvalidParameter))
}

// ResolvePath resolves a possibly-structural name for a Source.Find: it splits
// the name, resolves the head through resolveHead (the Source's own exact
// lookup), and — when the name carries structural steps — walks them into the
// head's value, returning the leaf as a path-named Data. A plain name (no
// steps) returns the head Data unchanged, so a Source's existing behavior is
// preserved. A head that is not Ready cannot be navigated (structural reads run
// only over usable data).
func ResolvePath(
	ctx context.Context,
	name string,
	resolveHead func(head string) (Data, error),
) (Data, error) {
	head, steps, err := SplitPath(name)
	if err != nil {
		return nil, err
	}

	d, err := resolveHead(head)
	if err != nil {
		return nil, err
	}

	if len(steps) == 0 {
		return d, nil
	}

	if d.State().Name() != ReadyDataState.Name() {
		return nil, errs.New(
			errs.M("cannot navigate %q: %q is not in Ready state", name, head),
			errs.C(errorClass, errs.InvalidParameter))
	}

	leaf, err := WalkSteps(ctx, d.Value(), steps)
	if err != nil {
		return nil, err
	}

	return NewPathData(name, leaf)
}

// ParsePath parses a root-relative structural path into its full step list.
// Unlike SplitPath (which splits a NAME head from its steps for a Source.Find),
// the leading segment here is itself a step — a field or an index — because the
// root value may be a record OR a list ("items[0].price" vs "[0].total"). An
// empty path returns nil. It is the write-side (SetPath) counterpart of
// SplitPath (ADR-011 v.6 §2.9.3).
func ParsePath(path string) ([]Step, error) {
	if path == "" {
		return nil, nil
	}

	// A path that starts with '[' is a leading index step — SplitPath rejects
	// an empty head, so tokenize the whole path directly.
	if path[0] == '[' {
		var steps []Step

		rest := path
		for rest != "" {
			step, r, err := nextStep(rest, path)
			if err != nil {
				return nil, err
			}

			steps = append(steps, step)
			rest = r
		}

		return steps, nil
	}

	// A leading name: SplitPath gives (head, steps); the head is the first field.
	head, steps, err := SplitPath(path)
	if err != nil {
		return nil, err
	}

	return append([]Step{{Field: head}}, steps...), nil
}

// PathsOverlap reports whether two structural paths address overlapping data:
// they are equal, or one is a segment-boundary prefix of the other ("order"
// overlaps "order.items[0].price" in both directions; "order" and "orders"
// do not). It is the intersection semantics of a dependency declaration
// (DependencyLister, ADR-006 v.3 §2.7): a change at a path affects every
// dependency on the path, its ancestors, and its descendants. A malformed
// or empty path is a classified error — callers on the conditional sweep
// fail toward re-evaluation, never toward a silent skip.
func PathsOverlap(a, b string) (bool, error) {
	as, err := overlapSteps(a)
	if err != nil {
		return false, err
	}

	bs, err := overlapSteps(b)
	if err != nil {
		return false, err
	}

	short := as
	if len(bs) < len(as) {
		short = bs
	}

	for i := range short {
		if as[i] != bs[i] {
			return false, nil
		}
	}

	return true, nil
}

// overlapSteps parses one PathsOverlap argument into its full step list,
// rejecting the empty path (a Step is comparable, so prefix matching is a
// plain element-wise ==).
func overlapSteps(path string) ([]Step, error) {
	if path == "" {
		return nil, pathErr("an empty path isn't allowed")
	}

	return ParsePath(path)
}

// WalkSteps folds steps over v: a field step asserts Record and calls Field; a
// key step asserts Map and calls Entry; an index step asserts Collection and
// calls GetAt. A Collection element or Map entry that is not itself a Value is
// a read-only scalar leaf — a further step into it is a classified error.
// Every mis-step names the walked prefix and the actual kind.
func WalkSteps(ctx context.Context, v Value, steps []Step) (Value, error) {
	cur := v
	walked := ""

	for _, s := range steps {
		if s.isField() {
			rec, ok := cur.(Record)
			if !ok {
				return nil, notNavigable(walked, "a record", "."+s.Field, cur)
			}

			next, err := rec.Field(ctx, s.Field)
			if err != nil {
				return nil, err
			}

			cur, walked = next, walked+"."+s.Field

			continue
		}

		if s.isKey() {
			m, ok := cur.(Map)
			if !ok {
				return nil, notNavigable(walked, "a map", KeyLabel(s.Key), cur)
			}

			raw, err := m.Entry(ctx, s.Key)
			if err != nil {
				return nil, err
			}

			cur, walked = asValue(raw), walked+KeyLabel(s.Key)

			continue
		}

		col, ok := cur.(Collection)
		if !ok {
			idx := "[" + strconv.Itoa(s.Index) + "]"

			return nil, notNavigable(walked, "a list", idx, cur)
		}

		raw, err := col.GetAt(ctx, s.Index)
		if err != nil {
			return nil, err
		}

		walked += "[" + strconv.Itoa(s.Index) + "]"

		if val, ok := raw.(Value); ok {
			cur = val
		} else {
			cur = scalarLeaf{v: raw}
		}
	}

	return cur, nil
}

// notNavigable builds the classified error for a step that cannot be taken
// because the current value is not of the required kind.
func notNavigable(walked, want, step string, got Value) error {
	prefix := walked
	if prefix == "" {
		prefix = "<root>"
	}

	return errs.New(
		errs.M("cannot take %q: %s is not %s but a %s",
			step, prefix, want, kindOf(got)),
		errs.C(errorClass, errs.InvalidParameter))
}

// scalarLeaf wraps a raw Go value read from a Collection as a read-only
// data.Value leaf, so a path read always yields a Value. It is not writable —
// structural writes go through the owning Record/Collection (SRD-042 S2).
type scalarLeaf struct{ v any }

// Get returns the wrapped raw value.
func (s scalarLeaf) Get(context.Context) any { return s.v }

// Update always errors: a path-read scalar leaf is read-only.
func (s scalarLeaf) Update(context.Context, any) error {
	return errs.New(
		errs.M("a path-read scalar is read-only"),
		errs.C(errorClass, errs.InvalidParameter))
}

// Lock is a no-op on the immutable leaf.
func (s scalarLeaf) Lock() {}

// Unlock is a no-op on the immutable leaf.
func (s scalarLeaf) Unlock() {}

// Type returns the wrapped value's Go type name.
func (s scalarLeaf) Type() string {
	if s.v == nil {
		return "nil"
	}

	return reflect.TypeOf(s.v).String()
}

// Clone returns the leaf itself — it is immutable.
func (s scalarLeaf) Clone() Value { return s }

// pathData is a transient, read-only data.Data wrapping a path-resolution leaf.
// Its Name() is the full path — deliberately NOT CheckName-validated, since a
// path is a derived address, not a data-element name (SRD-042 §3.5).
type pathData struct {
	v    Value
	idef *ItemDefinition
	path string
}

// NewPathData wraps a path-resolution leaf as a read-only Data named by the
// full path (state Ready), for a Source.Find result over a structural path.
// A nil value is rejected with an error (FIX-026 — public APIs validate,
// never defer a panic).
func NewPathData(path string, v Value) (Data, error) {
	if v == nil {
		return nil, errs.New(
			errs.M("NewPathData: a nil Value isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed),
			errs.D("path", path))
	}

	idef, err := NewItemDefinition(v)
	if err != nil {
		return nil, err
	}

	return pathData{path: path, v: v, idef: idef}, nil
}

// ID returns the path.
func (d pathData) ID() string { return d.path }

// Docs returns no documentation.
func (d pathData) Docs() []*foundation.Documentation { return nil }

// Name returns the full path.
func (d pathData) Name() string { return d.path }

// Value returns the resolved leaf value.
func (d pathData) Value() Value { return d.v }

// State returns the Ready state — a path resolves only over usable data.
func (d pathData) State() SrcState { return *ReadyDataState }

// ItemDefinition returns the leaf's item definition.
func (d pathData) ItemDefinition() *ItemDefinition { return d.idef }
