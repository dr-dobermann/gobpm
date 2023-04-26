package executor

import (
	"fmt"

	"github.com/dr-dobermann/gobpm/pkg/data"
)

type DataLinker interface {
	CheckInData() error
	CheckOutData() error
}

func CheckLinkedData(ds data.DataSet,
	eEnv ExecutionEnvironment) error {

	for _, di := range ds.Items {
		v, err := eEnv.VStore().GetVar(di.ItemSubject.Structure.Name())
		if err != nil {
			return err
		}

		if v.Type() != di.ItemSubject.Structure.Type() ||
			!v.CanConvertTo(di.ItemSubject.Structure.Type()) {

			return fmt.Errorf("incompatible types for variable %q (has %s, expected %s)",
				di.ItemSubject.Structure.Name(),
				v.Type().String(),
				di.ItemSubject.Structure.Type().String())
		}
	}

	return nil
}
