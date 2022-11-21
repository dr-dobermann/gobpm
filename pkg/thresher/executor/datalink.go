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
		v, err := eEnv.VStore().GetVar(di.ItemSubjectRef.StructureRef.Name())
		if err != nil {
			return err
		}

		if v.Type() != di.ItemSubjectRef.StructureRef.Type() ||
			!v.CanConvertTo(di.ItemSubjectRef.StructureRef.Type()) {

			return fmt.Errorf("incompatible types for variable %q (has %s, expected %s)",
				di.ItemSubjectRef.StructureRef.Name(),
				v.Type().String(),
				di.ItemSubjectRef.StructureRef.Type().String())
		}
	}

	return nil
}
