package values

// This file consists of typed function for Variable.

// GetT returns typed value of the Value.
func (v *Variable[T]) GetT() T {
	v.lock.Lock()
	defer v.lock.Unlock()

	return v.value
}

// GetP returns the pointer of the Value's value.
// It could be used for direct update of the value. To guarantee of the thread
// safety use Lock/Unlock of the Value.
//
// DO NOT TRY to update inner value on GetP call as followed:
//    v.Lock()
//    v.GetP().inner_value = new_value
//    v.Unlock()
// BECAUSE IT LEADS to DEADLOCK
//
// Use followed pattern:
//    pv := v.GetP()
//    v.Lock()
//    pv.inner_value = new_value
//    v.Unlock()
func (v *Variable[T]) GetP() *T {
	v.lock.Lock()
	defer v.lock.Unlock()

	return &v.value
}

// UpdateT sets new typed value of the Value.
func (v *Variable[T]) UpdateT(value T) error {
	v.lock.Lock()
	defer v.lock.Unlock()

	v.value = value

	v.notify()

	return nil
}
