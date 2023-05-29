package service_test

import (
	"fmt"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/identity"
	"github.com/dr-dobermann/gobpm/pkg/model/common"
	"github.com/dr-dobermann/gobpm/pkg/model/dataprovider"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/matryer/is"
)

// ================= Interface resources =======================================
type dbInterface struct {
	dbName string
	User   string
	Pwd    string

	values map[string]int
}

func NewDBInterface(name, user, pwd string) *dbInterface {
	if name == "" || user == "" {
		panic("daName and userName shouldn't be empty")
	}

	return &dbInterface{
		dbName: name,
		User:   user,
		Pwd:    pwd,
		values: map[string]int{},
	}
}

func (dbI *dbInterface) Get(index string) (int, error) {

	i, ok := dbI.values[index]
	if !ok {
		return 0, fmt.Errorf("no data %q", index)
	}

	return i, nil
}

func (dbI *dbInterface) IsCollection() bool {

	return true
}

func (dbI *dbInterface) Len() int {

	return len(dbI.values)
}

func (dbI *dbInterface) Copy() dataprovider.DataItem {
	c := dbInterface{
		dbName: dbI.dbName,
		User:   dbI.User,
		Pwd:    dbI.Pwd,
		values: map[string]int{},
	}

	for k, v := range dbI.values {
		c.values[k] = v
	}

	return &c
}

func (dbI *dbInterface) GetValue() map[string]interface{} {
	vc := map[string]interface{}{}

	for k, v := range dbI.values {
		vc[k] = v
	}

	return vc
}

func (dbI *dbInterface) UpdateValue(nv map[string]interface{}) error {

	if nv == nil {
		return fmt.Errorf("no data for update")
	}

	// clear values
	dbI.values = map[string]int{}

	for k, v := range nv {
		switch vv := v.(type) {
		case float64:
			dbI.values[k] = int(vv)

		case int:
			dbI.values[k] = vv

		default:
			return fmt.Errorf("invalid value of %q (want int or float64, has %v)",
				k, v)
		}
	}

	return nil
}

func (dbI *dbInterface) GetGuts() interface{} {
	return dbI
}

// =============================================================================
func TestInterface(t *testing.T) {

	is := is.New(t)

	iID, iName := identity.NewID(), "test-interface"

	// check with empty DataItem
	_, err := service.NewInterface(iID, iName, nil)
	is.True(err != nil)

	iface, err := service.NewInterface(iID, iName,
		NewDBInterface("test-db", "user", "pwd"))
	is.NoErr(err)

	// try to get unexisted operation
	opName := "get-data"
	_, err = iface.GetOperation(opName)
	is.True(err != nil)

	//
	oId, oName := identity.NewID(), "get-data"

	errors := service.GenerateOpErrs(service.NO_INTERFACE, service.INVALID_INTERFACE)

	errors = append(errors,
		common.MustError("Data fetch error", "NOT_FOUND",
			&common.ItemDefinition{
				Kind:   common.IkInformaion,
				Item:   dataprovider.NewSimpleDataItem(""),
				Import: nil,
			}))

	// operation executor
	opExctr := func(op *service.Operation) *common.Error {

		inf := op.GetInterface()
		if inf == nil {
			return op.GetError(service.NO_INTERFACE)
		}

		dbImpl, ok := inf.GetImplementor().GetGuts().(dbInterface)
		if !ok {
			return op.GetError(service.INVALID_INTERFACE)
		}

		k := op.GetInMessage().GetItem().GetGuts()
		key, ok := k.(dataprovider.SimpleDataItem[string])
		if !ok {
			t.Fatal("couldn't get message payload")
		}

		v, err := dbImpl.Get(key.Get())
		if err != nil {
			return op.GetError("NOT_FOUND")
		}

		mOut, err := common.NewMessage("result",
			dataprovider.NewSimpleDataItem[int](v))
		if err != nil {
			e := op.GetError(service.OP_EXEC_ERR)
			e.Data().Item.UpdateValue(
				map[string]interface{}{
					"value": fmt.Sprintf("couldn't create OutMessage %q: %v",
						"result", err)})

			return e
		}

		if err = op.AddOutMessage(mOut); err != nil {

			e := op.GetError(service.OP_EXEC_ERR)
			e.Data().Item.UpdateValue(
				map[string]interface{}{
					"value": fmt.Sprintf("couldn't add operation OutMessage %q: %v",
						"result", err)})

			return e
		}

		return nil
	}

	di := dataprovider.NewSimpleDataItem[string]("test")

	inMsg, err := common.NewMessage("in-msg", di)
	is.NoErr(err)

	op, err := service.NewOperation(oId, oName,
		inMsg, errors, service.OperationFunctor(opExctr), iface)

	is.NoErr(err)
}
