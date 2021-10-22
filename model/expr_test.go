package model

import (
	"log"
	"testing"
)

func TestVariablesValues(t *testing.T) {
	td := []struct {
		i int
		b bool
		s string
		f float64
	}{
		{2, true, "2", 2.0},                   // VtInt
		{1, true, "true", 1.0},                // VtBool
		{4, true, "3.66", 3.66},               // VtString
		{3, true, "3.33333", float64(10) / 3}, // VtFloat
	}

	for i := 0; i < 4; i++ {
		var (
			v   *Variable
			err error
		)
		// creating variables
		//vn := fmt.Sprintf("v%d", i)
		vn := "v"
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

		// creating duplicates
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
			v.SetPrecision(5)
		}

		log.Printf("Got variable %s of type %s with precision %d", v.Name(), v.Type().String(), v.Precision())

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
