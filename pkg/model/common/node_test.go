package common_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/identity"
	"github.com/dr-dobermann/gobpm/pkg/model/common"
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
	_, err := fn.Connect(&sn, sName, nil)
	is.True(err != nil)
}
