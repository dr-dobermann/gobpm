package instance

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/generated/mockeventproc"
	"github.com/dr-dobermann/gobpm/generated/mockflow"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
)

// TestRegisterEventNoParentProducer: an instance wired without a parent
// EventProducer refuses registration with a clear error instead of a nil
// dereference on the delegation call.
func TestRegisterEventNoParentProducer(t *testing.T) {
	proc := mockeventproc.NewMockEventProcessor(t)
	eDef := mockflow.NewMockEventDefinition(t)

	inst := &Instance{}
	inst.setState(Created)

	require.Error(t, inst.RegisterEvent(proc, eDef))
}

// TestPropagateEventDelegates: an Active instance forwards the propagation to
// its parent producer and reports its success verbatim.
func TestPropagateEventDelegates(t *testing.T) {
	ep := mockeventproc.NewMockEventProducer(t)
	eDef := mockflow.NewMockEventDefinition(t)

	ep.EXPECT().PropagateEvent(mock.Anything, eDef).Return(nil)

	inst := &Instance{parentEventProducer: ep}
	inst.setState(Active)

	require.NoError(t, inst.PropagateEvent(t.Context(), eDef))
}

// TestPropagateEventWrapsParentError: a parent-producer failure comes back
// wrapped with the event-definition diagnostics, not swallowed.
func TestPropagateEventWrapsParentError(t *testing.T) {
	ep := mockeventproc.NewMockEventProducer(t)
	eDef := mockflow.NewMockEventDefinition(t)

	ep.EXPECT().PropagateEvent(mock.Anything, eDef).
		Return(errors.New("hub rejected"))
	eDef.EXPECT().ID().Return("ed-1")
	eDef.EXPECT().Type().Return(flow.TriggerMessage)

	inst := &Instance{parentEventProducer: ep}
	inst.setState(Active)

	require.Error(t, inst.PropagateEvent(t.Context(), eDef))
}

// TestEventProducerReturnsSelf: the instance exposes ITSELF as the
// RuntimeEnvironment's EventProducer (delegation happens inside the methods).
func TestEventProducerReturnsSelf(t *testing.T) {
	inst := &Instance{}

	require.Same(t, inst, inst.EventProducer())
}
