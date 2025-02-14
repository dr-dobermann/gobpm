package data

import (
	"context"
	"time"
)

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

type StepDirection bool

const (
	StepForward  StepDirection = true
	StepBackward StepDirection = false
)

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

// Updater is an interface for Values, which allows track its-own update events.
// It doesn't provide ability to read updated/new value due to security issues.
// Everyone who has access to value could read it in appropriate way.
type Updater interface {
	// Register registers single Value's updating event callback function.
	// If registration failed error returned.
	Register(regName string, updFunc UpdateCallback) error

	// Unregister deletes previously made registration.
	Unregister(regName string)
}

// Registered function which will be called as soon ad Value changed.
// Due to there is no any warranty about right order of the
// Value updates notification, time of update is provided.
// if you got any notification after the one you're already process, just
// ignore them.
// If data was changed in the collection, then index will be filled.
type UpdateCallback func(when time.Time, changeType ChangeType, index any)

// ChangeType indicates the type of Value's change.
type ChangeType string

const (
	ValueUpdated ChangeType = "Value_Updated"
	ValueAdded   ChangeType = "Value_Added"
	ValueDeleted ChangeType = "Value_Deleted"
)
