package values

import (
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
)

// In this file is a collection of typed methods of Array[T]

// GetT is a typed version of Get.
func (a *Array[T]) GetT() T {
	a.lock.Lock()
	defer a.lock.Unlock()

	if a.index < 0 {
		errs.Panic("collection is empty")
	}

	return a.elements[a.index]
}

// GetP returns the pointer of the Value's value.
// It could be used for direct update of the value.
// To guarantee of the thread safety use Lock/Unlock of the
// Value.
func (a *Array[T]) GetP() *T {
	a.lock.Lock()
	defer a.lock.Unlock()

	if a.index < 0 {
		errs.Panic("collection is empty")
	}

	return &a.elements[a.index]
}

// UpdateT is a typed version of Update.
func (a *Array[T]) UpdateT(value T) error {
	a.lock.Lock()
	defer a.lock.Unlock()

	if a.index < 0 {
		return errs.New(
			errs.M("collection is empty"),
			errs.C(errorClass, errs.EmptyCollectionError))
	}

	a.elements[a.index] = value

	a.notify(data.ValueUpdated, a.index)

	return nil
}

// GoToT is a typed version of GoTo.
// if index is negative, then collection index goes backward on
// index steps.
func (a *Array[T]) GoToT(index int) error {
	a.lock.Lock()
	defer a.lock.Unlock()

	if index < 0 {
		index = len(a.elements) + index
	}

	if err := checkIndex[T](index, a); err != nil {
		return err
	}

	a.index = index

	return nil
}

// GetAllT is a typed version of GetAll.
func (a *Array[T]) GetAllT() []T {
	a.lock.Lock()
	defer a.lock.Unlock()

	return append([]T{}, a.elements...)
}

// GetKeysT is a typed version of GetKeys.
func (a *Array[T]) GetKeysT() []int {
	a.lock.Lock()
	defer a.lock.Unlock()

	res := make([]int, len(a.elements))
	for i := 0; i < len(a.elements); i++ {
		res = append(res, i)
	}

	return res
}

// IndexT is a typed version of Index.
func (a *Array[T]) IndexT() int {
	a.lock.Lock()
	defer a.lock.Unlock()

	return a.index
}

// AddT is a typed version of Add.
func (a *Array[T]) AddT(value T) error {
	a.lock.Lock()
	defer a.lock.Unlock()

	a.elements = append(a.elements, value)

	if a.index < 0 {
		a.index = 0
	}

	a.notify(data.ValueAdded, len(a.elements)-1)

	return nil
}

// GetAtT is typed version of GetAt.
func (a *Array[T]) GetAtT(index int) (T, error) {
	a.lock.Lock()
	defer a.lock.Unlock()

	if err := checkIndex[T](index, a); err != nil {
		var emptyValue T

		return emptyValue, err
	}

	return a.elements[index], nil
}

// InsertT is a typed version of Insert.
func (a *Array[T]) InsertT(value T, index int) error {
	a.lock.Lock()
	defer a.lock.Unlock()

	if err := checkIndex[T](index, a); err != nil {
		return err
	}

	a.elements = append(a.elements[:index],
		append([]T{value}, a.elements[index:]...)...)

	a.notify(data.ValueAdded, index)

	return nil
}

// DeleteT is a typed version of Delete.
func (a *Array[T]) DeleteT(index int) error {
	a.lock.Lock()
	defer a.lock.Unlock()

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

	a.notify(data.ValueDeleted, index)

	return nil
}
