package model

import (
	"testing"
)

func TestVariablesValues(t *testing.T) {
	// int variable
	v, err := global.NewInt("x", 2)
	if err != nil {
		t.Error("Couldn't create a new variable ", err)
	}

	if _, err = global.NewInt("x", 3); err == nil {
		t.Error("Double variable in namespace")
	}

	if v == nil {
		t.Error("New variable is empty!")
	}

	i := v.Int()
	if i != 2 {
		t.Error("invalid int variable value : ", i)
	}

	s := v.String()
	if s != "2" {
		t.Error("invalid string variable value : ", s)
	}

	b := v.Bool()
	if !b {
		t.Error("invalid bool variable value : ", b)
	}

	f := v.Float64()
	if f != 2.0 {
		t.Error("invalid float64 variable value : ", f)
	}
}

func TestVariableGetter(t *testing.T) {
	global.NewInt("x", 2)
	v1, err := global.GetVar("x", VtInt)
	if v1 == nil || err != nil {
		t.Error("couldn't get a variable : ", err)
	}

	if v1 == nil || v1.Int() != 2 {
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
