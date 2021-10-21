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
)

type Variable struct {
	BaseElement
	name        string
	initialized bool
	vtype       VarType
	value       interface{}
}

func (v *Variable) Name() string {
	return v.name
}

func (v *Variable) Type() VarType {
	return v.vtype
}

func (v *Variable) Int() int {
	if v.vtype == VtString {
		s := v.value.(string)

		if i, err := strconv.ParseInt(s, 10, 64); err != nil {
			panic("cannot convert string var " + v.name +
				"[" + s + "] to int" + err.Error())
		} else {
			return int(i)
		}
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

	case VtString:
		s := v.value.(string)
		if len(s) > 0 {
			b = true
		}
	}

	return b
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
type varStore map[string]varMap

func (vs *varStore) getVar(vn string, vt VarType) *Variable {

	vm := map[string]varMap(*vs)[vn]
	if vm == nil {
		vm = make(varMap)
	}

	v := map[VarType]*Variable(vm)[vt]
	if v == nil {
		v = &Variable{
			BaseElement: BaseElement{
				id:            NewID(),
				Documentation: Documentation{"", ""}},
			name: vn}
	}

	return v
}

var global varStore = make(varStore)

// NewInt creates a new int variable
// if the variable exists, the error returned
func (vs *varStore) NewInt(vn string, val int) (*Variable, error) {

	v := global.getVar(vn, VtInt)

	if v.initialized {
		return nil, NewModelError(uuid.Nil,
			"variable "+vn+" of int type already exists",
			nil)
	}

	v.value = val
	v.initialized = true

	return v, nil
}
