package variables

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

type Type uint8

const (
	Int Type = iota
	Bool
	String
	Float
	Time
)

func (vt Type) String() string {
	return []string{"Int", "Bool", "String", "Float", "Time"}[vt]
}

type VariableValues struct {
	I int64
	B bool
	S string
	F float64
	T time.Time
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
	VariableValues

	name  string
	vType Type
	value interface{}
	// float precision. Default 2
	prec int
}

// V creates a new variable
func V(n string, t Type, v interface{}) *Variable {
	n = strings.Trim(n, " ")

	vv := &Variable{
		name:  n,
		vType: t,
		prec:  2}

	vv.update(v)

	return vv
}

func (v *Variable) Name() string {
	return v.name
}

func (v *Variable) Type() Type {
	return v.vType
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

func (v *Variable) RawValues() VariableValues {
	return v.VariableValues
}

func (v *Variable) Copy() Variable {
	return Variable{
		VariableValues: v.VariableValues,
		name:           v.name,
		vType:          v.vType,
		value:          v.value,
		prec:           v.prec,
	}
}

func (v Variable) NewVErr(
	err error,
	format string,
	values ...interface{}) VariableError {

	return VariableError{vName: v.name, vType: v.vType,
		msg: fmt.Sprintf(format, values...), Err: err}
}

// update updates a value of the Variable v.
//
// it expected to receive the value of internal type of v.
func (v *Variable) update(newVal interface{}) error {
	switch v.vType {
	case Int:
		if newVal == nil {
			v.value = int64(0)
			v.I = 0
			break
		}

		if i, ok := newVal.(int64); !ok {
			if i, ok := newVal.(int); !ok {
				return v.NewVErr(nil, "couldn't convert %v to int", newVal)
			} else {
				v.value = int64(i)
				v.I = int64(i)
			}
		} else {
			v.value = i
			v.I = i
		}

	case Bool:
		if newVal == nil {
			v.value = false
			v.B = false
			break
		}

		if b, ok := newVal.(bool); !ok {
			return v.NewVErr(nil, "couldn't convert %v to bool", newVal)
		} else {
			v.value = b
			v.B = b
		}

	case String:
		if newVal == nil {
			v.value = ""
			v.S = ""
			break
		}

		if s, ok := newVal.(string); !ok {
			return v.NewVErr(nil, "couldn't convert %v to string", newVal)
		} else {
			v.value = s
			v.S = s
		}

	case Float:
		if newVal == nil {
			v.value = float64(0.0)
			v.F = 0.0
			break
		}

		if f, ok := newVal.(float64); !ok {
			return v.NewVErr(nil, "couldn't convert %v to float64", newVal)
		} else {
			v.value = f
			v.F = f
		}

	case Time:
		if newVal == nil {
			v.T = time.Now()
			v.value = v.T
			break
		}

		if t, ok := newVal.(time.Time); !ok {
			return v.NewVErr(nil, "couldn't convert %v to Time", newVal)

		} else {
			v.value = t
			v.T = t
		}
	}

	return nil
}

// Int returns a integer representation of variable v.
// if v is the String and converstion errror from string to float64 happened
// then panic fired
func (v *Variable) Int() int64 {
	var i int64

	switch v.vType {
	case Int:
		i = v.I

	case Bool:
		if v.B {
			i = 1
		} else {
			i = 0
		}

	case String:
		if f, err := strconv.ParseFloat(v.S, 64); err != nil {
			panic("cannot convert string var " + v.name +
				"[" + v.S + "] to float64" + err.Error())
		} else {
			i = int64(math.Round(f))
		}

	case Float:
		i = int64(v.F)

	case Time:
		i = v.T.UnixMilli()
	}

	return i
}

// StrVal returns a string representation of variable v.
func (v *Variable) StrVal() string {
	var s string

	switch v.vType {
	case Int:
		s = strconv.Itoa(int(v.I))

	case Bool:
		if v.B {
			s = "true"
		} else {
			s = "false"
		}

	case Float:
		s = strconv.FormatFloat(v.F, 'f', v.prec, 64)

	case String:
		s = v.S

	case Time:
		s = v.T.Format(time.RFC3339)
	}

	return s
}

// Bool returns a boolean representation of variable v.
func (v *Variable) Bool() bool {
	var b bool

	switch v.vType {
	case Int:
		if v.I != 0 {
			b = true
		}

	case Bool:
		b = v.B

	case Float:
		if v.F != 0.0 {
			b = true
		}

	case String:
		if len(v.S) > 0 {
			b = true
		}

	case Time:
		b = !v.T.IsZero()
	}

	return b
}

// Float64 returns a float64 representation of variable v.
// if v is the String and converstion errror from string to float64 happened
// then panic fired
func (v *Variable) Float64() float64 {
	var (
		f   float64
		err error
	)

	switch v.vType {
	case Int:
		f = float64(v.I)

	case Bool:
		if v.B {
			f = 1.0
		}

	case Float:
		f = v.F

	case String:
		f, err = strconv.ParseFloat(v.S, 64)
		if err != nil {
			panic("couldn't transform string " +
				v.S + " into float64 : " + err.Error())
		}

	case Time:
		f = float64(v.T.UnixMilli())
	}

	return f
}

func (v *Variable) Time() time.Time {
	var (
		t   time.Time
		err error
	)

	switch v.vType {
	case Int:
		t = time.UnixMilli(v.I)

	case Bool:
		if v.B {
			t = time.Now()
		}

	case String:
		if t, err = time.Parse(time.RFC3339, v.S); err != nil {
			panic("couldn't cast string '" + v.S +
				"' to time : " + err.Error())
		}

	case Float:
		t = time.UnixMilli(int64(v.F))

	case Time:
		t = v.T
	}

	return t
}

func (v *Variable) IsEqual(ov *Variable) bool {
	switch v.vType {
	case Int:
		return v.I == ov.I

	case Bool:
		return v.B == ov.B

	case String:
		return v.S == ov.S

	case Float:
		return v.F == ov.F

	case Time:
		return v.T.Equal(ov.T)
	}

	return false
}
