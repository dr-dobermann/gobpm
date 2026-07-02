package events_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

// boundaryHostTask builds a real activity (a ReceiveTask) to attach boundary
// events to — it embeds the activity that carries BoundaryEvents/AddBoundaryEvent.
func boundaryHostTask(t *testing.T) *activities.ReceiveTask {
	t.Helper()

	msg := bpmncommon.MustMessage("host_msg",
		data.MustItemDefinition(values.NewVariable(1)))

	rt, err := activities.NewReceiveTask("host", msg)
	require.NoError(t, err)

	return rt
}

func signalDef(t *testing.T, name string) flow.EventDefinition {
	t.Helper()

	s, err := events.NewSignal(name, nil)
	require.NoError(t, err)

	sed, err := events.NewSignalEventDefinition(s)
	require.NoError(t, err)

	return sed
}

func errorDef(t *testing.T, code string) flow.EventDefinition {
	t.Helper()

	e, err := bpmncommon.NewError("err_"+code, code, nil)
	require.NoError(t, err)

	eed, err := events.NewErrorEventDefinition(e)
	require.NoError(t, err)

	return eed
}

func messageDef(t *testing.T) flow.EventDefinition {
	t.Helper()

	m := bpmncommon.MustMessage("bmsg",
		data.MustItemDefinition(values.NewVariable(1)))

	med, err := events.NewMessageEventDefinition(m, nil)
	require.NoError(t, err)

	return med
}

// fakeActivity is a flow.ActivityNode that is NOT boundary-capable (it lacks
// BoundaryEvents/AddBoundaryEvent), used to exercise BoundTo's host-capability
// guard.
type fakeActivity struct {
	flow.BaseNode
}

func (*fakeActivity) ActivityType() flow.ActivityType {
	return flow.TaskActivity
}

func escalationDef(t *testing.T) flow.EventDefinition {
	t.Helper()

	esc, err := events.NewEscalation("esc", "ESC",
		data.MustItemDefinition(values.NewVariable(1)))
	require.NoError(t, err)

	eed, err := events.NewEscalationEventDefintion(esc)
	require.NoError(t, err)

	return eed
}

// T-1 — NewBoundaryEvent validates every parameter (SRD-029 FR-2).
func TestNewBoundaryEventValidation(t *testing.T) {
	data.CreateDefaultStates()

	host := boundaryHostTask(t)
	sig := signalDef(t, "s1")
	errd := errorDef(t, "E1")
	esc := escalationDef(t)

	tests := []struct {
		name    string
		host    flow.ActivityNode
		def     flow.EventDefinition
		cancel  bool
		wantErr bool
	}{
		{"nil host", nil, sig, true, true},
		{"nil definition", host, nil, true, true},
		{"trigger not allowed on a boundary (escalation)", host, esc, true, true},
		{"error boundary cannot be non-interrupting", host, errd, false, true},
		{"valid interrupting signal boundary", host, sig, true, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			be, err := events.NewBoundaryEvent("b", tt.host, tt.def, tt.cancel)

			if tt.wantErr {
				require.Error(t, err)
				require.Nil(t, be)

				return
			}

			require.NoError(t, err)
			require.NotNil(t, be)
			require.Equal(t, tt.host, be.AttachedTo())
			require.Equal(t, tt.cancel, be.CancelActivity())
			require.Equal(t, flow.BoundaryEventClass, be.EventClass())
		})
	}
}

// TestNewBoundaryEventCatchEventError: a base option the embedded catch event
// rejects propagates out of NewBoundaryEvent (the newCatchEvent error path), after
// the host/definition/trigger guards have passed.
func TestNewBoundaryEventCatchEventError(t *testing.T) {
	data.CreateDefaultStates()

	host := boundaryHostTask(t)
	sig := signalDef(t, "s_bad")

	// options.WithName is not a valid base option for an event node, so the
	// embedded catch-event build fails inside NewBoundaryEvent.
	be, err := events.NewBoundaryEvent("b", host, sig, true,
		options.WithName("not a base option"))
	require.Error(t, err)
	require.Nil(t, be)
}

// T-2 — attachment populates the host and multiplicity is enforced per Event
// Declaration (SRD-029 FR-3; ADR-018 §2.5).
func TestBoundaryEventAttachmentAndMultiplicity(t *testing.T) {
	data.CreateDefaultStates()

	t.Run("attach populates BoundaryEvents and back-reference",
		func(t *testing.T) {
			host := boundaryHostTask(t)
			sig := signalDef(t, "s1")

			be, err := events.NewBoundaryEvent("b1", host, sig, true)
			require.NoError(t, err)
			require.Equal(t, host, be.AttachedTo())

			bes := host.BoundaryEvents()
			require.Len(t, bes, 1)
			require.Equal(t, be.ID(), bes[0].ID())
		})

	t.Run("second interrupting handler for the same declaration is rejected",
		func(t *testing.T) {
			host := boundaryHostTask(t)
			sig := signalDef(t, "s1")

			_, err := events.NewBoundaryEvent("b1", host, sig, true)
			require.NoError(t, err)

			// same definition instance => same Event Declaration.
			_, err = events.NewBoundaryEvent("b2", host, sig, true)
			require.Error(t, err)
			require.Len(t, host.BoundaryEvents(), 1)
		})

	t.Run("non-interrupting handlers are unbounded for the same declaration",
		func(t *testing.T) {
			host := boundaryHostTask(t)
			sig := signalDef(t, "s1")

			_, err := events.NewBoundaryEvent("b1", host, sig, true)
			require.NoError(t, err)

			_, err = events.NewBoundaryEvent("b2", host, sig, false)
			require.NoError(t, err)

			_, err = events.NewBoundaryEvent("b3", host, sig, false)
			require.NoError(t, err)

			require.Len(t, host.BoundaryEvents(), 3)
		})

	t.Run("interrupting handlers for distinct declarations are allowed",
		func(t *testing.T) {
			host := boundaryHostTask(t)

			// a non-interrupting boundary the later interrupting scan skips.
			_, err := events.NewBoundaryEvent("b0", host, signalDef(t, "s1"), false)
			require.NoError(t, err)

			_, err = events.NewBoundaryEvent("b1", host, signalDef(t, "s2"), true)
			require.NoError(t, err)

			_, err = events.NewBoundaryEvent("b2", host, errorDef(t, "E1"), true)
			require.NoError(t, err)

			require.Len(t, host.BoundaryEvents(), 3)
		})
}

// BoundTo's own guards and the node-shape methods (Clone/Node/flow gating).
func TestBoundaryEventNodeShape(t *testing.T) {
	data.CreateDefaultStates()

	host := boundaryHostTask(t)

	be, err := events.NewBoundaryEvent("b", host, signalDef(t, "s1"), true)
	require.NoError(t, err)

	t.Run("BoundTo rejects a nil host", func(t *testing.T) {
		require.Error(t, be.BoundTo(nil))
	})

	t.Run("BoundTo rejects a host that is not boundary-capable",
		func(t *testing.T) {
			bn, err := flow.NewBaseNode("fake")
			require.NoError(t, err)

			require.Error(t, be.BoundTo(&fakeActivity{BaseNode: *bn}))
		})

	t.Run("message boundary registers its payload output", func(t *testing.T) {
		h := boundaryHostTask(t)

		mbe, err := events.NewBoundaryEvent("bm", h, messageDef(t), false)
		require.NoError(t, err)
		require.NotNil(t, mbe)
		require.Len(t, h.BoundaryEvents(), 1)
	})

	t.Run("Node returns itself", func(t *testing.T) {
		require.Equal(t, flow.Node(be), be.Node())
	})

	t.Run("Clone preserves cancelActivity and back-reference", func(t *testing.T) {
		cn, err := be.Clone()
		require.NoError(t, err)

		c, ok := cn.(*events.BoundaryEvent)
		require.True(t, ok)
		require.Equal(t, be.CancelActivity(), c.CancelActivity())
		require.Equal(t, be.AttachedTo(), c.AttachedTo())
	})

	t.Run("boundary accepts no incoming flow, supports outgoing", func(t *testing.T) {
		require.Error(t, be.AcceptIncomingFlow(nil))
		require.NoError(t, be.SupportOutgoingFlow(nil))
	})
}
