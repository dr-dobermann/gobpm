package model

import "testing"

func TestVariables(t *testing.T) {
	v, err := global.NewInt("x", 2)

	if err != nil {
		t.Error("Couldn't create a new variable ", err)
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
}
