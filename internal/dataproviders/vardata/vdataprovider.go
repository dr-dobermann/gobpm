package vardata

import (
	"fmt"

	"github.com/dr-dobermann/gobpm/internal/errs"
	"github.com/dr-dobermann/gobpm/pkg/variables"
)

type varStoreDataProvider struct {
	variables.VarStore
}

func (dp *varStoreDataProvider) AddDataItem(nv DataItem) error {
	_, err := dp.NewVar(nv.GetOne())
	if err != nil {
		return fmt.Errorf("couldn't add new DataItem %q: %v", nv.Name(), err)
	}

	return nil
}

func (dp *varStoreDataProvider) GetDataItem(vname string) (DataItem, error) {
	v, err := dp.GetVar(vname)
	if err != nil {
		return nil, err
	}

	return NewDI(*v), nil
}

func (dp *varStoreDataProvider) DelDataItem(vname string) error {
	return dp.DelVar(vname)
}

func (dp *varStoreDataProvider) UpdateDataItem(vname string, newVal DataItem) error {
	if newVal == nil {
		return errs.ErrNoVariable
	}

	if newVal.IsCollection() {
		return errs.ErrIsNotACollection
	}

	v := newVal.GetOne()

	return dp.Update(vname, v.Value())
}

func New() DataProvider {
	dp := new(varStoreDataProvider)
	dp.VarStore = *variables.NewVarStore()

	return dp
}
