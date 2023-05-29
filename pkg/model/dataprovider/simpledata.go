package dataprovider

import (
	"fmt"
)

// SimpleDataItem is a type for creation of simple DataItem values.
// if you need to have DataItem for an int, string, float, bool variable
// just create SimpleDataItem with int or any other simple type and use this
// object wherever you need DataItem.
type SimpleDataItem[T any] struct {
	value T
}

func NewSimpleDataItem[T any](v T) *SimpleDataItem[T] {

	return &SimpleDataItem[T]{v}
}

func (sdi *SimpleDataItem[T]) Get() T {
	return sdi.value
}

func (sdi *SimpleDataItem[T]) Set(nv T) {
	sdi.value = nv
}

// ============== DataItem interface implementation ============================
func (sdi *SimpleDataItem[T]) IsCollection() bool {

	return false
}

func (sdi *SimpleDataItem[T]) Len() int {

	return 1
}

func (sdi *SimpleDataItem[T]) Copy() DataItem {
	return &SimpleDataItem[T]{sdi.value}
}

func (sdi *SimpleDataItem[T]) GetValue() map[string]interface{} {

	return map[string]interface{}{"value": sdi.value}
}

func (sdi *SimpleDataItem[T]) UpdateValue(nv map[string]interface{}) error {

	v, ok := nv["value"]
	if !ok {
		return fmt.Errorf("there is no value field in Update param")
	}

	vv, ok := v.(T)
	if !ok {
		return fmt.Errorf("couldn't convert to destination type")
	}

	sdi.value = vv

	return nil
}

func (sdi *SimpleDataItem[T]) GetGuts() interface{} {
	return sdi
}
