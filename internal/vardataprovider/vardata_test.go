package vardataprovider_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/internal/errs"
	vdp "github.com/dr-dobermann/gobpm/internal/vardataprovider"
	"github.com/dr-dobermann/gobpm/pkg/variables"
	"github.com/matryer/is"
)

func TestVarDataItem(t *testing.T) {
	is := is.New(t)

	// creation
	di := vdp.NewDI(*variables.V("x", variables.Int, 2))
	is.True(di != nil)
	is.True(di.Get().I == 2)
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
	is.True(di.Get().I == 4)
	// should panic
	// is.NoErr(di.UpdateSome(0, 1,
	// 	[]*variables.Variable{variables.V("_", variables.String, "test")}))
}

func TestVarDataProvider(t *testing.T) {
	is := is.New(t)

	dp := vdp.New()
	is.True(dp != nil)

	is.NoErr(dp.AddDataItem(vdp.NewDI(*variables.V("X", variables.Int, 5))))

	di, err := dp.GetDataItem("X")
	is.NoErr(err)
	is.True(di != nil)
	is.True(di.Type() == variables.Int)
	is.True(di.Get().I == 5)

	// look for a non-existing variable
	_, err = dp.GetDataItem("y")
	is.True(err != nil)

	// update non-existing variable
	err = dp.UpdateDataItem("Y", vdp.NewDI(*variables.V("_", variables.Int, 3)))
	is.True(err != nil)

	err = dp.UpdateDataItem("X", vdp.NewDI(*variables.V("_", variables.Int, 3)))
	is.NoErr(err)
	di, err = dp.GetDataItem("X")
	is.NoErr(err)
	is.True(di.Get().I == 3)

	_, err = di.GetSome(0, 3)
	is.True(err == errs.ErrIsNotACollection)

	err = dp.DelDataItem("X")
	is.NoErr(err)

	_, err = dp.GetDataItem("X")
	is.True(err != nil)
}
