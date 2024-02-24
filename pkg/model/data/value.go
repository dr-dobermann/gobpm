package data

type Value interface {
	// Get returns copy of the Value's value.
	// For collection Get retrieves element with current index
	// if collection is empty then panic will be fired.
	Get() any

	// Update sets new value of the Value.
	// For collection Update changes element with current index
	// if collection is empty then panic will be fired.
	Update(any) error

	// Lock locks Value's internal mutex in case user need to update internal
	// Value throug its pointer.
	Lock()

	// Unlock unlocks internal Value's mutex.
	Unlock()

	// Type returns string representation of the Value's type.
	Type() string
}

type Direction bool

const (
	StepForward  Direction = true
	StepBackward Direction = false
)

type Collection interface {
	// Count returns legth of the collection.
	Count() int

	// Rewind sets current index in collection to start position
	Rewind()

	// GoTo sets collection current index to desired position.
	// first element has 0 index.
	GoTo(position any) error

	// Next shifts current index of the collection for given distance.
	// if distance is negative then index shifted backwards.
	Next(dir Direction) error

	// GetAll returns all values of the collection.
	GetAll() []any

	// GetKeys returns a list of keys
	GetKeys() []any

	// Index returns current index in the collection.
	Index() any

	// Clear removes all elements in the collection and
	// sets index to 0.
	Clear()

	// Add adds new value into the end of the collection.
	// If there is any problem occured, then error returned.
	Add(value any) error

	// GetAt tries to retrieve a values at index and returns it on success
	// or returns error on failure.
	GetAt(index any) (any, error)

	// Insert adds new value at index.
	Insert(value any, index any) error

	// Delete removes collection element at index position.
	Delete(index any) error
}
