package data

import (
	"context"
)

// Value interface provides methods for data manipulation and locking.
type Value interface {
	// Get returns copy of the Value's value.
	// For collection Get retrieves element with current index
	// if collection is empty then panic will be fired.
	Get(ctx context.Context) any

	// Update sets new value of the Value.
	// For collection Update changes element with current index
	// if collection is empty then panic will be fired.
	Update(context.Context, any) error

	// Lock locks Value's internal mutex in case user need to update internal
	// Value through its pointer.
	Lock()

	// Unlock unlocks internal Value's mutex.
	Unlock()

	// Type returns string representation of the Value's type.
	Type() string

	// Clone creates a clone of the Value
	Clone() Value
}

// StepDirection defines the direction of stepping through collection elements.
type StepDirection bool

const (
	// StepForward indicates forward stepping through collection elements.
	StepForward StepDirection = true
	// StepBackward indicates backward stepping through collection elements.
	StepBackward StepDirection = false
)

// Collection interface extends Value to provide collection-specific operations.
type Collection interface {
	Value

	// Count returns legth of the collection.
	Count() int

	// Rewind sets current index in collection to start position
	Rewind()

	// GoTo sets collection current index to desired position.
	// first element has 0 index.
	GoTo(position any) error

	// Next shifts current index of the collection for given distance.
	Next(dir StepDirection) error

	// GetAll returns all values of the collection.
	GetAll(context.Context) []any

	// GetKeys returns a list of keys
	GetKeys() []any

	// Index returns current index in the collection.
	Index() any

	// Clear removes all elements in the collection and
	// sets index to 0.
	Clear()

	// Add adds new value into the end of the collection.
	// If there is any problem occurred, then error returned.
	Add(ctx context.Context, value any) error

	// GetAt tries to retrieve a values at index and returns it on success
	// or returns error on failure.
	GetAt(ctx context.Context, index any) (any, error)

	// Insert adds new value at index.
	Insert(ctx context.Context, value any, index any) error

	// Delete removes collection element at index position.
	Delete(ctx context.Context, index any) error
}

// Record is the optional structural capability of a Value (ADR-011 v.6
// §2.9.1): a string-keyed, heterogeneous, insertion-ordered set of fields. A
// Value that implements Record is navigable by ".field" path steps; a value's
// kind (scalar / list / record) is discovered by type assertion, exactly as
// with Collection — a scalar implements neither and is a path leaf.
type Record interface {
	Value

	// Keys lists the field names in insertion order.
	Keys() []string

	// Field returns the named field's value, or a classified
	// errs.ObjectNotFound error when the field is absent.
	Field(ctx context.Context, name string) (Value, error)

	// SetField sets (adds or replaces) the named field. The implementation
	// enforces its own shape: the dynamic values.Record accepts new fields; a
	// typed adapter (S4) rejects unknown names. The name must be CheckName-legal
	// so a field is always addressable by a structural path.
	SetField(ctx context.Context, name string, v Value) error
}

// ChangeType classifies a committed data change. It is the commit-diff's
// change-kind vocabulary (ADR-011 v.6 §2.9.4, wired in the S3 slice): each
// diff entry is a (path, ChangeType) pair. The string values are mirrored by
// the observability DataChange phases (ADR-013 v.2), so the wire names stay
// aligned across both.
type ChangeType string

const (
	// ValueUpdated represents a value update change type.
	ValueUpdated ChangeType = "Value_Updated"
	// ValueAdded represents a value added change type.
	ValueAdded ChangeType = "Value_Added"
	// ValueDeleted represents a value deleted change type.
	ValueDeleted ChangeType = "Value_Deleted"
)
