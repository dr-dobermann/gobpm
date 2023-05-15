package executor

// type DataLinker interface {
// 	CheckInData() error
// 	CheckOutData() error
// }

// func CheckLinkedData(ds data.DataSet,
// 	eEnv ExecutionEnvironment) error {

// 	for _, di := range ds.Items {
// 		v, err := eEnv.VStore().GetVar(di.ItemSubject.Item.Name())
// 		if err != nil {
// 			return err
// 		}

// 		if v.Type() != di.ItemSubject.Item.Type() ||
// 			!v.CanConvertTo(di.ItemSubject.Item.Type()) {

// 			return fmt.Errorf("incompatible types for variable %q (has %s, expected %s)",
// 				di.ItemSubject.Item.Name(),
// 				v.Type().String(),
// 				di.ItemSubject.Item.Type().String())
// 		}
// 	}

// 	return nil
// }
