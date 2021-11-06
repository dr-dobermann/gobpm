package model

import (
	"fmt"
	"math"
	"strconv"
	"time"
)

type VarType uint8

const (
	VtInt VarType = iota
	VtBool
	VtString
	VtFloat
	VtTime
)

func (vt VarType) String() string {
	return []string{"Int", "Bool", "String", "Float", "Time"}[vt]
}

type Variable struct {
	name  string
	vtype VarType
	value interface{}
	// float precision. Default 2
	prec int
}

// V creates new variable
func V(n string, t VarType, v interface{}) *Variable {
	return &Variable{n, t, v, 2}
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
			return NewModelError(fmt.Sprintf("couldn't convert %v to int", newVal), nil)
		} else {
			v.value = i
		}

	case VtBool:
		if b, ok := newVal.(bool); !ok {
			return NewModelError(fmt.Sprintf("couldn't convert %v to bool", newVal), nil)
		} else {
			v.value = b
		}

	case VtString:
		if s, ok := newVal.(string); !ok {
			return NewModelError(fmt.Sprintf("couldn't convert %v to string", newVal), nil)
		} else {
			v.value = s
		}

	case VtFloat:
		if f, ok := newVal.(float64); !ok {
			return NewModelError(fmt.Sprintf("couldn't convert %v to float64", newVal), nil)
		} else {
			v.value = f
		}

	case VtTime:
		if t, ok := newVal.(time.Time); !ok {
			return NewModelError(fmt.Sprintf("couldn't convert %v to Time", newVal), nil)
		} else {
			v.value = t
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

	case VtTime:
		t := v.value.(time.Time)
		i = int(t.Unix())
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

	case VtTime:
		t := v.value.(time.Time)
		s = t.String()
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

	case VtTime:
		f = 0.0
	}

	return f
}

// varStore retpresents the variables store
type VarStore map[string]*Variable

func (vs *VarStore) checkVar(vn string) bool {
	_, ok := map[string]*Variable(*vs)[vn]

	return ok
}

func (vs *VarStore) getVar(vn string, vt VarType, returnEmpty bool) *Variable {

	v, ok := map[string]*Variable(*vs)[vn]

	if !ok {
		if !returnEmpty {
			return nil
		}

		v = &Variable{
			name:  vn,
			vtype: vt,
			prec:  2}

		map[string]*Variable(*vs)[vn] = v
	}

	return v
}

// GetVar returns variable vn of tyep vt form namespace vs
// if the variable isn't found, then error returned
func (vs *VarStore) GetVar(vn string) (*Variable, error) {
	v := vs.getVar(vn, VtInt, false)
	if v == nil {
		return nil, NewModelError("variable "+vn+
			" isn't found", nil)
	}

	return v, nil
}

// DelVar deletes variable vn of type vt from namespace vs
// if there is no such variable, then error returned
func (vs *VarStore) DelVar(vn string) error {
	if !vs.checkVar(vn) {
		return NewModelError("couldn't find variable "+vn, nil)
	}
	delete(map[string]*Variable(*vs), vn)

	return nil
}

func (vs *VarStore) Update(vn string, newVal interface{}) error {
	v := vs.getVar(vn, VtInt, false)
	if v == nil {
		return NewModelError("couldn't find variable "+vn, nil)
	}

	return v.update(newVal)
}

// NewInt creates a new int variable in namespace vs
// if the variable with the same name and the same type exists, the error returned
func (vs *VarStore) NewInt(vn string, val int) (*Variable, error) {
	if vs.checkVar(vn) {
		return nil, NewModelError("variable "+vn+" already exists",
			nil)
	}

	v := vs.getVar(vn, VtInt, true)
	v.value = val

	return v, nil
}

// NewBool creates a new bool variable in namespace vs
// if the variable with the same name and the same type exists, the error returned
func (vs *VarStore) NewBool(vn string, val bool) (*Variable, error) {
	if vs.checkVar(vn) {
		return nil, NewModelError("variable "+vn+" already exists",
			nil)
	}

	v := vs.getVar(vn, VtBool, true)
	v.value = val

	return v, nil
}

// NewString creates a new string variable in namespace vs
// if the variable with the same name and the same type exists, the error returned
func (vs *VarStore) NewString(vn string, val string) (*Variable, error) {
	if vs.checkVar(vn) {
		return nil, NewModelError("variable "+vn+" already exists",
			nil)
	}

	v := vs.getVar(vn, VtString, true)
	v.value = val

	return v, nil
}

// NewFloat creates a new string variable in namespace vs
// if the variable with the same name and the same type exists, the error returned
func (vs *VarStore) NewFloat(vn string, val float64) (*Variable, error) {
	if vs.checkVar(vn) {
		return nil, NewModelError("variable "+vn+" already exists",
			nil)
	}

	v := vs.getVar(vn, VtFloat, true)
	v.value = val

	return v, nil
}

func (vs *VarStore) NewTime(vn string, val time.Time) (*Variable, error) {
	if vs.checkVar(vn) {
		return nil, NewModelError("variable "+vn+" already exists",
			nil)
	}

	v := vs.getVar(vn, VtTime, true)
	v.value = val

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

func (e *Expression) Copy() *Expression {
	ec := Expression{
		NamedElement: e.NamedElement,
		language:     e.language,
		body:         e.body,
		etype:        e.etype,
		retType:      e.retType}
	ec.id = NewID()

	return &ec
}
