package variables

import (
	"fmt"
	"testing"
	"time"

	"github.com/matryer/is"
)

// testing data except for Time.
// Time would be tested separately
var td = []struct {
	i int64
	b bool
	s string
	f float64
}{
	// original data
	{2, true, "2", 2.0},                   // Int
	{1, true, "true", 1.0},                // Bool
	{4, false, "3.66", 3.66},              // String
	{3, true, "3.33333", float64(10) / 3}, // Float
	// updated data
	{100, true, "100", 100.0},    // Int
	{0, false, "false", 0.0},     // Bool
	{0, true, "true", 0.0},       // String
	{3, true, "3.14", 3.1415928}, // Float
}

func TestTimeVariable(t *testing.T) {
	is := is.New(t)

	tm := time.Now()
	ts := tm.Format(time.RFC3339)
	t.Log(ts)

	v := V("now", Time, tm)

	is.Equal(tm.UnixMilli(), v.Int())
	is.Equal(float64(tm.UnixMilli()), v.Float64())
	is.Equal(ts, v.StrVal())
}

func TestVs(t *testing.T) {

	testVs := NewVarStore()

	for i := 0; i < 4; i++ {
		var (
			v   *Variable
			err error
		)

		// creating variables
		vn := fmt.Sprintf("v%d", i)
		switch Type(i) {
		case Int:
			_, err = testVs.NewInt(vn, td[i].i)

		case Bool:
			_, err = testVs.NewBool(vn, td[i].b)

		case String:
			_, err = testVs.NewString(vn, td[i].s)

		case Float:
			_, err = testVs.NewFloat(vn, td[i].f)
		}

		if err != nil {
			t.Error("cannot create variable " + vn + " of type " + string(Type(i)))
		}

		// checking duplicates
		switch Type(i) {
		case Int:
			_, err = testVs.NewInt(vn, td[i].i)

		case Bool:
			_, err = testVs.NewBool(vn, td[i].b)

		case String:
			_, err = testVs.NewString(vn, td[i].s)

		case Float:
			_, err = testVs.NewFloat(vn, td[i].f)
		}

		if err == nil {
			t.Error("create duplicate variable " + vn + " of type " + string(Type(i)))
		}

		// checking values
		v, err = testVs.GetVar(vn)
		if err != nil {
			t.Error("cannot get variable value for " + vn + " of type " + string(Type(i)))
		}
		if Type(i) == Float {
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

		if v.Type() != Type(i) {
			t.Error("invalid variable type. expected ", Type(i).String(), ", got ", v.Type())
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
		switch Type(i) {
		case Int:
			_, err = testVs.NewInt(vn, td[i].i)

		case Bool:
			_, err = testVs.NewBool(vn, td[i].b)

		case String:
			_, err = testVs.NewString(vn, td[i].s)

		case Float:
			_, err = testVs.NewFloat(vn, td[i].f)
		}

		if err != nil {
			t.Error("cannot create variable "+vn+" of type "+Type(i).String(), err)
		}

		// update non-existent (by name) variable
		if err = testVs.Update("fake_var", 100); err == nil {
			t.Error("updating non-existent variable fake_var")
		}

		// update
		switch Type(i) {
		case Int:
			err = testVs.Update(vn, td[i+4].i)

			// update with allowed value
			if errr := testVs.Update(vn, "100"); errr != nil {
				t.Error("couldn't update int with allowed value \"200\"", errr)
			}

			// update with disallowed value
			if errr := testVs.Update(vn, "str"); errr == nil {
				t.Error("couldn't update int with invalid value \"str\":", errr)
			}

		case Bool:
			err = testVs.Update(vn, td[i+4].b)

			// update by wrong type
			if errr := testVs.Update(vn, "200"); errr == nil {
				t.Error("updating variable ", vn, " type Bool with string(\"200\")")
			}

		case String:
			err = testVs.Update(vn, td[i+4].s)

			// update by wrong type
			if errr := testVs.Update(vn, 200); errr == nil {
				t.Error("updating variable ", vn, " type String with int(\"200\")")
			}

		case Float:
			err = testVs.Update(vn, td[i+4].f)

			// update by wrong type
			if errr := testVs.Update(vn, "3.1415928"); errr != nil {
				t.Error("updating variable with allowed type \"200\"", errr)
			}

			// update by wrong type
			if errr := testVs.Update(vn, "str"); errr == nil {
				t.Error("updating variable ", vn, " type Float with string(\"str\")")
			}
		}

		if err != nil {
			t.Error("cannont update variable "+vn+" of type "+
				Type(i).String()+" : ", err)
		}

		// checking values
		v, err = testVs.GetVar(vn)
		if err != nil {
			t.Error("cannot get variable value for " + vn + " of type " + Type(i).String())
		}

		var (
			in int64
			f  float64
		)

		if Type(i) != String {
			in = v.Int()
		} else {
			in = td[i+4].i
		}

		b := v.Bool()
		s := v.StrVal()

		if Type(i) != String {
			f = v.Float64()
		} else {
			f = td[i+4].f
		}

		if in != td[i+4].i || b != td[i+4].b || s != td[i+4].s || f != td[i+4].f {
			t.Error("Invalid variable ", vn, " value. Expected ", td[i+4], "Got ", in, b, s, f)
		}
	}
}

func TestVar(t *testing.T) {
	now := time.Now()

	testValues := map[Type]Values{
		Int:    {42, true, "42", 42.0, time.Time{}},
		Bool:   {1, true, "true", 1.0, time.Time{}},
		String: {0, true, "Hello dober!", 0.0, time.Time{}},
		Float:  {75, true, "74.8", 74.8, time.Time{}},
		Time:   {0, true, "now", 0, now},
	}

	var testVars [2][]*Variable

	// create variables and its duplicates
	testVars[0] = []*Variable{}
	testVars[1] = []*Variable{}
	i := 0
	for vt, vv := range testValues {
		val := getVarValue(vt, vv)

		// original
		testVars[0] = append(testVars[0], V(vt.String(), vt, val))

		// duplicate
		testVars[1] = append(testVars[0], V(vt.String(), vt, val))

		i++
	}

	for i, v := range testVars[0] {
		if !v.IsEqual(testVars[1][i]) {
			t.Fatalf("variable %s comparison error", v.Name())
		}
	}
}

func TestVarCopy(t *testing.T) {
	v := V("test", String, "Hello, Dober!")
	nv := v.Copy()

	if !v.IsEqual(&nv) {
		t.Fatalf("couldn't copy variable '%s'", v.name)
	}
}

func TestVarConversion(t *testing.T) {
	is := is.New(t)

	type testCase struct {
		sType Type
		val   Values
	}

	badCases := map[Type]testCase{
		Int:   {sType: String, val: Values{S: "trash"}},
		Bool:  {sType: String, val: Values{S: "trash"}},
		Float: {sType: String, val: Values{S: "trash"}},
		Time:  {sType: String, val: Values{S: "trash"}},
	}

	goodCases := map[Type]testCase{
		Int:   {sType: String, val: Values{S: "10"}},
		Float: {sType: String, val: Values{S: "10.2"}},
		Time:  {sType: String, val: Values{S: "2022-01-20T12:30:25+06:00"}},
		Bool:  {sType: String, val: Values{S: "true"}},
	}

	for nt, bc := range badCases {
		tv := V(bc.sType.String(), bc.sType, getVarValue(bc.sType, bc.val))
		is.True(!tv.CanConvertTo(nt))
	}

	for nt, bc := range goodCases {
		tv := V(bc.sType.String(), bc.sType, getVarValue(bc.sType, bc.val))
		is.True(tv.CanConvertTo(nt))
	}

}

func getVarValue(t Type, vv Values) interface{} {
	var val interface{}

	switch t {
	case Int:
		val = vv.I

	case Bool:
		val = vv.B

	case String:
		val = vv.S

	case Float:
		val = vv.F

	case Time:
		val = vv.T
	}

	return val
}
