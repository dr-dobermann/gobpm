package common_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/common"
	"github.com/dr-dobermann/gobpm/pkg/identity"
	"github.com/matryer/is"
)

func TestElements(t *testing.T) {

	is := is.New(t)

	// element creation
	id := identity.NewID()
	name := "TestElement"

	e := common.NewElement(id, name, common.EtActivity)
	is.True(e != nil)
	is.True(e.Container() == nil)
	is.True(e.Type() == common.EtActivity)
	is.True(e.Category() == "")

	e.SetCategory("test")
	is.True(e.Category() == "test")
}

func TestContainer(t *testing.T) {

	is := is.New(t)

	cname := "TestContainer"
	cID := identity.NewID()
	ec := common.NewContainer(cID, cname)
	is.True(ec != nil)

	// Remove unexisted
	err := ec.Remove(identity.NewID())
	is.True(err != nil)

	// Add
	e1 := common.NewElement(identity.EmptyID(), "e1", common.EtActivity)
	err = ec.Add(e1)
	is.NoErr(err)
	err = ec.Add(e1)
	is.NoErr(err)

	// Get
	e, err := ec.Get(e1.ID())
	is.NoErr(err)
	is.True(e == e1)

	// Add to another container
	ec1 := common.NewContainer(identity.EmptyID(), "ec1")
	err = ec1.Add(e1)
	is.True(err != nil)

	// Assign and Remove
	err = e.AssignTo(ec1)
	is.True(err != nil)
	err = ec.Remove(e.ID())
	is.NoErr(err)

	// Add multy
	e1, e2 := common.NewElement(identity.EmptyID(), "ee1", common.EtActivity),
		common.NewElement(identity.EmptyID(), "ee2", common.EtGateway)
	err = ec1.Add(e1, e2)
	is.NoErr(err)
	_, err = ec1.Get(e2.ID())
	is.NoErr(err)

	el := ec1.GetAll()
	is.True(len(el) == 2)
}
