package data

import (
	"context"
	"strconv"
)

// FieldInfo describes one field or element at a level of a value's shape
// (ADR-011 v.7 §2.9.1). Kind is "scalar" | "list" | "record" | "map"; Type
// carries the Go type name for a scalar only.
type FieldInfo struct {
	Name string
	Kind string
	Type string
}

// kindOf reports a value's structural kind by capability assertion: a Record
// has fields, a Collection is a list, a Map is a data-keyed dictionary,
// anything else is a scalar leaf. The probe order (Record, Collection, Map)
// is fixed and documented; a Value implements at most one structural
// capability (ADR-011 v.7 §2.9.1), so the order never decides a kind.
func kindOf(v Value) string {
	switch v.(type) {
	case Record:
		return "record"
	case Collection:
		return "list"
	case Map:
		return "map"
	default:
		return "scalar"
	}
}

// infoFor builds the FieldInfo for a value under the given name.
func infoFor(name string, v Value) FieldInfo {
	k := kindOf(v)

	fi := FieldInfo{Name: name, Kind: k}
	if k == "scalar" {
		fi.Type = v.Type()
	}

	return fi
}

// SchemaAt lists the shape at one level of v addressed by path ("" = v itself):
// a record's fields, a list's element slot ("[]"), or a scalar leaf. It is
// general over Value — a scalar is a valid answer, not an error (§4.7).
func SchemaAt(ctx context.Context, v Value, path string) ([]FieldInfo, error) {
	node, err := resolveInto(ctx, v, path)
	if err != nil {
		return nil, err
	}

	switch n := node.(type) {
	case Record:
		keys := n.Keys()
		out := make([]FieldInfo, 0, len(keys))

		for _, k := range keys {
			f, err := n.Field(ctx, k)
			if err != nil {
				return nil, err
			}

			out = append(out, infoFor(k, f))
		}

		return out, nil

	case Collection:
		fi := FieldInfo{Name: "[]", Kind: "unknown"}
		if n.Count() > 0 {
			e, err := n.GetAt(ctx, 0)
			if err != nil {
				return nil, err
			}

			fi = infoFor("[]", asValue(e))
		}

		return []FieldInfo{fi}, nil

	case Map:
		// One homogeneous element slot — the map counterpart of the list's
		// "[]" (a slot label, not an addressable key): a map's keys are data,
		// so its shape is the value shape, taken from the sorted-first entry.
		fi := FieldInfo{Name: `["*"]`, Kind: "unknown"}
		if keys := n.Keys(); len(keys) > 0 {
			e, err := n.Entry(ctx, keys[0])
			if err != nil {
				return nil, err
			}

			fi = infoFor(`["*"]`, asValue(e))
		}

		return []FieldInfo{fi}, nil

	default:
		return []FieldInfo{infoFor("", node)}, nil
	}
}

// Walk visits every node of v depth-first with its full path (the root is
// visited with path ""), so a caller can materialize the whole shape.
func Walk(
	ctx context.Context,
	v Value,
	visit func(path string, fi FieldInfo),
) error {
	visit("", infoFor("", v))

	return walkChildren(ctx, "", v, visit)
}

// walkChildren recurses into a Record's fields or a Collection's elements.
func walkChildren(
	ctx context.Context,
	path string,
	v Value,
	visit func(path string, fi FieldInfo),
) error {
	switch n := v.(type) {
	case Record:
		for _, k := range n.Keys() {
			f, err := n.Field(ctx, k)
			if err != nil {
				return err
			}

			childPath := path + "." + k
			visit(childPath, infoFor(k, f))

			if err := walkChildren(ctx, childPath, f, visit); err != nil {
				return err
			}
		}

	case Collection:
		for i := 0; i < n.Count(); i++ {
			e, err := n.GetAt(ctx, i)
			if err != nil {
				return err
			}

			child := asValue(e)
			childPath := path + "[" + strconv.Itoa(i) + "]"
			visit(childPath, infoFor("["+strconv.Itoa(i)+"]", child))

			if err := walkChildren(ctx, childPath, child, visit); err != nil {
				return err
			}
		}

	case Map:
		for _, k := range n.Keys() { // sorted — deterministic walk output
			e, err := n.Entry(ctx, k)
			if err != nil {
				return err
			}

			child := asValue(e)
			childPath := path + KeyLabel(k)
			visit(childPath, infoFor(KeyLabel(k), child))

			if err := walkChildren(ctx, childPath, child, visit); err != nil {
				return err
			}
		}
	}

	return nil
}

// resolveInto walks path relative to v (v is the root): the head is treated as
// a field of v, the remaining steps navigate on. "" returns v itself.
func resolveInto(ctx context.Context, v Value, path string) (Value, error) {
	if path == "" {
		return v, nil
	}

	head, steps, err := SplitPath(path)
	if err != nil {
		return nil, err
	}

	cur := v
	if head != "" {
		rec, ok := cur.(Record)
		if !ok {
			return nil, notNavigable("", "a record", "."+head, cur)
		}

		if cur, err = rec.Field(ctx, head); err != nil {
			return nil, err
		}
	}

	return WalkSteps(ctx, cur, steps)
}

// asValue returns a Collection element as a Value: itself when it already is
// one, or a read-only scalar leaf for a raw Go element.
func asValue(e any) Value {
	if v, ok := e.(Value); ok {
		return v
	}

	return scalarLeaf{v: e}
}
