package gep

import (
	"fmt"
	"strings"

	"github.com/dr-dobermann/gobpm/internal/errs"
)

const (
	gepLanguage = "GEP"
)

// GEP -- GoBPM Expression Processor.
//
// GEP implements internal GoBPM Expression functionality for
//
type GEP struct {
	ExpressionBase

	vs         *VarStore
	calculated bool

	// every step generated result
	result []Variable
}

func NewGEP(vs *VarStore) *GEP {
	gep := new(GEP)

	gep.id = NewID()
	gep.vs = vs
	gep.language = gepLanguage

	return gep
}

func (gep *GEP) GetResults() ([]Variable, error) {
	if !gep.calculated {
		return nil, errs.ErrNotCalculated
	}

	return []Variable{gep.result}, nil
}

func (gep *GEP) LinkToVarStore(vs *VarStore) error {
	if vs == nil {
		return errs.ErrEmptyVarStore
	}

	gep.vs = vs

	return nil
}

type ExpressionOperation func(v *Variable) error

// loads varible named v.Name and typed v.Type from linked VarStore to
func (gep *GEP) Load(v *Variable) error {
	gep.calculated = false

	if err := gep.checkVar(v); err != nil {
		return err
	}

	vv, err := gep.vs.GetVar(v.name)
	if err != nil {
		return fmt.Errorf("couldn't find variable '%s': %v", v.name, err)
	}

	if vv.Type() != v.Type() {
		return fmt.Errorf(
			"requested variable has different type: want %v, got %v",
			v.Time(), vv.Type())
	}

	gep.result = *vv
	gep.retType = gep.result.vtype
	gep.calculated = true

	return nil
}

func (gep *GEP) checkVar(v *Variable) error {
	if gep.vs == nil {
		return errs.ErrEmptyVarStore
	}

	if v == nil {
		return errs.ErrNoVariable
	}

	return nil
}

// creates a new variable in the expression to save results of postcoming
// operation into it
func (gep *GEP) Create(v *Variable) error {
	gep.calculated = false

	if err := gep.checkVar(v); err != nil {
		return err
	}

	if strings.Trim(v.name, " ") == "" {
		return errs.ErrNoVariable
	}

	gep.result = *v
	gep.retType = gep.result.vtype
	gep.calculated = true

	return nil
}

// stores or updates value of calculated result into the linked VarStore
func (gep *GEP) Store(_ *Variable) error {
	if gep.vs == nil {
		return errs.ErrEmptyVarStore
	}

	if !gep.calculated {
		return errs.ErrNotCalculated
	}

	// store as a new variable
	if _, err := gep.vs.GetVar(gep.result.name); err != nil {
		_, err = gep.vs.NewVar(gep.result)
		if err != nil {
			return fmt.Errorf("couldn't update variable '%s' "+
				"in linked VarStore: %v", gep.result.name, err)
		}

		return nil

	}

	// update the existed variable
	if err := gep.vs.Update(gep.result.name, gep.result.Value()); err != nil {
		return fmt.Errorf("couldn't store variable '%s' "+
			"in linked VarStore: %v", gep.result.name, err)
	}

	return nil
}

func (gep *GEP) Less(v *Variable) error {
	if err := gep.checkVar(v); err != nil {
		return err
	}

	return nil
}
