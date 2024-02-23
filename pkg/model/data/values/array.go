package values

import (
	"fmt"
	"io"
	"reflect"
	"strconv"

	"github.com/dr-dobermann/gobpm/pkg/errs"
)

// Array is a implementation of the data.Collection and data.Value interfaces.
// Array isn't thread safe and created only for testing and demonstrating
// purposes.
// Thread safety should be added when needed.
type Array[T any] struct {
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
	if a.index < 0 {
		panic("collection is empty")
	}

	return a.elements[a.index]
}

// Update sets new value of the Value.
// For collection Update changes element with current index
// if collection is empty then panic will be fired.
func (a *Array[T]) Update(value T) error {
	if a.index < 0 {
		return &errs.ApplicationError{
			Message: "collection is empty",
			Classes: []string{
				errorClass,
				errs.EmptyCollectionError,
			},
		}
	}

	a.elements[a.index] = value

	return nil
}

// Type returns string representation of the Value's type.
func (a *Array[T]) Type() string {
	var v T

	return reflect.TypeOf(v).Name()
}

// ********************* Collection interface **********************************

// Len returns legth of the collection.
func (a *Array[T]) Len() int {
	return len(a.elements)
}

// Rewind sets current index in collection to 0.
func (a *Array[T]) Rewind() {
	if a.index < 0 {
		return
	}

	a.index = 0
}

// GoTo sets collection current index to desired position.
// first element has 0 index.
func (a *Array[T]) GoTo(index int) error {
	if index < 0 {
		index = len(a.elements) + index
	}

	if err := checkIndex[T](index, a); err != nil {
		return err
	}

	a.index = index

	return nil
}

// Next shifts current index of the collection for given distance.
// if distance is negative then index shifted backwards.
func (a *Array[T]) Next(distance int) error {
	idx := a.index + distance

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
func (a *Array[T]) GetAll() []T {
	return append([]T{}, a.elements...)
}

// Index returns current index in the collection.
// Index is -1 on empty collection.
func (a *Array[T]) Index() int {
	return a.index
}

// Clear removes all elements in the collection and
// sets index to -1.
func (a *Array[T]) Clear() {
	a.elements = []T{}
	a.index = -1
}

// Add adds new value into the end of the collection.
func (a *Array[T]) Add(value T) {
	a.elements = append(a.elements, value)

	if a.index < 0 {
		a.index = 0
	}
}

// GetAt tries to retrieve a values at index and returns it on success
// or returns error on failure.
func (a *Array[T]) GetAt(index int) (T, error) {
	if err := checkIndex[T](index, a); err != nil {
		var emptyValue T

		return emptyValue, err
	}

	return a.elements[index], nil
}

// Insert adds new value after index.
func (a *Array[T]) Insert(value T, index int) error {
	if err := checkIndex[T](index, a); err != nil {
		return err
	}

	a.elements = append(a.elements[:index], append([]T{value}, a.elements[index:]...)...)

	return nil
}

// Delete removes collection element on index position.
func (a *Array[T]) Delete(index int) error {
	if err := checkIndex[T](index, a); err != nil {
		return err
	}

	a.elements = append(a.elements[:index], a.elements[index+1:]...)

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
