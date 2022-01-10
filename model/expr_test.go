package model

import (
	"fmt"
	"testing"
	"time"

	"github.com/matryer/is"
)

// testing data except for VtTime.
// VtTime would be tested separately
var td = []struct {
	i int64
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

func TestTimeVariable(t *testing.T) {
	is := is.New(t)

	tm := time.Now()
	ts := tm.Format(time.RFC3339)

	v := V("now", VtTime, tm)

	is.Equal(tm.UnixMilli(), v.Int())
	is.Equal(float64(tm.UnixMilli()), v.Float64())
	is.Equal(ts, v.StrVal())
}

func testVs(t *testing.T) {

	testVs := NewVarStore()

	for i := 0; i < 4; i++ {
		var (
			v   *Variable
			err error
		)

		// creating variables
		vn := fmt.Sprintf("v%d", i)
		switch VarType(i) {
		case VtInt:
			_, err = testVs.NewInt(vn, td[i].i)

		case VtBool:
			_, err = testVs.NewBool(vn, td[i].b)

		case VtString:
			_, err = testVs.NewString(vn, td[i].s)

		case VtFloat:
			_, err = testVs.NewFloat(vn, td[i].f)
		}

		if err != nil {
			t.Error("cannot create variable " + vn + " of type " + string(VarType(i)))
		}

		// checking duplicates
		switch VarType(i) {
		case VtInt:
			_, err = testVs.NewInt(vn, td[i].i)

		case VtBool:
			_, err = testVs.NewBool(vn, td[i].b)

		case VtString:
			_, err = testVs.NewString(vn, td[i].s)

		case VtFloat:
			_, err = testVs.NewFloat(vn, td[i].f)
		}

		if err == nil {
			t.Error("create duplicate variable " + vn + " of type " + string(VarType(i)))
		}

		// checking values
		v, err = testVs.GetVar(vn)
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
		s := v.StrVal()
		f := v.Float64()

		if in != td[i].i || b != td[i].b || s != td[i].s || f != td[i].f {
			t.Error("Invalid variable ", vn, " value. Expected ", td[i], ", got ", in, b, s, f)
		}
	}

}

func TestVariableDeleter(t *testing.T) {

	testVs := NewVarStore()

	if _, err := testVs.NewInt("xx", 3); err != nil {
		t.Fatal("couldn't create int variable xx :", err)
	}

	if err := testVs.DelVar("xx"); err != nil {
		t.Error("couldn't delete variable")
	}

	if err := testVs.DelVar("xx"); err == nil {
		t.Error("double deleting")
	}

	if _, err := testVs.GetVar("xx"); err == nil {
		t.Error("variable isn't deleted")
	}
}

func TestVariableUpdate(t *testing.T) {
	testVs := NewVarStore()

	for i := 0; i < 4; i++ {
		var (
			v   *Variable
			err error
		)

		// creating variables
		vn := fmt.Sprintf("v%d", i+4)
		switch VarType(i) {
		case VtInt:
			_, err = testVs.NewInt(vn, td[i].i)

		case VtBool:
			_, err = testVs.NewBool(vn, td[i].b)

		case VtString:
			_, err = testVs.NewString(vn, td[i].s)

		case VtFloat:
			_, err = testVs.NewFloat(vn, td[i].f)
		}

		if err != nil {
			t.Error("cannot create variable "+vn+" of type "+VarType(i).String(), err)
		}

		// update non-existent (by name) variable
		if err = testVs.Update("fake_var", 100); err == nil {
			t.Error("updating non-existent variable fake_var")
		}

		// update
		switch VarType(i) {
		case VtInt:
			err = testVs.Update(vn, td[i+4].i)

			// update non-existent (by type) variable
			if errr := testVs.Update(vn, "200"); errr == nil {
				t.Error("updating non-existent variable ", vn, ":VtString")
			}

			// update by wrong type
			if errr := testVs.Update(vn, "200"); errr == nil {
				t.Error("updating variable ", vn, " type VtInt with string(\"200\")")
			}

		case VtBool:
			err = testVs.Update(vn, td[i+4].b)

			// update by wrong type
			if errr := testVs.Update(vn, "200"); errr == nil {
				t.Error("updating variable ", vn, " type VtBool with string(\"200\")")
			}

		case VtString:
			err = testVs.Update(vn, td[i+4].s)

			// update by wrong type
			if errr := testVs.Update(vn, 200); errr == nil {
				t.Error("updating variable ", vn, " type VtString with int(\"200\")")
			}

		case VtFloat:
			err = testVs.Update(vn, td[i+4].f)

			// update by wrong type
			if errr := testVs.Update(vn, "200"); errr == nil {
				t.Error("updating variable ", vn, " type VtFloat with string(\"200\")")
			}
		}

		if err != nil {
			t.Error("cannont update variable "+vn+" of type "+
				VarType(i).String()+" : ", err)
		}

		// checking values
		v, err = testVs.GetVar(vn)
		if err != nil {
			t.Error("cannot get variable value for " + vn + " of type " + VarType(i).String())
		}

		var (
			in int64
			f  float64
		)

		if VarType(i) != VtString {
			in = v.Int()
		} else {
			in = td[i+4].i
		}

		b := v.Bool()
		s := v.StrVal()

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
