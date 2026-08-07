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
	"github.com/dr-dobermann/gobpm/pkg/repository"
	"github.com/dr-dobermann/gobpm/pkg/repository/memrepo"
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
	th.m.Unlock()
	th.state.Store(uint32(Started))

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
		th.m.Unlock()
		th.state.Store(uint32(Started))

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

// corrWaitProcess is corrStartProcess with a PARKING node between the start and
// the end: the conversation stays live — and therefore recoverable from its
// checkpoint — instead of completing the moment it starts.
func corrWaitProcess(t *testing.T, name, msgName string) *process.Process {
	t.Helper()

	require.NoError(t, data.CreateDefaultStates())

	mp := goexpr.Must(nil,
		data.MustItemDefinition(values.NewVariable("")),
		func(ctx context.Context, ds data.Source) (data.Value, error) {
			d, err := ds.Find(ctx, "order_in")
			if err != nil {
				return nil, err
			}

			return values.NewVariable(fmt.Sprint(d.Value().Get(ctx))), nil
		})

	re, err := bpmncommon.NewCorrelationPropertyRetrievalExpression(mp,
		bpmncommon.MustMessage(msgName, data.MustItemDefinition(
			values.NewVariable(""), foundation.WithID("order_in"))))
	require.NoError(t, err)

	prop, err := bpmncommon.NewCorrelationProperty("orderId", "string",
		[]bpmncommon.CorrelationPropertyRetrievalExpression{*re})
	require.NoError(t, err)

	key, err := bpmncommon.NewCorrelationKey("orderKey",
		[]bpmncommon.CorrelationProperty{*prop})
	require.NoError(t, err)

	// Every id is pinned: recovery resolves the recorded node ids against the
	// second engine's registration (the ADR-033 §2.8 deployment-parity rule).
	proc, err := process.New(name, foundation.WithID(name))
	require.NoError(t, err)

	start, err := events.NewStartEvent("start",
		events.WithMessageTrigger(events.MustMessageEventDefinition(
			bpmncommon.MustMessage(msgName, data.MustItemDefinition(
				values.NewVariable(""), foundation.WithID("order_in"))), nil)),
		events.WithCorrelationKey(key),
		foundation.WithID(name+"-start"))
	require.NoError(t, err)

	when := time.Now().Add(time.Hour)
	tdef, err := events.NewTimerEventDefinition(
		goexpr.Must(nil, data.MustItemDefinition(values.NewVariable(time.Time{})),
			func(_ context.Context, _ data.Source) (data.Value, error) {
				return values.NewVariable(when), nil
			}), nil, nil)
	require.NoError(t, err)

	wait, err := events.NewIntermediateCatchEvent("wait", tdef,
		foundation.WithID(name+"-wait"))
	require.NoError(t, err)

	end, err := events.NewEndEvent("end", foundation.WithID(name+"-end"))
	require.NoError(t, err)

	for _, e := range []flow.Element{start, wait, end} {
		require.NoError(t, proc.Add(e))
	}

	_, err = flow.Link(start, wait)
	require.NoError(t, err)
	_, err = flow.Link(wait, end)
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
//
// The conversations must be LIVE for that to be the question under test, so
// this uses the PARKING process rather than the start→end one. With a process
// that completes the moment it starts, the repeat key raced its own instance's
// end: joining won only when the message beat the completion, and once a
// finished conversation stopped reserving its key (FIX-036 §1.2) the repeat
// legitimately started a third instance instead — a real contract, wrongly
// reached, which showed up as an intermittent failure of this test rather than
// of the one that owns it (TestCorrelationKeyReleasedAfterInstanceEnds).
func TestCorrelationDedup(t *testing.T) {
	broker := membroker.New()

	th, err := New("corr", WithMessageBroker(broker))
	require.NoError(t, err)

	proc := corrWaitProcess(t, "p-corr", "order placed")
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

// TestStarterGuardHelpers covers the SRD-033 FR-3 log helpers' defensive
// branches: a non-message starter yields no message name, and a non-gateway
// node is not a Parallel start.
func TestStarterGuardHelpers(t *testing.T) {
	s := &instanceStarter{} // eDef nil — not a message definition
	require.Equal(t, "", s.messageName())

	require.False(t, parallelStart(nil))

	end, err := events.NewEndEvent("e")
	require.NoError(t, err)
	require.False(t, parallelStart(end))
}

// TestCorrelationKeyReleasedAfterInstanceEnds is FIX-036 T-2: the reservation
// belongs to a CONVERSATION, not to the key forever. While the first instance
// lives, a repeat key joins it (no duplicate — TestCorrelationDedup's rule);
// once it has finished, the same business key must start a NEW instance rather
// than being answered "joined existing instance" against an instance that no
// longer exists, which silently dropped the message.
func TestCorrelationKeyReleasedAfterInstanceEnds(t *testing.T) {
	broker := membroker.New()

	th, err := New("corr-reuse", WithMessageBroker(broker), WithoutBanner())
	require.NoError(t, err)

	proc := corrStartProcess(t, "p-corr-reuse", "order placed", "order placed")
	_, err = th.RegisterProcess(proc)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, th.Run(ctx))

	require.NoError(t, broker.Publish(ctx,
		messaging.Envelope{Name: "order placed", Payload: "ORD-42"}))

	require.Eventually(t, func() bool { return instanceCount(th) == 1 },
		3*time.Second, 10*time.Millisecond)

	// the first conversation runs to its end (the process is start → end).
	require.Eventually(t, func() bool {
		return len(th.Instances(InstancesCompleted)) == 1
	}, 3*time.Second, 10*time.Millisecond, "the first instance must finish")

	// the SAME key again: a finished conversation is not a conversation.
	require.NoError(t, broker.Publish(ctx,
		messaging.Envelope{Name: "order placed", Payload: "ORD-42"}))

	require.Eventually(t, func() bool { return instanceCount(th) == 2 },
		3*time.Second, 10*time.Millisecond,
		"a repeat business key must start a new instance once the first ended")

	// and the reservation now names the SECOND instance, not the first.
	th.m.Lock()
	owner := th.seenKeys[nsKeyFor(proc.ID(), "ORD-42")]
	th.m.Unlock()

	require.NotEmpty(t, owner)
	require.NotEqual(t, keyInFlight, owner)
}

// TestForgetReleasesKeyAndSettled is FIX-036 T-3: Forget is the package's
// reaping path and now reaps everything the engine holds for the instance —
// its registration, its terminal signal and its correlation reservation — so
// neither map grows for the engine's lifetime.
func TestForgetReleasesKeyAndSettled(t *testing.T) {
	broker := membroker.New()

	th, err := New("corr-forget", WithMessageBroker(broker), WithoutBanner())
	require.NoError(t, err)

	proc := corrStartProcess(t, "p-corr-forget", "order placed", "order placed")
	_, err = th.RegisterProcess(proc)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, th.Run(ctx))

	require.NoError(t, broker.Publish(ctx,
		messaging.Envelope{Name: "order placed", Payload: "ORD-7"}))

	require.Eventually(t, func() bool { return instanceCount(th) == 1 },
		3*time.Second, 10*time.Millisecond)

	require.Eventually(t, func() bool {
		return len(th.Instances(InstancesCompleted)) == 1
	}, 3*time.Second, 10*time.Millisecond)

	ids := th.Instances(InstancesCompleted)
	require.Len(t, ids, 1)

	id := ids[0]

	th.m.Lock()
	_, hadSettled := th.settled[id]
	th.m.Unlock()
	require.True(t, hadSettled, "the terminal signal is minted per instance")

	require.NoError(t, th.Forget(id))

	th.m.Lock()
	defer th.m.Unlock()

	require.NotContains(t, th.instances, id)
	require.NotContains(t, th.settled, id)
	require.NotContains(t, th.seenKeys, nsKeyFor(proc.ID(), "ORD-7"))
}

// TestRecoveredConversationKeepsItsKey is FIX-036 §1.2's restart half. The
// reservation map is in-memory, so an engine that rebuilds a live conversation
// from its checkpoint must re-take that conversation's correlation key.
// Without it the recovered instance is unreserved and the very next message
// carrying its key starts a DUPLICATE conversation beside it.
func TestRecoveredConversationKeepsItsKey(t *testing.T) {
	repo := memrepo.New()
	broker := membroker.New()

	proc := corrWaitProcess(t, "p-corr-rec", "order placed")

	// Engine A: starts the conversation, which parks on its timer and is
	// checkpointed with its ConvKeys — then is abandoned, not shut down.
	thA, err := New("corr-rec", WithoutBanner(), WithRepository(repo),
		WithMessageBroker(broker), WithLeaseTTL(50*time.Millisecond))
	require.NoError(t, err)

	_, err = thA.RegisterProcess(proc)
	require.NoError(t, err)

	ctxA, cancelA := context.WithCancel(context.Background())
	require.NoError(t, thA.Run(ctxA))

	require.NoError(t, broker.Publish(ctxA,
		messaging.Envelope{Name: "order placed", Payload: "ORD-9"}))

	require.Eventually(t, func() bool { return instanceCount(thA) == 1 },
		3*time.Second, 10*time.Millisecond)

	var instID string

	thA.m.Lock()
	for id := range thA.instances {
		instID = id
	}
	thA.m.Unlock()

	require.Eventually(t, func() bool {
		rec, ok, lerr := repo.Load(context.Background(), instID)

		return lerr == nil && ok && len(rec.Payload) > 0
	}, 3*time.Second, 10*time.Millisecond, "the parked conversation must checkpoint")

	// "Crash" engine A the way the restart-recovery tests do: fence it out
	// with a foreign, already-expired claim so its terminal write on cancel is
	// CAS-rejected and the record stays Active — an abandoned conversation,
	// which is what the next engine recovers.
	rec, ok, err := repo.Load(context.Background(), instID)
	require.True(t, ok)
	require.NoError(t, err)

	rec.Lease = repository.Lease{
		Owner:       "crash-sim",
		Incarnation: rec.Lease.Incarnation + 1,
		Expiry:      time.Now().Add(-time.Second),
	}
	require.NoError(t, repo.Save(context.Background(), rec))

	cancelA()

	time.Sleep(80 * time.Millisecond)

	// Engine B recovers it. Its reservation map starts empty — the map never
	// crosses a process boundary, which is the whole defect.
	thB, err := New("corr-rec", WithoutBanner(), WithRepository(repo),
		WithMessageBroker(membroker.New()))
	require.NoError(t, err)

	_, err = thB.RegisterProcess(proc)
	require.NoError(t, err)

	ctxB, cancelB := context.WithCancel(context.Background())
	defer cancelB()
	require.NoError(t, thB.Run(ctxB))

	require.Eventually(t, func() bool { return instanceCount(thB) == 1 },
		3*time.Second, 10*time.Millisecond, "engine B must recover the instance")

	thB.m.Lock()
	owner, reserved := thB.seenKeys[nsKeyFor(proc.ID(), "ORD-9")]
	thB.m.Unlock()

	require.True(t, reserved,
		"the recovered conversation must own its correlation key again")
	require.Equal(t, instID, owner,
		"and the reservation must name the recovered instance")
}
