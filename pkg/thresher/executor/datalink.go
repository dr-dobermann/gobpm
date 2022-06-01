package executor

import (
	"fmt"

	"github.com/dr-dobermann/gobpm/pkg/model"
)

type DataLinker interface {
	CheckInData() error
	CheckOutData() error
}

func CheckLinkedData(ds model.DataSet,
	eEnv ExecutionEnvironment) error {

	for _, di := range ds.Items {
		v, err := eEnv.VStore().GetVar(di.Name)
		if err != nil {
			return err
		}

		if v.Type() != di.IDef.ItemType || !v.CanConvertTo(di.IDef.ItemType) {
			return fmt.Errorf("incompatible types for variable %q (has %s, expected %s)",
				di.Name, v.Type().String(), di.IDef.ItemType.String())
		}
	}

	return nil
}
