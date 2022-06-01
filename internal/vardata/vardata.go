package executor

import (
	"github.com/dr-dobermann/gobpm/internal/errs"
	vars "github.com/dr-dobermann/gobpm/pkg/variables"
)

// Shell-class for variables.Variable for implementation
// run-time DataAccessor interface from model:data.go
type VarDataAccessor struct {
	vars.Variable
}

func (va *VarDataAccessor) Name() string {
	return va.Variable.Name()
}

func (va *VarDataAccessor) IsCollection() bool {
	return false
}

func (va *VarDataAccessor) Len() int {
	return 1
}

func (va *VarDataAccessor) GetOne() vars.Variable {
	return *vars.V(va.Name(), va.Type(), va.Value())
}

func (va *VarDataAccessor) GetSome(from, to int) []vars.Variable {
	panic("GetSome could not be implemented for a single vars.Variable")
}

func (va *VarDataAccessor) UpdateOne(newValue *vars.Variable) error {
	if newValue == nil {
		return errs.ErrNoVariable
	}

	va.Variable = newValue.Copy()

	return nil
}

func (va *VarDataAccessor) UpdateSome(from, to int,
	newValues []*vars.Variable) error {

	return errs.ErrIsNotACollection
}
