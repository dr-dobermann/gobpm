// GoBPM is BPMN v.2 compliant business process engine
//
// (c) 2021, 2022 Ruslan Gabitov a.k.a. dr-dobermann.
// Use of this source is governed by LGPL license that
// can be found in the LICENSE file.

/*
Package vardata implements DataProvider and DataItem interfaces for
variables' VarStore and Variable.
*/
package vardata

import (
	"github.com/dr-dobermann/gobpm/internal/errs"
	vars "github.com/dr-dobermann/gobpm/pkg/variables"
)

type variableDataItem struct {
	vars.Variable
}

func NewDI(v vars.Variable) DataItem {
	nv := new(variableDataItem)

	nv.Variable = v

	return nv
}

func (va *variableDataItem) IsCollection() bool {
	return false
}

func (va *variableDataItem) Len() int {
	return 1
}

func (va *variableDataItem) GetOne() vars.Variable {
	return va.Copy()
}

func (va *variableDataItem) GetSome(from, to int) []vars.Variable {
	panic("GetSome could not be implemented for a single variableDataItem")
}

func (va *variableDataItem) UpdateOne(newValue *vars.Variable) error {
	if newValue == nil {
		return errs.ErrNoVariable
	}

	return va.Update(newValue.Value())
}

func (va *variableDataItem) UpdateSome(from, to int,
	newValues []*vars.Variable) error {

	return errs.ErrIsNotACollection
}
