package data

import (
	"context"
	"reflect"
	"strconv"
)

// Change is one entry of a commit-diff: a data path and how it changed
// (ADR-011 v.6 §2.9.4). Path is rooted at the committed name and uses the
// structural grammar ("order", "order.items[0].price"); Type is the retargeted
// ChangeType vocabulary the observability DataChange phases mirror.
type Change struct {
	Path string
	Type ChangeType
}

// DiffValues compares the old and new value graphs rooted at root and returns
// one Change per changed path (ADR-011 v.6 §2.9.4, the commit-diff seam). A nil
// old (or new) means the whole (sub)value was added (or deleted) — one Change
// at its root, no per-leaf explosion; only within-shape differences descend.
// Returns nil when nothing changed.
//
// The walk runs over committed values under the scope's commit lock — it uses
// no cancellable I/O, so the value-accessor contexts are background ones.
func DiffValues(root string, oldV, newV Value) []Change {
	var changes []Change

	diffInto(&changes, root, valueOrNil(oldV), valueOrNil(newV))

	return changes
}

// valueOrNil normalizes a typed-nil Value interface to a plain nil any, so the
// presence checks in diffInto stay simple.
func valueOrNil(v Value) any {
	if v == nil {
		return nil
	}

	return v
}

// diffInto appends the changes between old and new at path. Elements arrive as
// any because a Collection may hold raw scalars (not data.Value) — mirroring
// WalkSteps' scalarLeaf handling: a raw element is compared by value, a Value
// element is compared per its kind.
func diffInto(changes *[]Change, path string, oldV, newV any) {
	switch {
	case oldV == nil && newV == nil:
		return

	case oldV == nil:
		*changes = append(*changes, Change{Path: path, Type: ValueAdded})

		return

	case newV == nil:
		*changes = append(*changes, Change{Path: path, Type: ValueDeleted})

		return
	}

	oldRec, oldIsRec := oldV.(Record)
	newRec, newIsRec := newV.(Record)

	if oldIsRec && newIsRec {
		diffRecords(changes, path, oldRec, newRec)

		return
	}

	oldCol, oldIsCol := oldV.(Collection)
	newCol, newIsCol := newV.(Collection)

	if oldIsCol && newIsCol {
		diffCollections(changes, path, oldCol, newCol)

		return
	}

	// A kind change (scalar↔record↔list) is one Updated at this node — the
	// shapes aren't comparable, so there is no descent.
	if oldIsRec != newIsRec || oldIsCol != newIsCol {
		*changes = append(*changes, Change{Path: path, Type: ValueUpdated})

		return
	}

	if !scalarEqual(oldV, newV) {
		*changes = append(*changes, Change{Path: path, Type: ValueUpdated})
	}
}

// diffRecords descends into two records over the union of their keys, in the
// new record's order first (stable output), then old-only keys (deletions).
func diffRecords(changes *[]Change, path string, oldV, newV Record) {
	ctx := context.Background()

	oldKeys := map[string]bool{}
	for _, k := range oldV.Keys() {
		oldKeys[k] = true
	}

	for _, k := range newV.Keys() {
		ov := fieldOrNil(ctx, oldV, k)
		nv := fieldOrNil(ctx, newV, k)

		diffInto(changes, path+"."+k, ov, nv)
		delete(oldKeys, k)
	}

	// old-only keys, in the old record's order: deletions.
	for _, k := range oldV.Keys() {
		if !oldKeys[k] {
			continue
		}

		diffInto(changes, path+"."+k, fieldOrNil(ctx, oldV, k), nil)
	}
}

// fieldOrNil reads a record field, normalizing a missing field to nil.
func fieldOrNil(ctx context.Context, r Record, key string) any {
	v, err := r.Field(ctx, key)
	if err != nil {
		return nil
	}

	return v
}

// diffCollections descends into two collections positionally over
// [0, max(len)); an index past one side's length is a nil there (an append is
// Added, a truncation Deleted).
func diffCollections(changes *[]Change, path string, oldV, newV Collection) {
	ctx := context.Background()

	oldN, newN := oldV.Count(), newV.Count()

	for i := range max(oldN, newN) {
		var ov, nv any

		if i < oldN {
			ov, _ = oldV.GetAt(ctx, i)
		}

		if i < newN {
			nv, _ = newV.GetAt(ctx, i)
		}

		diffInto(changes, path+"["+strconv.Itoa(i)+"]", ov, nv)
	}
}

// scalarEqual compares two leaf values: a data.Value leaf by its Get snapshot,
// a raw collection element by itself.
func scalarEqual(oldV, newV any) bool {
	ctx := context.Background()

	if ov, ok := oldV.(Value); ok {
		oldV = ov.Get(ctx)
	}

	if nv, ok := newV.(Value); ok {
		newV = nv.Get(ctx)
	}

	return reflect.DeepEqual(oldV, newV)
}
