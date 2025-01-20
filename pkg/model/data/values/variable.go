package values

import (
	"context"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
)

type Variable[T any] struct {
	lock sync.Mutex

	value T

	// for data.Updater
	evtUpdaters map[string]data.UpdateCallback
}

// NewVariable creates a new variable of type T.
func NewVariable[T any](value T) *Variable[T] {
	return &Variable[T]{
		value:       value,
		evtUpdaters: make(map[string]data.UpdateCallback),
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

	v.notify()

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
// data.Updater interface

// Register registers single Value's updating event callback function.
func (v *Variable[T]) Register(regName string, updFn data.UpdateCallback) error {
	if updFn == nil {
		return errs.New(
			errs.M("empty update function"),
			errs.C(errorClass, errs.InvalidParameter))
	}

	regName = strings.Trim(regName, " ")
	if regName == "" {
		return errs.New(
			errs.M("registration name couldn't be empty"),
			errs.C(errorClass, errs.InvalidParameter))
	}

	v.lock.Lock()
	defer v.lock.Unlock()

	if _, ok := v.evtUpdaters[regName]; ok {
		return errs.New(
			errs.M("registration "+regName+" alreday exists"),
			errs.C(errorClass, errs.DuplicateObject))
	}

	v.evtUpdaters[regName] = updFn

	return nil
}

// Unregister deletes previously made registration.
func (v *Variable[T]) Unregister(regName string) {
	v.lock.Lock()
	defer v.lock.Unlock()

	delete(v.evtUpdaters, regName)
}

// notify prepares a list of updaters to call them after
// Value has changed.
func (v *Variable[T]) notify() {
	upff := []data.UpdateCallback{}

	for _, f := range v.evtUpdaters {
		upff = append(upff, f)
	}

	if len(upff) > 0 {
		go sendVariableUpdates(time.Now(), upff)
	}
}

// calls all the registered at the moment callbacks
// to inform that value changed.
// Due to there is no restriction for the time of processing every
// notification, sendUpdates runs as goroutine.
func sendVariableUpdates(when time.Time, funcs []data.UpdateCallback) {
	for _, f := range funcs {
		f(when, data.ValueUpdated, -1)
	}
}

// *****************************************************************************
// check implementation of data.Value and data.Updater interface
var (
	varInterfaceChecker *Variable[bool]
	_                   data.Value   = varInterfaceChecker
	_                   data.Updater = varInterfaceChecker
)
