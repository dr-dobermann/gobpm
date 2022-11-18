package model

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/common"
	"github.com/dr-dobermann/gobpm/pkg/identity"
	"github.com/matryer/is"
)

func TestGatewayModel(t *testing.T) {
	is := is.New(t)

	testData := []struct {
		gm              GatewayModel
		gt              GatewayType
		name            string
		dir             GatewayDirection
		flow            EventGatewayFlowType
		firstCopyFailed bool
	}{
		{
			gt:              Exclusive,
			name:            "ExclusiveC",
			dir:             Converging,
			flow:            ExclusiveFlow,
			firstCopyFailed: false},
		{
			gt:              Exclusive,
			name:            "ExclusiveD",
			dir:             Diverging,
			flow:            ExclusiveFlow,
			firstCopyFailed: false},
	}

	// dummy process
	p := NewProcess(identity.NewID(), "GTestPrcs", "")

	// dummy empty process copy
	cp, err := p.Copy()
	is.NoErr(err)

	for _, td := range testData {
		// Test models creation
		switch td.gt {
		case Exclusive:
			td.gm = NewExclusiveGateway(p, td.name, td.dir, nil)

		default:
			t.Fatalf("Unknown gateway type '%s'", td.gt.String())
		}

		// model shouldn't be empty
		is.True(td.gm != nil)

		// Test correctness of the created model
		is.True(td.dir == td.gm.Direction())
		is.True(td.name == td.gm.Name())
		is.True(td.gm.Type() == common.EtGateway)
		is.True(td.gt == td.gm.GwayType())

		// check copying of empty gateway
		if td.firstCopyFailed {
			_, err := td.gm.Copy(cp)
			t.Log(err)
			is.True(err != nil)
		} else {
			gmc, err := td.gm.Copy(cp)
			is.NoErr(err)

			// check copied gateway
			is.True(gmc.Direction() == td.gm.Direction())
			is.True(gmc.Type() == td.gm.Type())
			is.True(gmc.GwayType() == td.gm.GwayType())
			is.True(gmc.Name() == td.gm.Name())
			is.True(gmc.ID() != td.gm.ID())
			is.True(gmc.ProcessID() == cp.ID())
			is.True(td.gm.ProcessID() == p.ID())
		}
	}

}
