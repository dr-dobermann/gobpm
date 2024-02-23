package data

type Value interface {
	// Get returns value of the Value.
	// For collection Get retrieves element with current index
	// if collection is empty then panic will be fired.
	Get() any

	// Update sets new value of the Value.
	// For collection Update changes element with current index
	// if collection is empty then panic will be fired.
	Update(any) error

	// Type returns string representation of the Value's type.
	Type() string
}

type Collection interface {
	// Len returns legth of the collection.
	Len() int

	// Rewind sets current index in collection to 0.
	Rewind()

	// GoTo sets collection current index to desired position.
	// first element has 0 index.
	GoTo(position int) error

	// Next shifts current index of the collection for given distance.
	// if distance is negative then index shifted backwards.
	Next(distance int) error

	// GetAll returns all values of the collection.
	GetAll() []any

	// Index returns current index in the collection.
	Index() int

	// Clear removes all elements in the collection and
	// sets index to 0.
	Clear()

	// Add adds new value into the end of the collection.
	Add(value any)

	// GetAt tries to retrieve a values at index and returns it on success
	// or returns error on failure.
	GetAt(index int) (any, error)

	// Insert adds new value after index.
	Insert(value any, index int) error

	// Delete removes collection element on index position.
	Delete(index int) error
}
