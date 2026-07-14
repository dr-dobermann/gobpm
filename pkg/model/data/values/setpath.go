package values

import (
	"context"
	"strconv"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
)

// SetPath sets v at the structural path within root — a path relative to root,
// "items[0].price" or "[0].total" (ADR-011 v.6 §2.9.3). It walks to the parent
// of the last step, creating missing intermediate records/lists on a permissive
// dynamic target (a following ".field" → a values.Record, a following "[i]" → a
// values.Array), then sets the last step via Record.SetField or
// Collection.SetAt. An empty path is an error — a whole-value write is
// Value.Update.
//
// It lives in `values` (not `data`) because auto-vivify constructs concrete
// values.Record / values.Array, and `data` cannot import `values`.
func SetPath(
	ctx context.Context, root data.Value, path string, v data.Value,
) error {
	steps, err := data.ParsePath(path)
	if err != nil {
		return err
	}

	if len(steps) == 0 {
		return errs.New(
			errs.M("SetPath: an empty path — a whole-value write is Value.Update"),
			errs.C(errorClass, errs.InvalidParameter))
	}

	parent, err := walkToParent(ctx, root, steps)
	if err != nil {
		return err
	}

	return setLast(ctx, parent, steps[len(steps)-1], v)
}

// walkToParent descends steps[:len-1] from root, creating a missing intermediate
// per the NEXT step's kind on a permissive dynamic parent, and returns the owner
// of the last step.
func walkToParent(
	ctx context.Context, root data.Value, steps []data.Step,
) (data.Value, error) {
	cur := root

	for i := 0; i < len(steps)-1; i++ {
		child, err := descendOrVivify(ctx, cur, steps[i], steps[i+1])
		if err != nil {
			return nil, err
		}

		cur = child
	}

	return cur, nil
}

// descendOrVivify takes one step into cur, creating the child (typed per next)
// when it is missing on a permissive dynamic parent.
func descendOrVivify(
	ctx context.Context, cur data.Value, step, next data.Step,
) (data.Value, error) {
	if step.Field != "" {
		rec, ok := cur.(data.Record)
		if !ok {
			return nil, notWritable("."+step.Field, "a record", cur)
		}

		if child, err := rec.Field(ctx, step.Field); err == nil {
			return child, nil // an existing field holds a non-nil Value
		}

		// missing → vivify and attach; a typed parent that rejects a set
		// surfaces its own error (walkToParent discards the child then).
		child := vivify(next)

		return child, rec.SetField(ctx, step.Field, child)
	}

	col, ok := cur.(data.Collection)
	if !ok {
		return nil, notWritable(indexLabel(step.Index), "a list", cur)
	}

	if raw, err := col.GetAt(ctx, step.Index); err == nil {
		child, ok := raw.(data.Value)
		if !ok {
			return nil, notWritable(indexLabel(step.Index),
				"a navigable element", cur)
		}

		return child, nil
	}

	// missing → append the vivified child (SetAt appends only at index == len;
	// an index past len surfaces its own error, discarding the child).
	child := vivify(next)

	return child, col.SetAt(ctx, step.Index, child)
}

// setLast applies the final step as a set.
func setLast(
	ctx context.Context, parent data.Value, last data.Step, v data.Value,
) error {
	if last.Field != "" {
		rec, ok := parent.(data.Record)
		if !ok {
			return notWritable("."+last.Field, "a record", parent)
		}

		return rec.SetField(ctx, last.Field, v)
	}

	col, ok := parent.(data.Collection)
	if !ok {
		return notWritable(indexLabel(last.Index), "a list", parent)
	}

	return col.SetAt(ctx, last.Index, v)
}

// vivify builds a fresh empty dynamic container for a missing intermediate: a
// following field step needs a record, a following index step needs a list.
func vivify(next data.Step) data.Value {
	if next.Field != "" {
		r, _ := NewRecord() // no fields → never errors

		return r
	}

	return NewArray[data.Value]()
}

func indexLabel(i int) string {
	return "[" + strconv.Itoa(i) + "]"
}

func notWritable(step, want string, got data.Value) error {
	return errs.New(
		errs.M("cannot set %q: the container is not %s (is %s)",
			step, want, got.Type()),
		errs.C(errorClass, errs.InvalidParameter))
}
