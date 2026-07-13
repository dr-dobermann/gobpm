package values

import (
	"context"
	"reflect"
	"sync"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
)

// Variable represents a generic variable value.
type Variable[T any] struct {
	value T
	lock  sync.Mutex
}

// NewVariable creates a new variable of type T.
func NewVariable[T any](value T) *Variable[T] {
	return &Variable[T]{
		value: value,
	}
}

// Get returns value of the Value.
func (v *Variable[T]) Get(_ context.Context) any {
	v.lock.Lock()
	defer v.lock.Unlock()

	return v.value
}

// Lock locks Value's internal mutex in case user need to update internal
// Value throug its pointer.
func (v *Variable[T]) Lock() {
	v.lock.Lock()
}

// Unlock unlocks internal Value's mutex.
func (v *Variable[T]) Unlock() {
	v.lock.Unlock()
}

// Update sets new value of the Value.
// For collection Update changes element with current index
// if collection is empty then panic will be fired.
func (v *Variable[T]) Update(_ context.Context, value any) error {
	val, err := checkValue[T](value)
	if err != nil {
		return err
	}

	v.lock.Lock()
	defer v.lock.Unlock()

	v.value = val

	return nil
}

// Type returns string representation of the Value's type.
func (v *Variable[T]) Type() string {
	return reflect.TypeOf(v.value).Name()
}

// Clone creates a clone of the Variable with same value.
func (v *Variable[T]) Clone() data.Value {
	v.lock.Lock()
	defer v.lock.Unlock()

	return NewVariable[T](v.value)
}

// *****************************************************************************
// check implementation of the data.Value interface
var (
	varInterfaceChecker *Variable[bool]
	_                   data.Value = varInterfaceChecker
)
