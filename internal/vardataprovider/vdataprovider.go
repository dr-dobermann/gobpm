package vardataprovider

import (
	"fmt"

	dp "github.com/dr-dobermann/gobpm/pkg/dataprovider"

	"github.com/dr-dobermann/gobpm/internal/errs"
	"github.com/dr-dobermann/gobpm/pkg/variables"
)

type varStoreDataProvider struct {
	variables.VarStore
}

func (dp *varStoreDataProvider) AddDataItem(nv dp.DataItem) error {
	_, err := dp.NewVar(nv.Get())
	if err != nil {
		return fmt.Errorf("couldn't add new DataItem %q: %v", nv.Name(), err)
	}

	return nil
}

func (dp *varStoreDataProvider) GetDataItem(vname string) (dp.DataItem, error) {
	v, err := dp.GetVar(vname)
	if err != nil {
		return nil, err
	}

	return NewDI(*v), nil
}

func (dp *varStoreDataProvider) DelDataItem(vname string) error {
	return dp.DelVar(vname)
}

func (dp *varStoreDataProvider) UpdateDataItem(vname string, newVal dp.DataItem) error {

	if newVal == nil {
		return errs.ErrNoVariable
	}

	if newVal.IsCollection() {
		return errs.ErrIsNotACollection
	}

	v := newVal.Get()

	return dp.Update(vname, v.Value())
}

func New() dp.DataProvider {
	dp := new(varStoreDataProvider)
	dp.VarStore = *variables.NewVarStore()

	return dp
}
