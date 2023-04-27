package common_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/common"
	"github.com/dr-dobermann/gobpm/pkg/identity"
	"github.com/matryer/is"
)

func TestNode(t *testing.T) {
	is := is.New(t)

	fn := common.FlowNode{
		FlowElement: *common.NewElement(identity.EmptyID(),
			"First Element", common.EtActivity),
	}

	of := fn.GetOutputFlows()
	is.Equal(len(of), 0)
	is.Equal(fn.HasIncoming(), false)

	sn := common.FlowNode{
		FlowElement: *common.NewElement(identity.EmptyID(),
			"Second Element", common.EtActivity),
	}
	is.Equal(sn.HasIncoming(), false)

	sName := "Test Link"
	fl, err := fn.Connect(&sn, sName)
	is.NoErr(err)
	is.Equal(fl.Name(), sName)
	is.Equal(fl.GetSource(), &fn)
	is.Equal(fl.GetTarget(), &sn)

	_, err = fn.Connect(&sn, "")
	is.True(err != nil)
	is.True(sn.HasIncoming())
	is.True(!fn.HasIncoming())
	is.Equal(len(fn.GetOutputFlows()), 1)
	is.Equal(len(sn.GetOutputFlows()), 0)
}
