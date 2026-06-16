package thresher

import (
	"context"
	"fmt"
	"testing"

	"github.com/dr-dobermann/gobpm/generated/mockeventproc"
	"github.com/dr-dobermann/gobpm/internal/instance/snapshot"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// recvTaskProcess builds a process whose entry is a ReceiveTask waiting for
// msgName. withIncoming prepends a none start (so the receiver is mid-flow, not
// a start); instantiate toggles WithInstantiate.
func recvTaskProcess(
	t *testing.T, name, msgName string, instantiate, withIncoming bool,
) *process.Process {
	t.Helper()

	require.NoError(t, data.CreateDefaultStates())

	proc, err := process.New(name)
	require.NoError(t, err)

	opts := []options.Option{activities.WithoutParams()}
	if instantiate {
		opts = append(opts, activities.WithInstantiate())
	}

	recv, err := activities.NewReceiveTask(name+"-recv",
		bpmncommon.MustMessage(msgName,
			data.MustItemDefinition(values.NewVariable(""),
				foundation.WithID("order_in"))),
		opts...)
	require.NoError(t, err)

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	require.NoError(t, proc.Add(recv))
	require.NoError(t, proc.Add(end))
	_, err = flow.Link(recv, end)
	require.NoError(t, err)

	if withIncoming {
		start, err := events.NewStartEvent("start")
		require.NoError(t, err)
		require.NoError(t, proc.Add(start))
		_, err = flow.Link(start, recv)
		require.NoError(t, err)
	}

	return proc
}

func TestScanInstantiatingReceiveTask(t *testing.T) {
	th, err := New("scan-recv")
	require.NoError(t, err)

	t.Run("no-incoming instantiate ReceiveTask is a starter", func(t *testing.T) {
		s, err := snapshot.New(recvTaskProcess(t, "p-i", "order placed", true, false))
		require.NoError(t, err)

		starters := scanInstantiatingStarts(s, th)
		require.Len(t, starters, 1)
		require.Equal(t, "order placed", starters[0].eDef.Message().Name())
	})

	t.Run("non-instantiate ReceiveTask is not a starter", func(t *testing.T) {
		// a non-instantiate receiver must be mid-flow (a no-incoming
		// non-instantiate ReceiveTask with an EndEvent is an invalid process —
		// it has no instantiation point).
		s, err := snapshot.New(recvTaskProcess(t, "p-n", "order placed", false, true))
		require.NoError(t, err)

		require.Empty(t, scanInstantiatingStarts(s, th))
	})

	t.Run("instantiate ReceiveTask with an incoming flow is not a starter",
		func(t *testing.T) {
			s, err := snapshot.New(
				recvTaskProcess(t, "p-in", "order placed", true, true))
			require.NoError(t, err)

			require.Empty(t, scanInstantiatingStarts(s, th))
		})
}

// msgStartProcess builds a process whose start is a message StartEvent (no
// incoming flow) wired to an EndEvent.
func msgStartProcess(t *testing.T, name, msgName string) *process.Process {
	t.Helper()

	require.NoError(t, data.CreateDefaultStates())

	proc, err := process.New(name)
	require.NoError(t, err)

	start, err := events.NewStartEvent("start",
		events.WithMessageTrigger(
			events.MustMessageEventDefinition(
				bpmncommon.MustMessage(msgName,
					data.MustItemDefinition(values.NewVariable(""),
						foundation.WithID("order_in"))),
				nil)))
	require.NoError(t, err)

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	require.NoError(t, proc.Add(start))
	require.NoError(t, proc.Add(end))

	_, err = flow.Link(start, end)
	require.NoError(t, err)

	return proc
}

// noneStartProcess builds a plain none-start process (no instantiating trigger).
func noneStartProcess(t *testing.T, name string) *process.Process {
	t.Helper()

	proc, err := process.New(name)
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	require.NoError(t, proc.Add(start))
	require.NoError(t, proc.Add(end))

	_, err = flow.Link(start, end)
	require.NoError(t, err)

	return proc
}

func TestScanInstantiatingStarts(t *testing.T) {
	th, err := New("scan-test")
	require.NoError(t, err)

	t.Run("message start trigger found", func(t *testing.T) {
		s, err := snapshot.New(msgStartProcess(t, "p-msg", "order placed"))
		require.NoError(t, err)

		starters := scanInstantiatingStarts(s, th)
		require.Len(t, starters, 1)
		require.Equal(t, "order placed", starters[0].eDef.Message().Name())
		require.NotEmpty(t, starters[0].ID())
		require.Same(t, s, starters[0].snapshot)
		require.NotNil(t, starters[0].startNode)
	})

	t.Run("none start yields no starters", func(t *testing.T) {
		s, err := snapshot.New(noneStartProcess(t, "p-none"))
		require.NoError(t, err)

		starters := scanInstantiatingStarts(s, th)
		require.Empty(t, starters)
	})
}

func TestRegisterProcessStarters(t *testing.T) {
	t.Run("auto registers a starter (bookkeeping)", func(t *testing.T) {
		th, err := New("auto")
		require.NoError(t, err)

		proc := msgStartProcess(t, "p-auto", "order placed")
		require.NoError(t, th.RegisterProcess(proc))

		th.m.Lock()
		got := th.starters[proc.ID()]
		th.m.Unlock()
		require.Len(t, got, 1)
	})

	t.Run("manual-start registers none", func(t *testing.T) {
		th, err := New("manual")
		require.NoError(t, err)

		proc := msgStartProcess(t, "p-manual", "order placed")
		require.NoError(t, th.RegisterProcess(proc, WithManualStart()))

		th.m.Lock()
		got := th.starters[proc.ID()]
		th.m.Unlock()
		require.Empty(t, got)
	})

	t.Run("a failing register option is surfaced", func(t *testing.T) {
		th, err := New("opt-err")
		require.NoError(t, err)

		boom := func(*registerConfig) error {
			return fmt.Errorf("bad register option")
		}
		require.Error(t, th.RegisterProcess(noneStartProcess(t, "p-opt"), boom))
	})

	t.Run("re-registration is idempotent", func(t *testing.T) {
		th, err := New("idem")
		require.NoError(t, err)

		proc := msgStartProcess(t, "p-idem", "order placed")
		require.NoError(t, th.RegisterProcess(proc))
		require.NoError(t, th.RegisterProcess(proc))

		th.m.Lock()
		got := th.starters[proc.ID()]
		th.m.Unlock()
		require.Len(t, got, 1)
	})
}

func TestStarterLifecycle(t *testing.T) {
	t.Run("register before Run, wired at Run, torn down", func(t *testing.T) {
		th, err := New("life-before")
		require.NoError(t, err)

		proc := msgStartProcess(t, "p-life", "order placed")
		require.NoError(t, th.RegisterProcess(proc))

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		require.NoError(t, th.Run(ctx))

		// A clean UnregisterProcess proves the starter WAS registered on the hub
		// at Run (the hub's UnregisterEvent would error ObjectNotFound were it
		// not), and clears the bookkeeping.
		require.NoError(t, th.UnregisterProcess(proc.ID()))

		th.m.Lock()
		_, hasStarters := th.starters[proc.ID()]
		_, hasSnap := th.snapshots[proc.ID()]
		th.m.Unlock()
		require.False(t, hasStarters)
		require.False(t, hasSnap)
	})

	t.Run("register after Run wires immediately", func(t *testing.T) {
		th, err := New("life-after")
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		require.NoError(t, th.Run(ctx))

		proc := msgStartProcess(t, "p-after", "order placed")
		require.NoError(t, th.RegisterProcess(proc))
		require.NoError(t, th.UnregisterProcess(proc.ID()))
	})

	t.Run("manual-start: no starter, clean teardown", func(t *testing.T) {
		th, err := New("life-manual")
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		require.NoError(t, th.Run(ctx))

		proc := msgStartProcess(t, "p-mlife", "order placed")
		require.NoError(t, th.RegisterProcess(proc, WithManualStart()))
		require.NoError(t, th.UnregisterProcess(proc.ID()))
	})

	t.Run("unregister unknown / empty id rejected", func(t *testing.T) {
		th, err := New("life-bad")
		require.NoError(t, err)

		require.Error(t, th.UnregisterProcess("nope"))
		require.Error(t, th.UnregisterProcess("   "))
	})
}

// TestLaunchInstanceFromEventError covers launchInstanceFromEvent's build-error
// wrap: a start node absent from the snapshot makes instance.NewFromEvent fail.
func TestLaunchInstanceFromEventError(t *testing.T) {
	th, err := New("launch-err")
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, th.Run(ctx))

	s, err := snapshot.New(noneStartProcess(t, "p-launch"))
	require.NoError(t, err)

	// a start node id that is not present in the snapshot → NewFromEvent fails.
	bogus, err := events.NewStartEvent("bogus")
	require.NoError(t, err)

	med := events.MustMessageEventDefinition(
		bpmncommon.MustMessage("m", data.MustItemDefinition(nil)), nil)

	require.Error(t, th.launchInstanceFromEvent(ctx, s, bogus, med))
}

// TestRegisterStartersError covers the registerStarters error path: a hub that
// rejects a persistent registration surfaces a wrapped error.
func TestRegisterStartersError(t *testing.T) {
	th, err := New("reg-err")
	require.NoError(t, err)

	s, err := snapshot.New(msgStartProcess(t, "p-regerr", "order placed"))
	require.NoError(t, err)

	starters := scanInstantiatingStarts(s, th)
	require.Len(t, starters, 1)

	mh := mockeventproc.NewMockEventHub(t)
	mh.EXPECT().
		RegisterPersistentEvent(mock.Anything, mock.Anything).
		Return(fmt.Errorf("hub rejected the subscription")).
		Once()
	th.eventHub = mh

	err = th.registerStarters(starters)
	require.Error(t, err)
}

// TestUnregisterProcessHubError covers the UnregisterProcess teardown error
// path: a hub that rejects UnregisterEvent surfaces a wrapped error.
func TestUnregisterProcessHubError(t *testing.T) {
	th, err := New("unreg-err")
	require.NoError(t, err)

	s, err := snapshot.New(msgStartProcess(t, "p-unreg", "order placed"))
	require.NoError(t, err)
	starters := scanInstantiatingStarts(s, th)
	require.Len(t, starters, 1)

	mh := mockeventproc.NewMockEventHub(t)
	mh.EXPECT().
		UnregisterEvent(mock.Anything, mock.Anything).
		Return(fmt.Errorf("hub rejected the teardown")).
		Once()
	th.eventHub = mh

	th.m.Lock()
	th.snapshots[s.ProcessID] = s
	th.starters[s.ProcessID] = starters
	th.state = Started
	th.m.Unlock()

	require.Error(t, th.UnregisterProcess(s.ProcessID))
}

// TestRunRegisterStartersError covers Run's startup-registration error path: a
// hub that rejects RegisterPersistentEvent at Run surfaces a wrapped error.
func TestRunRegisterStartersError(t *testing.T) {
	th, err := New("run-err")
	require.NoError(t, err)

	s, err := snapshot.New(msgStartProcess(t, "p-run", "order placed"))
	require.NoError(t, err)
	starters := scanInstantiatingStarts(s, th)
	require.Len(t, starters, 1)

	mh := mockeventproc.NewMockEventHub(t)
	mh.EXPECT().Start(mock.Anything).Return(nil).Once()
	mh.EXPECT().Run(mock.Anything).Return(nil).Maybe()
	mh.EXPECT().
		RegisterPersistentEvent(mock.Anything, mock.Anything).
		Return(fmt.Errorf("hub rejected the subscription")).
		Once()
	th.eventHub = mh

	th.m.Lock()
	th.snapshots[s.ProcessID] = s
	th.starters[s.ProcessID] = starters
	th.m.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.Error(t, th.Run(ctx))
}

