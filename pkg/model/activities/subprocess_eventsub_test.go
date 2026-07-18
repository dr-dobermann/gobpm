package activities_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/stretchr/testify/require"
)

// sigStart builds a Signal-triggered start; interrupting when itr is true.
func sigStart(t *testing.T, name string, itr bool) *events.StartEvent {
	t.Helper()

	sig, err := events.NewSignal(name+"-sig",
		data.MustItemDefinition(values.NewVariable(1)))
	require.NoError(t, err)

	opts := []options.Option{
		events.WithSignalTrigger(events.MustSignalEventDefinition(sig)),
	}
	if !itr {
		// interrupting is the default (§13.5.4) — flip it for the
		// non-interrupting case.
		opts = append(opts, events.WithNonInterrupting())
	}

	start, err := events.NewStartEvent(name, opts...)
	require.NoError(t, err)

	return start
}

// errEventSubNI builds an Event Sub-Process with a NON-interrupting Error start
// — an invalid combination (Error is interrupting-only, BPMN §10.5.6), used to
// assert the SRD-053 validation gate.
func errEventSubNI(t *testing.T, name, code string) *activities.SubProcess {
	t.Helper()

	sp, err := activities.NewSubProcess(name, activities.WithTriggeredByEvent())
	require.NoError(t, err)

	bpErr, err := bpmncommon.NewError(name+"-err", code, nil)
	require.NoError(t, err)
	eed, err := events.NewErrorEventDefinition(bpErr)
	require.NoError(t, err)
	start, err := events.NewStartEvent(name+"-start",
		events.WithErrorTrigger(eed), events.WithNonInterrupting())
	require.NoError(t, err)

	task := spTask(t, name+"-task")
	end, err := events.NewEndEvent(name + "-end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, task, end} {
		require.NoError(t, sp.Add(e))
	}
	_, err = flow.Link(start, task)
	require.NoError(t, err)
	_, err = flow.Link(task, end)
	require.NoError(t, err)

	return sp
}

// eventSub builds an Event Sub-Process: [signal start] → task → end. The start
// is interrupting when itr is true.
func eventSub(t *testing.T, name string, itr bool) *activities.SubProcess {
	t.Helper()

	sp, err := activities.NewSubProcess(name, activities.WithTriggeredByEvent())
	require.NoError(t, err)

	start := sigStart(t, name+"-start", itr)
	task := spTask(t, name+"-task")
	end, err := events.NewEndEvent(name + "-end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, task, end} {
		require.NoError(t, sp.Add(e))
	}
	_, err = flow.Link(start, task)
	require.NoError(t, err)
	_, err = flow.Link(task, end)
	require.NoError(t, err)

	return sp
}

func TestEventSubProcessValidate(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	t.Run("one interrupting triggered start validates", func(t *testing.T) {
		es := eventSub(t, "ok", true)
		require.NoError(t, es.Validate())
		require.True(t, es.IsEventSubProcess())
	})

	t.Run("a plain sub-process is not an event sub-process", func(t *testing.T) {
		require.False(t, noneStartSP(t, "plain").IsEventSubProcess())
	})

	t.Run("non-interrupting (non-Error) start validates (SRD-053)", func(t *testing.T) {
		es := eventSub(t, "nonintr", false)
		require.NoError(t, es.Validate())
		require.True(t, es.IsEventSubProcess())
	})

	t.Run("non-interrupting Error start rejected (Error is interrupting-only)", func(t *testing.T) {
		err := errEventSubNI(t, "nierr", "E_X").Validate()
		require.Error(t, err)
		require.Contains(t, err.Error(), "must be interrupting")
	})

	t.Run("a None start is not a triggered start", func(t *testing.T) {
		sp, err := activities.NewSubProcess("nonestart",
			activities.WithTriggeredByEvent())
		require.NoError(t, err)
		start, err := events.NewStartEvent("s")
		require.NoError(t, err)
		require.NoError(t, sp.Add(start))

		err = sp.Validate()
		require.Error(t, err)
		require.Contains(t, err.Error(), "exactly one triggered Start Event")
	})

	t.Run("two triggered starts rejected", func(t *testing.T) {
		sp, err := activities.NewSubProcess("two",
			activities.WithTriggeredByEvent())
		require.NoError(t, err)
		require.NoError(t, sp.Add(sigStart(t, "a", true)))
		require.NoError(t, sp.Add(sigStart(t, "b", true)))

		err = sp.Validate()
		require.Error(t, err)
		require.Contains(t, err.Error(), "exactly one triggered Start Event")
	})

	t.Run("a None start mixed in is rejected", func(t *testing.T) {
		sp, err := activities.NewSubProcess("mix",
			activities.WithTriggeredByEvent())
		require.NoError(t, err)
		require.NoError(t, sp.Add(sigStart(t, "trig", true)))
		none, err := events.NewStartEvent("none")
		require.NoError(t, err)
		require.NoError(t, sp.Add(none))

		err = sp.Validate()
		require.Error(t, err)
	})

	t.Run("unflagged sub-process still rejects a triggered start", func(t *testing.T) {
		sp, err := activities.NewSubProcess("embedded")
		require.NoError(t, err)
		require.NoError(t, sp.Add(sigStart(t, "trig", true)))

		err = sp.Validate()
		require.Error(t, err)
		require.Contains(t, err.Error(), "triggered Start Event isn't allowed")
	})
}

// TestEventSubDoesNotBreakParentShape (SRD-052 FR-3): an inner event
// sub-process is skipped from the parent's entry classification, so a parent
// with a None start + an event-sub handler still validates.
func TestEventSubDoesNotBreakParentShape(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	parent, err := activities.NewSubProcess("parent")
	require.NoError(t, err)

	start, err := events.NewStartEvent("p-start")
	require.NoError(t, err)
	task := spTask(t, "p-task")
	end, err := events.NewEndEvent("p-end")
	require.NoError(t, err)
	es := eventSub(t, "handler", true)

	for _, e := range []flow.Element{start, task, end, es} {
		require.NoError(t, parent.Add(e))
	}
	_, err = flow.Link(start, task)
	require.NoError(t, err)
	_, err = flow.Link(task, end)
	require.NoError(t, err)

	require.NoError(t, parent.Validate(),
		"the event sub-process must not count as a flow-less entry")
}

// TestEventSubProcessClone (SRD-052 FR-1): the triggeredByEvent flag survives
// Clone.
func TestEventSubProcessClone(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	es := eventSub(t, "clone", true)

	cn, err := es.Clone()
	require.NoError(t, err)

	cs, ok := cn.(*activities.SubProcess)
	require.True(t, ok)
	require.True(t, cs.IsEventSubProcess(), "the triggered flag is copied")
}
