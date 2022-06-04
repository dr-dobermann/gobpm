package vardata

import (
	"github.com/dr-dobermann/gobpm/internal/errs"
	"github.com/dr-dobermann/gobpm/pkg/variables"
)

type VarStoreDataProvider struct {
	variables.VarStore
}

func (dp *VarStoreDataProvider) GetDataItem(vname string) (DataItem, error) {
	v, err := dp.GetVar(vname)
	if err != nil {
		return nil, err
	}

	di := NewVDI(v)

	return di, nil
}

func (dp *VarStoreDataProvider) DelDataItem(vname string) error {
	return dp.DelVar(vname)
}

func (dp *VarStoreDataProvider) UpdateDataItem(vname string, newVal DataItem) error {
	if newVal == nil {
		return errs.ErrNoVariable
	}

	if newVal.IsCollection() {
		return errs.ErrIsNotACollection
	}

	v := newVal.GetOne()

	return dp.Update(vname, v.Value())
}
