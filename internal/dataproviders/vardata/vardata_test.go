package vardata_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/internal/dataproviders/vardata"
	"github.com/dr-dobermann/gobpm/pkg/variables"
	"github.com/matryer/is"
)

func TestVarDataItem(t *testing.T) {
	is := is.New(t)

	// creation
	di := vardata.NewDI(*variables.V("x", variables.Int, 2))
	is.True(di != nil)
	is.True(di.GetOne().I == 2)
	is.True(di.Name() == "x")
	is.True(di.Type() == variables.Int)
	is.True(!di.IsCollection())
	is.True(di.Len() == 1)
	// should panic
	// is.True(len(di.GetSome(0, 1)) > 0)

	// update
	err := di.UpdateOne(variables.V("y", variables.String, "4"))
	is.NoErr(err)
	is.True(di.Name() == "x")
	is.True(di.Type() == variables.Int)
	is.True(di.GetOne().I == 4)
	// should panic
	// is.NoErr(di.UpdateSome(0, 1,
	// 	[]*variables.Variable{variables.V("_", variables.String, "test")}))
}

func TestVarDataProvider(t *testing.T) {
	is := is.New(t)

	dp := vardata.New()
	is.True(dp != nil)

	is.NoErr(dp.AddDataItem(vardata.NewDI(*variables.V("X", variables.Int, 5))))

	di, err := dp.GetDataItem("X")
	is.NoErr(err)
	is.True(di != nil)
	is.True(di.Type() == variables.Int)
	is.True(di.GetOne().I == 5)

	_, err = dp.GetDataItem("y")
	is.True(err != nil)

	err = dp.UpdateDataItem("X", vardata.NewDI(*variables.V("_", variables.Int, 3)))
	is.NoErr(err)
	di, err = dp.GetDataItem("X")
	is.NoErr(err)
	is.True(di.GetOne().I == 3)

	err = dp.DelDataItem("X")
	is.NoErr(err)

	_, err = dp.GetDataItem("X")
	is.True(err != nil)
}
