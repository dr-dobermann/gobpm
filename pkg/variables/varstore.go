package variables

import (
	"fmt"
	"sync"
	"time"
)

// varStore retpresents the variables store
type VarStore struct {
	sync.Mutex

	vars map[string]*Variable
}

func NewVarStore() *VarStore {
	return &VarStore{vars: map[string]*Variable{}}
}

func (vs *VarStore) checkVar(vn string) bool {
	vs.Lock()
	defer vs.Unlock()

	_, ok := vs.vars[vn]

	return ok
}

func (vs *VarStore) getVar(vn string, vt Type, returnEmpty bool) *Variable {
	vs.Lock()
	defer vs.Unlock()

	v, ok := vs.vars[vn]

	if !ok {
		if !returnEmpty {
			return nil
		}

		v = &Variable{
			name:  vn,
			vType: vt,
			prec:  defaultPrecision}

		vs.vars[vn] = v
	}

	return v
}

// GetVar returns variable vn of tyep vt form namespace vs
// if the variable isn't found, then error returned
func (vs *VarStore) GetVar(vn string) (*Variable, error) {
	v := vs.getVar(vn, Int, false)
	if v == nil {
		return nil, vs.newVSErr(nil, "variable "+vn+
			" isn't found")
	}

	return v, nil
}

// DelVar deletes variable vn of type vt from namespace vs
// if there is no such variable, then error returned
func (vs *VarStore) DelVar(vn string) error {
	if !vs.checkVar(vn) {
		return vs.newVSErr(nil, "couldn't find variable "+vn)
	}

	vs.Lock()
	defer vs.Unlock()

	delete(vs.vars, vn)

	return nil
}

// Update provides thread-safe VarStore variable updating.
func (vs *VarStore) Update(vn string, newVal interface{}) error {
	v := vs.getVar(vn, Int, false)
	if v == nil {
		return vs.newVSErr(nil, "couldn't find variable "+vn)
	}

	vs.Lock()
	defer vs.Unlock()

	err := v.update(newVal)

	return err
}

// NewInt creates a new int variable in namespace vs
// if the variable with the same name and the same type exists, the error returned
func (vs *VarStore) NewInt(vn string, val int64) (*Variable, error) {
	if vs.checkVar(vn) {
		return nil, vs.newVSErr(nil, "variable "+vn+" already exists")
	}

	v := vs.getVar(vn, Int, true)

	if err := v.update(val); err != nil {
		return nil, err
	}

	return v, nil
}

// NewBool creates a new bool variable in namespace vs
// if the variable with the same name and the same type exists, the error returned
func (vs *VarStore) NewBool(vn string, val bool) (*Variable, error) {
	if vs.checkVar(vn) {
		return nil, vs.newVSErr(nil, "variable "+vn+" already exists")
	}

	v := vs.getVar(vn, Bool, true)

	if err := v.update(val); err != nil {
		return nil, err
	}

	return v, nil
}

// NewString creates a new string variable in namespace vs
// if the variable with the same name and the same type exists, the error returned
func (vs *VarStore) NewString(vn string, val string) (*Variable, error) {
	if vs.checkVar(vn) {
		return nil, vs.newVSErr(nil, "variable "+vn+" already exists")
	}

	v := vs.getVar(vn, String, true)

	if err := v.update(val); err != nil {
		return nil, err
	}

	return v, nil
}

// NewFloat creates a new string variable in namespace vs
// if the variable with the same name and the same type exists, the error returned
func (vs *VarStore) NewFloat(vn string, val float64) (*Variable, error) {
	if vs.checkVar(vn) {
		return nil, vs.newVSErr(nil, "variable "+vn+" already exists")
	}

	v := vs.getVar(vn, Float, true)

	if err := v.update(val); err != nil {
		return nil, err
	}

	return v, nil
}

func (vs *VarStore) NewTime(vn string, val time.Time) (*Variable, error) {
	if vs.checkVar(vn) {
		return nil, vs.newVSErr(nil, "variable "+vn+" already exists")
	}

	v := vs.getVar(vn, Time, true)

	if err := v.update(val); err != nil {
		return nil, err
	}

	return v, nil
}

func (vs *VarStore) NewVar(v Variable) (*Variable, error) {
	if vs.checkVar(v.name) {
		return nil, vs.newVSErr(nil, "variable "+v.name+" already exists")
	}

	vr := vs.getVar(v.name, v.vType, true)

	if err := vr.update(v.value); err != nil {
		return nil, err
	}

	return vr, nil
}

func (vs *VarStore) newVSErr(
	err error,
	format string,
	values ...interface{}) VStoreError {
	return VStoreError{
		msg: fmt.Sprintf(format, values...),
		Err: err}
}
