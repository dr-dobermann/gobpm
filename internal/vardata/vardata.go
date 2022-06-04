package vardata

import (
	"github.com/dr-dobermann/gobpm/internal/errs"
	vars "github.com/dr-dobermann/gobpm/pkg/variables"
)

type VariableDataItem struct {
	vars.Variable
}

func NewVDI(v *vars.Variable) *VariableDataItem {
	nv := new(VariableDataItem)

	nv.Variable = *v

	return nv
}

func (va *VariableDataItem) IsCollection() bool {
	return false
}

func (va *VariableDataItem) Len() int {
	return 1
}

func (va *VariableDataItem) GetOne() vars.Variable {
	return va.Copy()
}

func (va *VariableDataItem) GetSome(from, to int) []vars.Variable {
	panic("GetSome could not be implemented for a single VariableDataItem")
}

func (va *VariableDataItem) UpdateOne(newValue *vars.Variable) error {
	if newValue == nil {
		return errs.ErrNoVariable
	}

	nv := newValue.Copy()

	va.Variable = nv

	return nil
}

func (va *VariableDataItem) UpdateSome(from, to int,
	newValues []*vars.Variable) error {

	return errs.ErrIsNotACollection
}
