package thresher

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/generated/mockeventproc"
	"github.com/dr-dobermann/gobpm/internal/instance/snapshot"
	"github.com/dr-dobermann/gobpm/pkg/messaging"
	"github.com/dr-dobermann/gobpm/pkg/messaging/membroker"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
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
		require.Equal(t, "order placed", triggerName(starters[0].eDef))
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
		require.Equal(t, "order placed", triggerName(starters[0].eDef))
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
		_, err = th.RegisterProcess(proc)
		require.NoError(t, err)

		th.m.Lock()
		regs := th.registrations[proc.ID()]
		th.m.Unlock()
		require.Len(t, regs, 1)
		require.Len(t, regs[0].starters, 1)
	})

	t.Run("manual-start registers none", func(t *testing.T) {
		th, err := New("manual")
		require.NoError(t, err)

		proc := msgStartProcess(t, "p-manual", "order placed")
		_, err = th.RegisterProcess(proc, WithManualStart())
		require.NoError(t, err)

		th.m.Lock()
		regs := th.registrations[proc.ID()]
		th.m.Unlock()
		require.Len(t, regs, 1)
		require.Empty(t, regs[0].starters)
	})

	t.Run("a failing register option is surfaced", func(t *testing.T) {
		th, err := New("opt-err")
		require.NoError(t, err)

		boom := func(*registerConfig) error {
			return fmt.Errorf("bad register option")
		}
		_, err = th.RegisterProcess(noneStartProcess(t, "p-opt"), boom)
		require.Error(t, err)
	})

	t.Run("re-registration creates a new version", func(t *testing.T) {
		th, err := New("versioned")
		require.NoError(t, err)

		proc := msgStartProcess(t, "p-ver", "order placed")
		reg1, err := th.RegisterProcess(proc)
		require.NoError(t, err)
		reg2, err := th.RegisterProcess(proc)
		require.NoError(t, err)

		require.Equal(t, 1, reg1.Version())
		require.Equal(t, 2, reg2.Version())

		th.m.Lock()
		regs := th.registrations[proc.ID()]
		th.m.Unlock()
		require.Len(t, regs, 2)
	})
}

func TestStarterLifecycle(t *testing.T) {
	t.Run("register before Run, wired at Run, torn down", func(t *testing.T) {
		th, err := New("life-before")
		require.NoError(t, err)

		proc := msgStartProcess(t, "p-life", "order placed")
		reg, err := th.RegisterProcess(proc)
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		require.NoError(t, th.Run(ctx))

		// A clean UnregisterProcess proves the starter WAS registered on the hub
		// at Run (the hub's UnregisterEvent would error ObjectNotFound were it
		// not), and clears the bookkeeping.
		require.NoError(t, th.UnregisterVersion(reg))

		th.m.Lock()
		_, hasReg := th.registrations[proc.ID()]
		th.m.Unlock()
		require.False(t, hasReg)
	})

	t.Run("register after Run wires immediately", func(t *testing.T) {
		th, err := New("life-after")
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		require.NoError(t, th.Run(ctx))

		proc := msgStartProcess(t, "p-after", "order placed")
		reg, err := th.RegisterProcess(proc)
		require.NoError(t, err)
		require.NoError(t, th.UnregisterVersion(reg))
	})

	t.Run("manual-start: no starter, clean teardown", func(t *testing.T) {
		th, err := New("life-manual")
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		require.NoError(t, th.Run(ctx))

		proc := msgStartProcess(t, "p-mlife", "order placed")
		reg, err := th.RegisterProcess(proc, WithManualStart())
		require.NoError(t, err)
		require.NoError(t, th.UnregisterVersion(reg))
	})

	t.Run("nil / foreign handle rejected", func(t *testing.T) {
		th, err := New("life-bad")
		require.NoError(t, err)

		require.Error(t, th.UnregisterVersion(nil))

		// a handle for a process never registered in this engine is rejected.
		other, err := New("life-other")
		require.NoError(t, err)
		foreign, err := other.RegisterProcess(
			msgStartProcess(t, "p-foreign", "order placed"))
		require.NoError(t, err)
		require.Error(t, th.UnregisterVersion(foreign))
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

	require.Error(t, th.launchInstanceFromEvent(ctx, s, bogus, med, "", ""))
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

// TestUnregisterProcessHubError covers the UnregisterVersion teardown error
// path: a hub that rejects UnregisterEvent surfaces a wrapped error. (Name kept
// across the UnregisterProcess→UnregisterVersion split.)
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

	reg := &ProcessRegistration{
		key: s.ProcessID, version: 1, snapshot: s, starters: starters,
	}

	th.m.Lock()
	th.registrations[s.ProcessID] = []*ProcessRegistration{reg}
	th.state = Started
	th.m.Unlock()

	require.Error(t, th.UnregisterVersion(reg))
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
	th.registrations[s.ProcessID] = []*ProcessRegistration{
		{key: s.ProcessID, version: 1, snapshot: s, starters: starters},
	}
	th.m.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.Error(t, th.Run(ctx))
}

// TestRegisterProcessSupersedeHubErrors covers the two latest-supersedes error
// paths in RegisterProcess: tearing down the previous latest's starters, then
// registering the new version's, each surfaces a hub failure (SRD-031.A FR-7).
func TestRegisterProcessSupersedeHubErrors(t *testing.T) {
	proc := msgStartProcess(t, "p-sup-err", "order placed")

	seedV1 := func(t *testing.T, th *Thresher) string {
		t.Helper()

		s1, err := snapshot.New(proc)
		require.NoError(t, err)

		v1 := &ProcessRegistration{
			key: s1.ProcessID, version: 1, snapshot: s1,
			starters: scanInstantiatingStarts(s1, th),
		}

		th.m.Lock()
		th.registrations[s1.ProcessID] = []*ProcessRegistration{v1}
		th.state = Started
		th.m.Unlock()

		return s1.ProcessID
	}

	t.Run("teardown of the superseded version errors", func(t *testing.T) {
		th, err := New("sup-teardown")
		require.NoError(t, err)

		mh := mockeventproc.NewMockEventHub(t)
		mh.EXPECT().
			UnregisterEvent(mock.Anything, mock.Anything).
			Return(fmt.Errorf("hub teardown rejected")).
			Once()
		th.eventHub = mh

		seedV1(t, th)

		_, err = th.RegisterProcess(proc) // v2 supersedes → teardown fails
		require.Error(t, err)
	})

	t.Run("re-register of the new version errors", func(t *testing.T) {
		th, err := New("sup-rereg")
		require.NoError(t, err)

		mh := mockeventproc.NewMockEventHub(t)
		mh.EXPECT().
			UnregisterEvent(mock.Anything, mock.Anything).
			Return(nil).
			Once()
		mh.EXPECT().
			RegisterPersistentEvent(mock.Anything, mock.Anything).
			Return(fmt.Errorf("hub register rejected")).
			Once()
		th.eventHub = mh

		seedV1(t, th)

		_, err = th.RegisterProcess(proc) // teardown ok, re-register fails
		require.Error(t, err)
	})
}

// TestStartersSkipsEmptyVersionSlice covers the defensive empty-slice guard in
// Starters: a key mapped to a zero-length version slice contributes no starter.
func TestStartersSkipsEmptyVersionSlice(t *testing.T) {
	th, err := New("empty-slice")
	require.NoError(t, err)

	th.m.Lock()
	th.registrations["ghost"] = nil
	th.m.Unlock()

	require.Empty(t, th.Starters())
}

// corrStartProcess builds a message-start process declaring a CorrelationKey
// whose single property extracts the payload value (read from the message item)
// as the key. The start is wired to an EndEvent.
func corrStartProcess(t *testing.T, name, msgName, refName string) *process.Process {
	t.Helper()

	require.NoError(t, data.CreateDefaultStates())

	// the retrieval expression reads the payload (bound under the message item
	// id "order_in") and returns it as the partial key.
	mp := goexpr.Must(nil,
		data.MustItemDefinition(values.NewVariable("")),
		func(ctx context.Context, ds data.Source) (data.Value, error) {
			d, err := ds.Find(ctx, "order_in")
			if err != nil {
				return nil, err
			}

			return values.NewVariable(fmt.Sprint(d.Value().Get(ctx))), nil
		})

	// refName names the MessageRef the retrieval expression applies to; when it
	// differs from the start message name the key can't be derived (ok=false).
	re, err := bpmncommon.NewCorrelationPropertyRetrievalExpression(mp,
		bpmncommon.MustMessage(refName, data.MustItemDefinition(
			values.NewVariable(""), foundation.WithID("order_in"))))
	require.NoError(t, err)

	prop, err := bpmncommon.NewCorrelationProperty("orderId", "string",
		[]bpmncommon.CorrelationPropertyRetrievalExpression{*re})
	require.NoError(t, err)

	key, err := bpmncommon.NewCorrelationKey("orderKey",
		[]bpmncommon.CorrelationProperty{*prop})
	require.NoError(t, err)

	proc, err := process.New(name)
	require.NoError(t, err)

	start, err := events.NewStartEvent("start",
		events.WithMessageTrigger(events.MustMessageEventDefinition(
			bpmncommon.MustMessage(msgName, data.MustItemDefinition(
				values.NewVariable(""), foundation.WithID("order_in"))), nil)),
		events.WithCorrelationKey(key))
	require.NoError(t, err)

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	require.NoError(t, proc.Add(start))
	require.NoError(t, proc.Add(end))
	_, err = flow.Link(start, end)
	require.NoError(t, err)

	return proc
}

func instanceCount(th *Thresher) int {
	th.m.Lock()
	defer th.m.Unlock()

	return len(th.instances)
}

// TestCorrelationDedup is ADR-016 v.1 §2.3 / SRD-015 V6: messages with distinct
// derived keys spawn distinct instances; a repeat of a seen key joins the
// existing instance (no duplicate).
func TestCorrelationDedup(t *testing.T) {
	broker := membroker.New()

	th, err := New("corr", WithMessageBroker(broker))
	require.NoError(t, err)

	proc := corrStartProcess(t, "p-corr", "order placed", "order placed")
	_, err = th.RegisterProcess(proc)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, th.Run(ctx))

	// keys A, B, A — A and B each instantiate once; the second A joins.
	for _, k := range []string{"A", "B", "A"} {
		require.NoError(t, broker.Publish(ctx,
			messaging.Envelope{Name: "order placed", Payload: k}))
	}

	require.Eventually(t, func() bool { return instanceCount(th) == 2 },
		3*time.Second, 10*time.Millisecond,
		"two distinct keys must spawn exactly two instances")

	th.m.Lock()
	require.Len(t, th.seenKeys, 2)
	th.m.Unlock()

	// the duplicate A must not spawn a third instance.
	require.Never(t, func() bool { return instanceCount(th) > 2 },
		300*time.Millisecond, 50*time.Millisecond)
}

// TestCorrelationNoKeyEachInstantiates: with no CorrelationKey declared, every
// message instantiates (name-match, no dedup — the M3 behaviour preserved).
func TestCorrelationNoKeyEachInstantiates(t *testing.T) {
	broker := membroker.New()

	th, err := New("nocorr", WithMessageBroker(broker))
	require.NoError(t, err)

	_, err = th.RegisterProcess(msgStartProcess(t, "p-nocorr", "order placed"))
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, th.Run(ctx))

	for range 2 {
		require.NoError(t, broker.Publish(ctx,
			messaging.Envelope{Name: "order placed", Payload: "same"}))
	}

	require.Eventually(t, func() bool { return instanceCount(th) == 2 },
		3*time.Second, 10*time.Millisecond,
		"without a key, each message instantiates")
}

// TestCorrelationUnderivableKeyInstantiates: a declared key whose retrieval
// expression doesn't apply to the message (MessageRef mismatch) can't be
// derived (ok=false), so the message instantiates without dedup.
func TestCorrelationUnderivableKeyInstantiates(t *testing.T) {
	broker := membroker.New()

	th, err := New("corr-mismatch", WithMessageBroker(broker))
	require.NoError(t, err)

	// the retrieval MessageRef ("other") differs from the start message name.
	proc := corrStartProcess(t, "p-mm", "order placed", "other")
	_, err = th.RegisterProcess(proc)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, th.Run(ctx))

	for range 2 {
		require.NoError(t, broker.Publish(ctx,
			messaging.Envelope{Name: "order placed", Payload: "same"}))
	}

	require.Eventually(t, func() bool { return instanceCount(th) == 2 },
		3*time.Second, 10*time.Millisecond,
		"an underivable key instantiates per message (no dedup)")
}

// TestResolveAndLaunchRollback covers the create-or-route rollback: when the
// launch fails after a key is reserved, the reservation is dropped so a later
// message can retry.
func TestResolveAndLaunchRollback(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	th, err := New("rollback")
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, th.Run(ctx))

	s, err := snapshot.New(noneStartProcess(t, "p-rb"))
	require.NoError(t, err)

	// a start node absent from the snapshot makes launchInstanceFromEvent fail.
	bogus, err := events.NewStartEvent("bogus")
	require.NoError(t, err)

	eDef := events.MustMessageEventDefinition(
		bpmncommon.MustMessage("m", data.MustItemDefinition(nil)), nil)

	require.Error(t, th.resolveAndLaunch(ctx, s, bogus, eDef, "orderKey", "K1"))

	// the reservation was rolled back.
	th.m.Lock()
	_, seen := th.seenKeys["p-rb\x1fK1"]
	th.m.Unlock()
	require.False(t, seen)
}

// TestTriggerName covers triggerName's signal and non-message/non-signal
// branches (the message branch is covered through the Starters listing tests).
func TestTriggerName(t *testing.T) {
	sig, err := events.NewSignal("GO", nil)
	require.NoError(t, err)
	sed, err := events.NewSignalEventDefinition(sig)
	require.NoError(t, err)
	require.Equal(t, "GO", triggerName(sed))

	// a non-message/non-signal definition yields "".
	require.Equal(t, "", triggerName(nil))
}
