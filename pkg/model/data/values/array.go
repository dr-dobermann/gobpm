package values

import (
	"fmt"
	"io"
	"reflect"
	"strconv"
	"sync"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
)

// Array is a implementation of the data.Collection and data.Value interfaces.
type Array[T any] struct {
	lock sync.Mutex

	elements []T
	index    int
}

// NewArray creates a new Array of T type, fill it with values and
// returns its pointer
func NewArray[T any](values ...T) *Array[T] {
	a := Array[T]{
		elements: make([]T, len(values)),
		index:    -1,
	}

	if len(values) > 0 {
		copy(a.elements, values)
		a.index = 0
	}

	return &a
}

// *********************** Value interface *************************************
// Get returns value of the Value.
// For collection Get retrieves element with current index
// if collection is empty then panic will be fired.
func (a *Array[T]) Get() any {
	a.lock.Lock()
	defer a.lock.Unlock()

	if a.index < 0 {
		panic("collection is empty")
	}

	return a.elements[a.index]
}

// Update sets new value of the Value.
// For collection Update changes element with current index
// if collection is empty then panic will be fired.
func (a *Array[T]) Update(value any) error {
	a.lock.Lock()
	defer a.lock.Unlock()

	if a.index < 0 {
		return &errs.ApplicationError{
			Message: "collection is empty",
			Classes: []string{
				errorClass,
				errs.EmptyCollectionError,
			},
		}
	}

	v, err := checkValue[T](value)
	if err != nil {
		return err
	}

	a.elements[a.index] = v

	return nil
}

// Type returns string representation of the Value's type.
func (a *Array[T]) Type() string {
	var v T

	return reflect.TypeOf(v).Name()
}

// ********************* Collection interface **********************************

// Count returns legth of the collection.
func (a *Array[T]) Count() int {
	a.lock.Lock()
	defer a.lock.Unlock()

	return len(a.elements)
}

// Rewind sets current index in collection to 0.
func (a *Array[T]) Rewind() {
	a.lock.Lock()
	defer a.lock.Unlock()

	if a.index < 0 {
		return
	}

	a.index = 0
}

// GoTo sets collection current index to desired position.
// first element has 0 index.
func (a *Array[T]) GoTo(index any) error {
	a.lock.Lock()
	defer a.lock.Unlock()

	idx, err := checkValue[int](index)
	if err != nil {
		return err
	}

	if idx < 0 {
		idx = len(a.elements) + idx
	}

	if err := checkIndex[T](idx, a); err != nil {
		return err
	}

	a.index = idx

	return nil
}

// Next shifts current index of the collection for given distance.
// if distance is negative then index shifted backwards.
func (a *Array[T]) Next(dir data.Direction) error {
	a.lock.Lock()
	defer a.lock.Unlock()

	idx := a.index

	if dir == data.StepForward {
		idx++
	} else {
		idx--
	}

	if err := checkIndex[T](idx, a); err != nil {
		return err
	}

	if idx == len(a.elements) {
		return io.EOF
	}

	a.index = idx

	return nil
}

// GetAll returns all values of the collection.
func (a *Array[T]) GetAll() []any {
	a.lock.Lock()
	defer a.lock.Unlock()

	res := make([]any, len(a.elements))
	for i, e := range a.elements {
		res[i] = e
	}

	return res
}

// GetKeys returns a list of keys
func (a *Array[T]) GetKeys() []any {
	a.lock.Lock()
	defer a.lock.Unlock()

	res := make([]any, len(a.elements))
	for i := 0; i < len(a.elements); i++ {
		res = append(res, i)
	}

	return res
}

// Index returns current index in the collection.
// Index is -1 on empty collection.
func (a *Array[T]) Index() any {
	a.lock.Lock()
	defer a.lock.Unlock()

	return a.index
}

// Clear removes all elements in the collection and
// sets index to -1.
func (a *Array[T]) Clear() {
	a.lock.Lock()
	defer a.lock.Unlock()

	a.elements = []T{}
	a.index = -1
}

// Add adds new value into the end of the collection.
// If there is any problem occured, then error returned.
func (a *Array[T]) Add(value any) error {
	a.lock.Lock()
	defer a.lock.Unlock()

	v, err := checkValue[T](value)
	if err != nil {
		return err
	}

	a.elements = append(a.elements, v)

	if a.index < 0 {
		a.index = 0
	}

	return nil
}

// GetAt tries to retrieve a values at index and returns it on success
// or returns error on failure.
func (a *Array[T]) GetAt(index any) (any, error) {
	a.lock.Lock()
	defer a.lock.Unlock()

	idx, err := checkValue[int](index)
	if err != nil {
		var v T

		return v, err
	}

	if err := checkIndex[T](idx, a); err != nil {
		var emptyValue T

		return emptyValue, err
	}

	return a.elements[idx], nil
}

// Insert adds new value at index.
func (a *Array[T]) Insert(value any, index any) error {
	a.lock.Lock()
	defer a.lock.Unlock()

	v, err := checkValue[T](value)
	if err != nil {
		return err
	}

	idx, err := checkValue[int](index)
	if err != nil {
		return err
	}

	if err := checkIndex[T](idx, a); err != nil {
		return err
	}

	a.elements = append(a.elements[:idx],
		append([]T{v}, a.elements[idx:]...)...)

	return nil
}

// Delete removes collection element on index position.
func (a *Array[T]) Delete(index any) error {
	a.lock.Lock()
	defer a.lock.Unlock()

	idx, err := checkValue[int](index)
	if err != nil {
		return err
	}

	if err := checkIndex[T](idx, a); err != nil {
		return err
	}

	a.elements = append(a.elements[:idx], a.elements[idx+1:]...)

	if len(a.elements) == 0 {
		a.index = -1

		return nil
	}

	if a.index >= len(a.elements) {
		a.index = len(a.elements) - 1
	}

	return nil
}

// checkIndex tests if collection is empty and is index is in elements range.
// on failure it returns error of EmptyCollectionError or OutOfRangeError class.
func checkIndex[T any](index int, a *Array[T]) error {
	// check if collection is empty
	if a.index < 0 {
		return &errs.ApplicationError{
			Message: "collection is empty",
			Classes: []string{
				errorClass,
				errs.EmptyCollectionError,
			},
		}
	}

	if index < 0 || index > len(a.elements)-1 {
		return &errs.ApplicationError{
			Message: fmt.Sprintf("index %d is out of range", index),
			Classes: []string{
				errorClass,
				errs.OutOfRangeError,
			},
			Details: map[string]string{
				"max_index": strconv.Itoa(len(a.elements) - 1),
			},
		}
	}

	return nil
}

// checkValue tries to cast value from any to T and returns casted value
// on success or error on failure.
func checkValue[T any](value any) (T, error) {
	v, ok := value.(T)
	if !ok {
		var v T
		return v,
			&errs.ApplicationError{
				Message: fmt.Sprintf(
					"value ( %v ) isn't a value of type %q", value,
					reflect.TypeOf(v).Name()),
				Classes: []string{
					errorClass,
					errs.TypeCastingError,
				},
			}
	}

	return v, nil
}

var array *Array[int]
var _ data.Collection = array
