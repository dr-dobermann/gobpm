package model

import (
	"fmt"
	"math"
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
		p = 2
	}
	v.prec = p
}

func (v *Variable) update(newVal interface{}) error {
	switch v.vtype {
	case VtInt:
		if i, ok := newVal.(int); !ok {
			return NewModelError(uuid.Nil, fmt.Sprintf("couldn't convert %v to int", newVal), nil)
		} else {
			v.value = i
		}

	case VtBool:
		if b, ok := newVal.(bool); !ok {
			return NewModelError(uuid.Nil, fmt.Sprintf("couldn't convert %v to bool", newVal), nil)
		} else {
			v.value = b
		}

	case VtString:
		if s, ok := newVal.(string); !ok {
			return NewModelError(uuid.Nil, fmt.Sprintf("couldn't convert %v to string", newVal), nil)
		} else {
			v.value = s
		}

	case VtFloat:
		if f, ok := newVal.(float64); !ok {
			return NewModelError(uuid.Nil, fmt.Sprintf("couldn't convert %v to float64", newVal), nil)
		} else {
			v.value = f
		}

	}

	return nil
}

// Int returns a integer representation of variable v.
// if v is the VtString and converstion errror from string to float64 happened
// then panic fired
func (v *Variable) Int() int {
	var i int

	switch v.vtype {
	case VtInt:
		i = v.value.(int)

	case VtBool:
		b := v.value.(bool)
		if b {
			i = 1
		} else {
			i = 0
		}

	case VtString:
		s := v.value.(string)
		if f, err := strconv.ParseFloat(s, 64); err != nil {
			panic("cannot convert string var " + v.name +
				"[" + s + "] to float64" + err.Error())
		} else {
			i = int(math.Round(f))
		}

	case VtFloat:
		f := v.value.(float64)
		i = int(f)
	}

	return i
}

// String returns a string representation of variable v.
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

// Bool returns a boolean representation of variable v.
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
		if f != 0.0 {
			b = true
		}

	case VtString:
		s := v.value.(string)
		if len(s) > 0 {
			b = true
		}
	}

	return b
}

// Float64 returns a float64 representation of variable v.
// if v is the VtString and converstion errror from string to float64 happened
// then panic fired
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

// GetVar returns variable vn of tyep vt form namespace vs
// if the variable isn't found, then error returned
func (vs *VarStore) GetVar(vn string, vt VarType) (*Variable, error) {
	v := vs.getVar(vn, vt, false)
	if v == nil {
		return nil, NewModelError(uuid.Nil,
			"variable "+vn+" of type "+vt.String()+" isn't found", nil)
	}

	return v, nil
}

// DelVar deletes variable vn of type vt from namespace vs
// if there is no such variable, then error returned
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

func (vs *VarStore) Update(vn string, vt VarType, newVal interface{}) error {
	v := vs.getVar(vn, vt, false)

	if v == nil {
		return NewModelError(uuid.Nil,
			"couldn't find variable "+vn+" of type "+vt.String(),
			nil)
	}

	return v.update(newVal)
}

// NewInt creates a new int variable in namespace vs
// if the variable with the same name and the same type exists, the error returned
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

// NewBool creates a new bool variable in namespace vs
// if the variable with the same name and the same type exists, the error returned
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

// NewString creates a new string variable in namespace vs
// if the variable with the same name and the same type exists, the error returned
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

// NewFloat creates a new string variable in namespace vs
// if the variable with the same name and the same type exists, the error returned
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

type ExpressionType uint8

const (
	ExTEmbedded ExpressionType = iota
)

type Expression struct {
	NamedElement
	language   string // Formal Expression language (FEEL) in URI format
	body       string
	etype      ExpressionType
	retType    VarType
	calculated bool
}

func (e Expression) Type() ExpressionType {
	return e.etype
}
