package model

import (
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

type variableValues struct {
	i int64
	b bool
	s string
	f float64
	t time.Time
}

// Variable is a variant-based type for using variables in gobpm.
//
// When it converts in|from time.Time it uses Unix Milliseconds value.
//
// Time converts into|from string it uses time.RFC3339
// 2006-01-02T15:04:05Z
// 2006-01-02T15:04:05+07:00
type Variable struct {
	// pre-casted values for eliminating casting on-the-fly.
	// For every type there is only one casted value.
	variableValues

	name  string
	vtype VarType
	value interface{}
	// float precision. Default 2
	prec int
}

// V creates a new variable
func V(n string, t VarType, v interface{}) *Variable {
	vv := &Variable{
		name:  n,
		vtype: t,
		prec:  2}

	vv.update(v)

	return vv
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

func (v *Variable) Value() interface{} {
	vv := v.value

	return vv
}

// update updates a value of the Variable v.
//
// it expected to receive the value of internal type of v.
func (v *Variable) update(newVal interface{}) error {
	switch v.vtype {
	case VtInt:
		if i, ok := newVal.(int64); !ok {
			if i, ok := newVal.(int); !ok {
				return NewModelError(nil, "couldn't convert %v to int", newVal)
			} else {
				v.value = int64(i)
				v.i = int64(i)
			}
		} else {
			v.value = i
			v.i = i
		}

	case VtBool:
		if b, ok := newVal.(bool); !ok {
			return NewModelError(nil, "couldn't convert %v to bool", newVal)
		} else {
			v.value = b
			v.b = b
		}

	case VtString:
		if s, ok := newVal.(string); !ok {
			return NewModelError(nil, "couldn't convert %v to string", newVal)
		} else {
			v.value = s
			v.s = s
		}

	case VtFloat:
		if f, ok := newVal.(float64); !ok {
			return NewModelError(nil, "couldn't convert %v to float64", newVal)
		} else {
			v.value = f
			v.f = f
		}

	case VtTime:
		if t, ok := newVal.(time.Time); !ok {
			return NewModelError(nil, "couldn't convert %v to Time", newVal)
		} else {
			v.value = t
			v.t = t
		}
	}

	return nil
}

// Int returns a integer representation of variable v.
// if v is the VtString and converstion errror from string to float64 happened
// then panic fired
func (v *Variable) Int() int64 {
	var i int64

	switch v.vtype {
	case VtInt:
		i = v.i

	case VtBool:
		if v.b {
			i = 1
		} else {
			i = 0
		}

	case VtString:
		if f, err := strconv.ParseFloat(v.s, 64); err != nil {
			panic("cannot convert string var " + v.name +
				"[" + v.s + "] to float64" + err.Error())
		} else {
			i = int64(math.Round(f))
		}

	case VtFloat:
		i = int64(v.f)

	case VtTime:
		i = v.t.UnixMilli()
	}

	return i
}

// StrVal returns a string representation of variable v.
func (v *Variable) StrVal() string {
	var s string

	switch v.vtype {
	case VtInt:
		s = strconv.Itoa(int(v.i))

	case VtBool:
		if v.b {
			s = "true"
		} else {
			s = "false"
		}

	case VtFloat:
		s = strconv.FormatFloat(v.f, 'f', v.prec, 64)

	case VtString:
		s = v.s

	case VtTime:
		s = v.t.Format(time.RFC3339)
	}

	return s
}

// Bool returns a boolean representation of variable v.
func (v *Variable) Bool() bool {
	var b bool

	switch v.vtype {
	case VtInt:
		if v.i != 0 {
			b = true
		}

	case VtBool:
		b = v.b

	case VtFloat:
		if v.f != 0.0 {
			b = true
		}

	case VtString:
		if len(v.s) > 0 {
			b = true
		}

	case VtTime:
		b = !v.t.IsZero()
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
		f = float64(v.i)

	case VtBool:
		if v.b {
			f = 1.0
		}

	case VtFloat:
		f = v.f

	case VtString:
		f, err = strconv.ParseFloat(v.s, 64)
		if err != nil {
			panic("couldn't transform string " +
				v.s + " into float64 : " + err.Error())
		}

	case VtTime:
		f = float64(v.t.UnixMilli())
	}

	return f
}

func (v *Variable) Time() time.Time {
	var (
		t   time.Time
		err error
	)

	switch v.vtype {
	case VtInt:
		t = time.UnixMilli(v.i)

	case VtBool:
		if v.b {
			t = time.Now()
		}

	case VtString:
		if t, err = time.Parse(time.RFC3339, v.s); err != nil {
			panic("couldn't cast string '" + v.s +
				"' to time : " + err.Error())
		}

	case VtFloat:
		t = time.UnixMilli(int64(v.f))

	case VtTime:
		t = v.t
	}

	return t
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
		return nil, NewModelError(nil, "variable "+vn+
			" isn't found")
	}

	return v, nil
}

// DelVar deletes variable vn of type vt from namespace vs
// if there is no such variable, then error returned
func (vs *VarStore) DelVar(vn string) error {
	if !vs.checkVar(vn) {
		return NewModelError(nil, "couldn't find variable "+vn)
	}
	delete(map[string]*Variable(*vs), vn)

	return nil
}

func (vs *VarStore) Update(vn string, newVal interface{}) error {
	v := vs.getVar(vn, VtInt, false)
	if v == nil {
		return NewModelError(nil, "couldn't find variable "+vn)
	}

	return v.update(newVal)
}

// NewInt creates a new int variable in namespace vs
// if the variable with the same name and the same type exists, the error returned
func (vs *VarStore) NewInt(vn string, val int64) (*Variable, error) {
	if vs.checkVar(vn) {
		return nil, NewModelError(nil, "variable "+vn+" already exists")
	}

	v := vs.getVar(vn, VtInt, true)
	v.update(val)

	return v, nil
}

// NewBool creates a new bool variable in namespace vs
// if the variable with the same name and the same type exists, the error returned
func (vs *VarStore) NewBool(vn string, val bool) (*Variable, error) {
	if vs.checkVar(vn) {
		return nil, NewModelError(nil, "variable "+vn+" already exists")
	}

	v := vs.getVar(vn, VtBool, true)
	v.update(val)

	return v, nil
}

// NewString creates a new string variable in namespace vs
// if the variable with the same name and the same type exists, the error returned
func (vs *VarStore) NewString(vn string, val string) (*Variable, error) {
	if vs.checkVar(vn) {
		return nil, NewModelError(nil, "variable "+vn+" already exists")
	}

	v := vs.getVar(vn, VtString, true)
	v.update(val)

	return v, nil
}

// NewFloat creates a new string variable in namespace vs
// if the variable with the same name and the same type exists, the error returned
func (vs *VarStore) NewFloat(vn string, val float64) (*Variable, error) {
	if vs.checkVar(vn) {
		return nil, NewModelError(nil, "variable "+vn+" already exists")
	}

	v := vs.getVar(vn, VtFloat, true)
	v.update(val)

	return v, nil
}

func (vs *VarStore) NewTime(vn string, val time.Time) (*Variable, error) {
	if vs.checkVar(vn) {
		return nil, NewModelError(nil, "variable "+vn+" already exists")
	}

	v := vs.getVar(vn, VtTime, true)
	v.update(val)

	return v, nil
}

func (vs *VarStore) NewVar(v Variable) (*Variable, error) {
	if vs.checkVar(v.name) {
		return nil, NewModelError(nil, "variable "+v.name+" already exists")
	}

	vr := vs.getVar(v.name, v.vtype, true)
	vr.update(v.value)

	return vr, nil
}
