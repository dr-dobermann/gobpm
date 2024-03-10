package values

import (
	"fmt"
	"io"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
)

// Array is a implementation of the data.Collection and data.Value interfaces.
type Array[T any] struct {
	lock sync.Mutex

	elements []T
	index    int

	// for data.Updater
	evtUpdaters map[string]data.UpdateCallback
}

// NewArray creates a new Array of T type, fill it with values and
// returns its pointer
func NewArray[T any](values ...T) *Array[T] {
	a := Array[T]{
		elements:    make([]T, len(values)),
		index:       -1,
		evtUpdaters: make(map[string]data.UpdateCallback),
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

// Lock locks Value's internal mutex in case user need to update internal
// Value throug its pointer.
func (a *Array[T]) Lock() {
	a.lock.Lock()
}

// Unlock unlocks internal Value's mutex.
func (a *Array[T]) Unlock() {
	a.lock.Unlock()
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

	a.notify(data.ValueUpdated, a.index)

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

	if idx == len(a.elements) {
		return io.EOF
	}

	if err := checkIndex[T](idx, a); err != nil {
		return err
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

	for i := range a.elements {
		a.notify(data.ValueDeleted, i)
	}

	a.elements = []T{}
	a.index = -1
}

// Add adds new value into the end of the collection.
// If there is any problem occurred, then error returned.
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

	a.notify(data.ValueAdded, len(a.elements)-1)

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
func (a *Array[T]) Insert(value, index any) error {
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

	a.notify(data.ValueAdded, idx)

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

	a.notify(data.ValueDeleted, index)

	return nil
}

// checkIndex tests if collection is empty and is index is in elements range.
// on failure it returns error of EmptyCollectionError or OutOfRangeError class.
func checkIndex[T any](index int, a *Array[T]) error {
	if err := checkForEmpty[T](a); err != nil {
		return err
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

func checkForEmpty[T any](a *Array[T]) error {
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

// *****************************************************************************
// data.Updater interface

// Register registers single Value's updating event callback function.
// It doesn't check for duplication and just changed the previously made
// registration.
func (a *Array[T]) Register(regName string, updFn data.UpdateCallback) error {
	if updFn == nil {
		return &errs.ApplicationError{
			Message: "empty updater function",
			Classes: []string{
				errorClass,
				errs.InvalidParameter,
			},
		}
	}

	regName = strings.Trim(regName, " ")
	if regName == "" {
		return &errs.ApplicationError{
			Message: "registration name couldn't be empty",
			Classes: []string{
				errorClass,
				errs.InvalidParameter,
			},
			Details: map[string]string{},
		}
	}

	a.lock.Lock()
	defer a.lock.Unlock()

	if _, ok := a.evtUpdaters[regName]; ok {
		return &errs.ApplicationError{
			Message: "registration " + regName + " alreday exists",
			Classes: []string{
				errorClass,
				errs.InvalidParameter,
			},
		}
	}

	a.evtUpdaters[regName] = updFn

	return nil
}

// Unregister deletes previously made registration.
func (a *Array[T]) Unregister(regName string) {
	a.lock.Lock()
	defer a.lock.Unlock()

	delete(a.evtUpdaters, regName)
}

// notify prepares a list of updaters to call them after
// Value has changed.
func (a *Array[T]) notify(chgType data.ChangeType, idx any) {
	upff := []data.UpdateCallback{}

	for _, f := range a.evtUpdaters {
		upff = append(upff, f)
	}

	if len(upff) > 0 {
		go sendArrayUpdates(time.Now(), chgType, idx, upff)
	}
}

// calls all the registered at the moment callbacks
// to inform that value changed.
// Due to there is no restriction for the time of processing every
// notification, sendUpdates runs as goroutine.
func sendArrayUpdates(when time.Time,
	chgType data.ChangeType,
	idx any,
	funcs []data.UpdateCallback) {
	for _, f := range funcs {
		f(when, chgType, idx)
	}
}

// *****************************************************************************
// check implementation of data.Value, data.Collection and data.Updater
// interfaces.
var arrayInterfaceChecker *Array[int]
var _ data.Collection = arrayInterfaceChecker
var _ data.Value = arrayInterfaceChecker
var _ data.Updater = arrayInterfaceChecker
