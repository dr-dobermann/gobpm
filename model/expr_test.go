package model

import (
	"fmt"
	"testing"
)

var td = []struct {
	i int
	b bool
	s string
	f float64
}{
	// original data
	{2, true, "2", 2.0},                   // VtInt
	{1, true, "true", 1.0},                // VtBool
	{4, true, "3.66", 3.66},               // VtString
	{3, true, "3.33333", float64(10) / 3}, // VtFloat
	// updated data
	{100, true, "100", 100.0},
	{0, false, "false", 0.0},
	{0, true, "dober", 0.0},
	{3, true, "3.14", 3.1415928},
}

func TestVariablesValues(t *testing.T) {
	for i := 0; i < 4; i++ {
		var (
			v   *Variable
			err error
		)

		// creating variables
		vn := fmt.Sprintf("v%d", i)
		switch VarType(i) {
		case VtInt:
			_, err = global.NewInt(vn, td[i].i)

		case VtBool:
			_, err = global.NewBool(vn, td[i].b)

		case VtString:
			_, err = global.NewString(vn, td[i].s)

		case VtFloat:
			_, err = global.NewFloat(vn, td[i].f)
		}

		if err != nil {
			t.Error("cannot create variable " + vn + " of type " + string(VarType(i)))
		}

		// checking duplicates
		switch VarType(i) {
		case VtInt:
			_, err = global.NewInt(vn, td[i].i)

		case VtBool:
			_, err = global.NewBool(vn, td[i].b)

		case VtString:
			_, err = global.NewString(vn, td[i].s)

		case VtFloat:
			_, err = global.NewFloat(vn, td[i].f)
		}

		if err == nil {
			t.Error("create duplicate variable " + vn + " of type " + string(VarType(i)))
		}

		// checking values
		v, err = global.GetVar(vn, VarType(i))
		if err != nil {
			t.Error("cannot get variable value for " + vn + " of type " + string(VarType(i)))
		}
		if VarType(i) == VtFloat {
			v.SetPrecision(-2)

			if v.Precision() != 2 {
				t.Error("invalid default precision. expected 2, got ", v.Precision())
			}

			v.SetPrecision(5)

			if v.Precision() != 5 {
				t.Error("coulnd't change float precision to 5, got ", v.Precision())
			}
		}

		if v.Name() != vn {
			t.Error("invalid variable name, expected ", vn, ", got ", v.Name())
		}

		if v.Type() != VarType(i) {
			t.Error("invalid variable type. expected ", VarType(i).String(), ", got ", v.Type())
		}

		in := v.Int()
		b := v.Bool()
		s := v.String()
		f := v.Float64()

		if in != td[i].i || b != td[i].b || s != td[i].s || f != td[i].f {
			t.Error("Invalid variable ", vn, " value. Expected ", td[i], ", got ", in, b, s, f)
		}
	}

}

func TestVariableGetter(t *testing.T) {
	global.NewInt("x", 0)
	v1, err := global.GetVar("x", VtInt)
	if v1 == nil || err != nil {
		t.Error("couldn't get a variable : ", err)
	}

	if v1 == nil || v1.Int() != 0 {
		t.Error("invalid variable value")
	}

	if v1 == nil || v1.Bool() {
		t.Error("invalid variable value")
	}

	if _, err := global.GetVar("xx", VtInt); err == nil {
		t.Error("non-existed variable returned")
	}

	v2, err := global.NewBool("x", true)
	if err != nil {
		t.Error("couldn't add new variable", err)
	}
	if v2.Bool() != true {
		t.Error("invalid variable value")
	}
}

func TestVariableDeleter(t *testing.T) {
	global.NewInt("xx", 3)
	global.NewBool("xx", true)

	if err := global.DelVar("xx", VtBool); err != nil {
		t.Error("couldn't delete variable")
	}

	if err := global.DelVar("xx", VtBool); err == nil {
		t.Error("double deleting")
	}

	if _, err := global.GetVar("xx", VtBool); err == nil {
		t.Error("variable isn't deleted")
	}

	if v, err := global.GetVar("xx", VtInt); err != nil {
		t.Error("variable isn't found")
	} else {
		if i := v.Int(); i != 3 {
			t.Error("invalid variable value ", i)
		}
	}

	if err := global.DelVar("xxx", VtInt); err == nil {
		t.Error("deleting inexisted variable")
	}

	if err := global.DelVar("xx", VtInt); err != nil {
		t.Error("couldn't delete variable", err)
	}
}

func TestVariableUpdate(t *testing.T) {
	for i := 0; i < 4; i++ {
		var (
			v   *Variable
			err error
		)

		// creating variables
		vn := fmt.Sprintf("v%d", i+4)
		vt := VarType(i)
		switch VarType(i) {
		case VtInt:
			_, err = global.NewInt(vn, td[i].i)

		case VtBool:
			_, err = global.NewBool(vn, td[i].b)

		case VtString:
			_, err = global.NewString(vn, td[i].s)

		case VtFloat:
			_, err = global.NewFloat(vn, td[i].f)
		}

		if err != nil {
			t.Error("cannot create variable "+vn+" of type "+VarType(i).String(), err)
		}

		// update non-existent (by name) variable
		if err = global.Update("fake_var", VtInt, 100); err == nil {
			t.Error("updating non-existent variable fake_var")
		}

		// update
		switch VarType(i) {
		case VtInt:
			err = global.Update(vn, vt, td[i+4].i)

			// update non-existent (by type) variable
			if errr := global.Update(vn, VtString, "200"); errr == nil {
				t.Error("updating non-existent variable ", vn, ":VtString")
			}

			// update by wrong type
			if errr := global.Update(vn, vt, "200"); errr == nil {
				t.Error("updating variable ", vn, " type VtInt with string(\"200\")")
			}

		case VtBool:
			err = global.Update(vn, vt, td[i+4].b)

			// update by wrong type
			if errr := global.Update(vn, vt, "200"); errr == nil {
				t.Error("updating variable ", vn, " type VtBool with string(\"200\")")
			}

		case VtString:
			err = global.Update(vn, vt, td[i+4].s)

			// update by wrong type
			if errr := global.Update(vn, vt, 200); errr == nil {
				t.Error("updating variable ", vn, " type VtString with int(\"200\")")
			}

		case VtFloat:
			err = global.Update(vn, vt, td[i+4].f)

			// update by wrong type
			if errr := global.Update(vn, vt, "200"); errr == nil {
				t.Error("updating variable ", vn, " type VtFloat with string(\"200\")")
			}
		}

		if err != nil {
			t.Error("cannont update variable "+vn+" of type "+
				VarType(i).String()+" : ", err)
		}

		// checking values
		v, err = global.GetVar(vn, VarType(i))
		if err != nil {
			t.Error("cannot get variable value for " + vn + " of type " + VarType(i).String())
		}

		var (
			in int
			f  float64
		)

		if VarType(i) != VtString {
			in = v.Int()
		} else {
			in = td[i+4].i
		}

		b := v.Bool()
		s := v.String()

		if VarType(i) != VtString {
			f = v.Float64()
		} else {
			f = td[i+4].f
		}

		if in != td[i+4].i || b != td[i+4].b || s != td[i+4].s || f != td[i+4].f {
			t.Error("Invalid variable ", vn, " value. Expected ", td[i+4], "Got ", in, b, s, f)
		}
	}
}
