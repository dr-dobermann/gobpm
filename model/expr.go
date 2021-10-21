package model

import (
	"strconv"

	"github.com/google/uuid"
)

type VarType uint8

const (
	VtInt VarType = iota
	VtBool
	VtString
	VtFloat
)

func (vt VarType) String() string {
	return []string{"Int", "Bool", "String", "Float"}[vt]
}

type Variable struct {
	BaseElement
	name        string
	initialized bool
	vtype       VarType
	value       interface{}
	// float precision. Default 2
	prec int
}

func (v *Variable) Name() string {
	return v.name
}

func (v *Variable) Type() VarType {
	return v.vtype
}

func (v *Variable) Precision() int {
	return v.prec
}

func (v *Variable) SetPrecision(p int) {
	if p < 0 {
		p = 0
	}
	v.prec = p
}

func (v *Variable) Int() int {
	switch v.vtype {
	case VtString:
		s := v.value.(string)

		if i, err := strconv.ParseInt(s, 10, 64); err != nil {
			panic("cannot convert string var " + v.name +
				"[" + s + "] to int" + err.Error())
		} else {
			return int(i)
		}

	case VtFloat:
		f := v.value.(float64)
		return int(f)

	}

	i := v.value.(int)

	return i
}

func (v *Variable) String() string {
	var s string

	switch v.vtype {
	case VtInt:
		i := v.value.(int)
		s = strconv.Itoa(i)

	case VtBool:
		b := v.value.(bool)
		if b {
			s = "true"
		} else {
			s = "false"
		}

	case VtFloat:
		f := v.value.(float64)
		s = strconv.FormatFloat(f, 'f', v.prec, 64)

	case VtString:
		s = v.value.(string)
	}

	return s
}

func (v *Variable) Bool() bool {
	var b bool

	switch v.vtype {
	case VtInt:
		i := v.value.(int)
		if i != 0 {
			b = true
		}

	case VtBool:
		b = v.value.(bool)

	case VtFloat:
		f := v.value.(float64)
		if f == 0.0 {
			b = false
		}

	case VtString:
		s := v.value.(string)
		if len(s) > 0 {
			b = true
		}
	}

	return b
}

func (v *Variable) Float64() float64 {
	var (
		f   float64
		err error
	)

	switch v.vtype {
	case VtInt:
		i := v.value.(int)
		f = float64(i)

	case VtBool:
		b := v.value.(bool)
		if b {
			f = 1.0
		}

	case VtFloat:
		f = v.value.(float64)

	case VtString:
		s := v.value.(string)
		f, err = strconv.ParseFloat(s, 64)
		if err != nil {
			panic("couldn't transform string " +
				s + " into float64 : " + err.Error())
		}
	}

	return f
}

type VPack struct {
	BaseElement
	name string
	vars map[string]*Variable
}

func (v *VPack) Name() string {
	return v.name
}

type Expression struct {
	NamedElement
	language string // Formal Expression language (FEEL) in URI format
	body     string // in future it could be changed to another specialized type or
	// realized by interface
	retType string // TODO: should be changed to standard go type in the future
}

type varMap map[VarType]*Variable

// varStore retpresents the variables store
// there is one global store and stores in every single process instance
// variables could have the same name but they should be different by value type
type VarStore map[string]varMap

var global VarStore = make(VarStore)

func (vs *VarStore) getVar(vn string, vt VarType, returnEmpty bool) *Variable {
	vm := map[string]varMap(*vs)[vn]
	if vm == nil {
		if !returnEmpty {
			return nil
		}

		vm = make(varMap)
		map[string]varMap(*vs)[vn] = vm
	}

	v, ok := map[VarType]*Variable(vm)[vt]

	if !ok {
		if !returnEmpty {
			return nil
		}

		v = &Variable{
			BaseElement: BaseElement{
				id:            NewID(),
				Documentation: Documentation{"", ""}},
			name:  vn,
			vtype: vt,
			prec:  2}

		map[VarType]*Variable(vm)[vt] = v
	}

	return v
}

func (vs *VarStore) GetVar(vn string, vt VarType) (*Variable, error) {
	v := vs.getVar(vn, vt, false)
	if v == nil {
		return nil, NewModelError(uuid.Nil,
			"variable "+vn+" of type "+vt.String()+" isn't found", nil)
	}

	return v, nil
}

func (vs *VarStore) DelVar(vn string, vt VarType) error {
	vm := map[string]varMap(*vs)[vn]
	if vm == nil {
		return NewModelError(uuid.Nil,
			"couldn't find variable group "+vn,
			nil)
	}
	if _, ok := map[VarType]*Variable(vm)[vt]; !ok {
		return NewModelError(uuid.Nil,
			"couldn't find variable "+vn+" of type "+vt.String(),
			nil)
	}
	delete(map[VarType]*Variable(vm), vt)

	// if there is no variables in var map, then delete the
	// variable map itself
	if len(vm) == 0 {
		delete(map[string]varMap(*vs), vn)
	}

	return nil
}

// NewInt creates a new int variable
// if the variable exists, the error returned
func (vs *VarStore) NewInt(vn string, val int) (*Variable, error) {
	v := vs.getVar(vn, VtInt, true)

	if v.initialized {
		return nil, NewModelError(uuid.Nil,
			"variable "+vn+" of Int type already exists",
			nil)
	}

	v.value = val
	v.initialized = true

	return v, nil
}

func (vs *VarStore) NewBool(vn string, val bool) (*Variable, error) {
	v := vs.getVar(vn, VtBool, true)

	if v.initialized {
		return nil, NewModelError(uuid.Nil,
			"variable "+vn+" of Bool type already exists",
			nil)
	}

	v.value = val
	v.initialized = true

	return v, nil
}

func (vs *VarStore) NewString(vn string, val string) (*Variable, error) {
	v := vs.getVar(vn, VtString, true)

	if v.initialized {
		return nil, NewModelError(uuid.Nil,
			"variable "+vn+" of String type already exists",
			nil)
	}

	v.value = val
	v.initialized = true

	return v, nil
}

func (vs *VarStore) NewFloat(vn string, val float64) (*Variable, error) {
	v := vs.getVar(vn, VtFloat, true)

	if v.initialized {
		return nil, NewModelError(uuid.Nil,
			"variable "+vn+" of Float type already exists",
			nil)
	}

	v.value = val
	v.initialized = true

	return v, nil
}
