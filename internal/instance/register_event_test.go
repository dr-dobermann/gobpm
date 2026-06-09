package instance

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/generated/mockeventproc"
	"github.com/dr-dobermann/gobpm/generated/mockflow"
)

// FIX-002 RC1: event-start nodes register during instance.New (Created), so
// RegisterEvent must permit non-terminal states and refuse only terminal ones.

func TestRegisterEventAllowsCreated(t *testing.T) {
	ep := mockeventproc.NewMockEventProducer(t)
	proc := mockeventproc.NewMockEventProcessor(t)
	eDef := mockflow.NewMockEventDefinition(t)

	ep.EXPECT().RegisterEvent(proc, eDef).Return(nil)

	inst := &Instance{parentEventProducer: ep}
	inst.setState(Created)

	require.NoError(t, inst.RegisterEvent(proc, eDef))
}

func TestRegisterEventRejectsTerminal(t *testing.T) {
	ep := mockeventproc.NewMockEventProducer(t)
	proc := mockeventproc.NewMockEventProcessor(t)
	eDef := mockflow.NewMockEventDefinition(t)

	// Used only when the guard builds its error detail.
	proc.EXPECT().ID().Return("p-1").Maybe()

	inst := &Instance{parentEventProducer: ep}
	inst.setState(Terminated)

	// Guard rejects before delegating, so ep.RegisterEvent is never called
	// (no EXPECT set — testify fails if it is).
	require.Error(t, inst.RegisterEvent(proc, eDef))
}
