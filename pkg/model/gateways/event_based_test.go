package gateways_test

import (
	"context"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/eventproc"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/gateways"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/stretchr/testify/require"
)

// --- arm builders -----------------------------------------------------------

// signalDef builds a stand-alone signal event definition (no payload).
func signalDef(t *testing.T, name string) flow.EventDefinition {
	t.Helper()

	sig, err := events.NewSignal("sig-"+name, nil)
	require.NoError(t, err)

	def, err := events.NewSignalEventDefinition(sig)
	require.NoError(t, err)

	return def
}

// signalArm builds a signal IntermediateCatchEvent (a catch-event arm).
func signalArm(t *testing.T, name string) *events.IntermediateCatchEvent {
	t.Helper()

	ice, err := events.NewIntermediateCatchEvent(name, signalDef(t, name))
	require.NoError(t, err)

	return ice
}

// receiveArm builds a message Receive Task (an activity arm).
func receiveArm(t *testing.T, name string) *activities.ReceiveTask {
	t.Helper()

	item := data.MustItemDefinition(values.NewVariable(0))

	msg, err := bpmncommon.NewMessage("msg-"+name, item)
	require.NoError(t, err)

	rt, err := activities.NewReceiveTask(name, msg)
	require.NoError(t, err)

	return rt
}

// messageCatchArm builds a Message IntermediateCatchEvent (a message catch-event arm).
func messageCatchArm(
	t *testing.T, name string,
) *events.IntermediateCatchEvent {
	t.Helper()

	item := data.MustItemDefinition(values.NewVariable(0))

	def, err := events.NewMessageEventDefinition(
		bpmncommon.MustMessage("msg-"+name, item), nil)
	require.NoError(t, err)

	ice, err := events.NewIntermediateCatchEvent(name, def)
	require.NoError(t, err)

	return ice
}

// ebLink links src -> trg, failing the test on error.
func ebLink(
	t *testing.T,
	src flow.SequenceSource,
	trg flow.SequenceTarget,
	opts ...options.Option,
) {
	t.Helper()

	_, err := flow.Link(src, trg, opts...)
	require.NoError(t, err)
}

// --- stub arms (for routing / catch-capability branches) --------------------

// stubArm is a minimal event-node arm that records the event routed into it.
type stubArm struct {
	flow.BaseNode
	foundation.BaseElement
	defs []flow.EventDefinition
	got  flow.EventDefinition
}

func newStubArm(
	t *testing.T, name string, defs ...flow.EventDefinition,
) *stubArm {
	t.Helper()

	fn, err := flow.NewBaseNode(name)
	require.NoError(t, err)

	return &stubArm{
		BaseNode:    *fn,
		BaseElement: *foundation.MustBaseElement(),
		defs:        defs,
	}
}

func (a *stubArm) Node() flow.Node                              { return a }
func (a *stubArm) NodeType() flow.NodeType                      { return flow.EventNodeType }
func (a *stubArm) SupportOutgoingFlow(*flow.SequenceFlow) error { return nil }
func (a *stubArm) AcceptIncomingFlow(*flow.SequenceFlow) error  { return nil }
func (a *stubArm) Definitions() []flow.EventDefinition          { return a.defs }
func (a *stubArm) EventClass() flow.EventClass {
	return flow.IntermediateEventClass
}

func (a *stubArm) ProcessEvent(_ context.Context, d flow.EventDefinition) error {
	a.got = d

	return nil
}

// stubNoProc is an event-node arm that CANNOT process events (no ProcessEvent).
type stubNoProc struct {
	flow.BaseNode
	foundation.BaseElement
	defs []flow.EventDefinition
}

func newStubNoProc(
	t *testing.T, name string, defs ...flow.EventDefinition,
) *stubNoProc {
	t.Helper()

	fn, err := flow.NewBaseNode(name)
	require.NoError(t, err)

	return &stubNoProc{
		BaseNode:    *fn,
		BaseElement: *foundation.MustBaseElement(),
		defs:        defs,
	}
}

func (a *stubNoProc) Node() flow.Node                              { return a }
func (a *stubNoProc) NodeType() flow.NodeType                      { return flow.EventNodeType }
func (a *stubNoProc) SupportOutgoingFlow(*flow.SequenceFlow) error { return nil }
func (a *stubNoProc) AcceptIncomingFlow(*flow.SequenceFlow) error  { return nil }
func (a *stubNoProc) Definitions() []flow.EventDefinition          { return a.defs }
func (a *stubNoProc) EventClass() flow.EventClass {
	return flow.IntermediateEventClass
}

var (
	_ flow.EventNode           = (*stubArm)(nil)
	_ eventproc.EventProcessor = (*stubArm)(nil)
	_ flow.EventNode           = (*stubNoProc)(nil)
)

// ============================================================================

func TestNewEventBasedGateway(t *testing.T) {
	g, err := gateways.NewEventBasedGateway(
		foundation.WithID("eb-1"),
		options.WithName("my event gateway"),
		gateways.WithDirection(gateways.Diverging))
	require.NoError(t, err)
	require.NotNil(t, g)
	require.Same(t, flow.Node(g), g.Node())
}

func TestEventBasedGatewayClone(t *testing.T) {
	g, err := gateways.NewEventBasedGateway()
	require.NoError(t, err)

	c, err := g.Clone()
	require.NoError(t, err)
	require.NotNil(t, c)

	cg, ok := c.(*gateways.EventBasedGateway)
	require.True(t, ok)
	require.NotSame(t, g, cg)
}

func TestEventBasedGatewayStartAttributes(t *testing.T) {
	t.Run("default mid-flow exclusive", func(t *testing.T) {
		g, err := gateways.NewEventBasedGateway()
		require.NoError(t, err)
		require.False(t, g.Instantiate())
		require.Equal(t, gateways.ExclusiveEvents, g.EventGatewayType())
	})

	t.Run("instantiate + parallel read back", func(t *testing.T) {
		g, err := gateways.NewEventBasedGateway(
			gateways.WithInstantiate(),
			gateways.WithEventGatewayType(gateways.ParallelEvents))
		require.NoError(t, err)
		require.True(t, g.Instantiate())
		require.Equal(t, gateways.ParallelEvents, g.EventGatewayType())
	})
}

func TestEventBasedGatewayCloneCarriesStart(t *testing.T) {
	g, err := gateways.NewEventBasedGateway(
		gateways.WithInstantiate(),
		gateways.WithEventGatewayType(gateways.ParallelEvents))
	require.NoError(t, err)

	cn, err := g.Clone()
	require.NoError(t, err)

	cg, ok := cn.(*gateways.EventBasedGateway)
	require.True(t, ok)
	require.True(t, cg.Instantiate())
	require.Equal(t, gateways.ParallelEvents, cg.EventGatewayType())
}

func TestEventBasedGatewayDefinitions(t *testing.T) {
	g, err := gateways.NewEventBasedGateway()
	require.NoError(t, err)

	a, b := signalArm(t, "a"), signalArm(t, "b")
	ebLink(t, g, a)
	ebLink(t, g, b)

	defs := g.Definitions()
	require.Len(t, defs, 2)

	got := map[string]bool{}
	for _, d := range defs {
		got[d.ID()] = true
	}

	require.True(t, got[a.Definitions()[0].ID()])
	require.True(t, got[b.Definitions()[0].ID()])
}

func TestEventBasedGatewayEventClass(t *testing.T) {
	g, err := gateways.NewEventBasedGateway()
	require.NoError(t, err)
	require.Equal(t, flow.IntermediateEventClass, g.EventClass())
}

func TestEventBasedGatewayProcessEventRoutes(t *testing.T) {
	g, err := gateways.NewEventBasedGateway()
	require.NoError(t, err)

	dA, dB := signalDef(t, "a"), signalDef(t, "b")
	armA := newStubArm(t, "a", dA)
	armB := newStubArm(t, "b", dB)
	ebLink(t, g, armA)
	ebLink(t, g, armB)

	// a known event routes to its owning arm only.
	require.NoError(t, g.ProcessEvent(context.Background(), dB))
	require.Nil(t, armA.got)
	require.Equal(t, dB.ID(), armB.got.ID())
}

func TestEventBasedGatewayProcessEventUnknown(t *testing.T) {
	g, err := gateways.NewEventBasedGateway()
	require.NoError(t, err)

	ebLink(t, g, newStubArm(t, "a", signalDef(t, "a")))

	err = g.ProcessEvent(context.Background(), signalDef(t, "x"))
	require.ErrorContains(t, err, "no arm owns")
}

func TestEventBasedGatewayProcessEventArmCannotProcess(t *testing.T) {
	g, err := gateways.NewEventBasedGateway()
	require.NoError(t, err)

	d := signalDef(t, "a")
	ebLink(t, g, newStubNoProc(t, "a", d))

	err = g.ProcessEvent(context.Background(), d)
	require.ErrorContains(t, err, "cannot process events")
}

func TestEventBasedGatewayExecRejected(t *testing.T) {
	g, err := gateways.NewEventBasedGateway()
	require.NoError(t, err)

	out, err := g.Exec(context.Background(), nil)
	require.Error(t, err)
	require.Nil(t, out)
	require.ErrorContains(t, err, "must not be executed")
}

func TestEventBasedGatewayValidate(t *testing.T) {
	// message/receive arms build ItemDefinitions, which need the default data states.
	require.NoError(t, data.CreateDefaultStates())

	t.Run("valid two signal arms", func(t *testing.T) {
		g, err := gateways.NewEventBasedGateway()
		require.NoError(t, err)
		ebLink(t, g, signalArm(t, "a"))
		ebLink(t, g, signalArm(t, "b"))
		require.NoError(t, g.Validate())
	})

	t.Run("fewer than two arms", func(t *testing.T) {
		g, err := gateways.NewEventBasedGateway()
		require.NoError(t, err)
		ebLink(t, g, signalArm(t, "a"))
		require.ErrorContains(t, g.Validate(), "at least two arms")
	})

	t.Run("arm is not an event node", func(t *testing.T) {
		g, err := gateways.NewEventBasedGateway()
		require.NoError(t, err)
		ebLink(t, g, signalArm(t, "a"))
		ebLink(t, g, newDummyNode("plain"))
		require.ErrorContains(t, g.Validate(),
			"intermediate catch event or a Receive Task")
	})

	t.Run("arm cannot catch its event", func(t *testing.T) {
		g, err := gateways.NewEventBasedGateway()
		require.NoError(t, err)
		ebLink(t, g, signalArm(t, "a"))
		ebLink(t, g, newStubNoProc(t, "b", signalDef(t, "b")))
		require.ErrorContains(t, g.Validate(), "able to catch its event")
	})

	t.Run("arm with two incoming flows", func(t *testing.T) {
		g, err := gateways.NewEventBasedGateway()
		require.NoError(t, err)
		a := signalArm(t, "a")
		ebLink(t, g, a)
		ebLink(t, g, signalArm(t, "b"))
		ebLink(t, newDummyNode("other"), a) // a now has 2 incoming
		require.ErrorContains(t, g.Validate(),
			"exactly one incoming flow")
	})

	t.Run("conditioned arm flow", func(t *testing.T) {
		g, err := gateways.NewEventBasedGateway()
		require.NoError(t, err)
		ebLink(t, g, signalArm(t, "a"),
			flow.WithCondition(boolCond(t, func(x int) bool { return x > 0 })))
		ebLink(t, g, signalArm(t, "b"))
		require.ErrorContains(t, g.Validate(), "must not carry a condition")
	})

	t.Run("unsupported trigger (conditional arm)", func(t *testing.T) {
		g, err := gateways.NewEventBasedGateway()
		require.NoError(t, err)

		cdef, err := events.NewConditionalEventDefinition(
			boolCond(t, func(x int) bool { return x > 0 }))
		require.NoError(t, err)
		cond, err := events.NewIntermediateCatchEvent("cond", cdef)
		require.NoError(t, err)

		ebLink(t, g, signalArm(t, "a"))
		ebLink(t, g, cond)
		require.ErrorContains(t, g.Validate(), "unsupported arm trigger")
	})

	t.Run("message catch + receive task rejected", func(t *testing.T) {
		g, err := gateways.NewEventBasedGateway()
		require.NoError(t, err)
		ebLink(t, g, messageCatchArm(t, "a"))
		ebLink(t, g, receiveArm(t, "b"))
		require.ErrorContains(t, g.Validate(), "§10.6.6")
	})

	t.Run("signal catch + receive task allowed", func(t *testing.T) {
		g, err := gateways.NewEventBasedGateway()
		require.NoError(t, err)
		ebLink(t, g, signalArm(t, "a"))
		ebLink(t, g, receiveArm(t, "b"))
		require.NoError(t, g.Validate())
	})

	t.Run("instantiating exclusive gate is valid", func(t *testing.T) {
		g, err := gateways.NewEventBasedGateway(gateways.WithInstantiate())
		require.NoError(t, err)
		ebLink(t, g, messageCatchArm(t, "a"))
		ebLink(t, g, messageCatchArm(t, "b"))
		require.NoError(t, g.Validate())
	})

	t.Run("instantiating parallel gate with a covering key is valid",
		func(t *testing.T) {
			g, err := gateways.NewEventBasedGateway(
				gateways.WithInstantiate(),
				gateways.WithEventGatewayType(gateways.ParallelEvents),
				gateways.WithCorrelationKey(corrKeyFor(t, "msg-a", "msg-b")))
			require.NoError(t, err)
			ebLink(t, g, messageCatchArm(t, "a"))
			ebLink(t, g, messageCatchArm(t, "b"))
			require.NoError(t, g.Validate())
		})

	t.Run("instantiating gate with incoming flow rejected", func(t *testing.T) {
		g, err := gateways.NewEventBasedGateway(gateways.WithInstantiate())
		require.NoError(t, err)
		ebLink(t, g, messageCatchArm(t, "a"))
		ebLink(t, g, messageCatchArm(t, "b"))
		ebLink(t, newDummyNode("up"), g) // the gate gets an incoming flow
		require.ErrorContains(t, g.Validate(),
			"instantiating gate must have no")
	})

	t.Run("instantiating gate with non-message arm rejected", func(t *testing.T) {
		g, err := gateways.NewEventBasedGateway(gateways.WithInstantiate())
		require.NoError(t, err)
		ebLink(t, g, messageCatchArm(t, "a"))
		ebLink(t, g, signalArm(t, "b")) // a Signal cannot instantiate
		require.ErrorContains(t, g.Validate(), "message-based")
	})

	t.Run("parallel without instantiate rejected", func(t *testing.T) {
		g, err := gateways.NewEventBasedGateway(
			gateways.WithEventGatewayType(gateways.ParallelEvents))
		require.NoError(t, err)
		ebLink(t, g, signalArm(t, "a"))
		ebLink(t, g, signalArm(t, "b"))
		require.ErrorContains(t, g.Validate(), "requires WithInstantiate")
	})
}

// TestParallelStartRequiresCorrelationKey covers SRD-033 FR-1 (ADR-005 v.4
// §2.12.5 rule 7): a keyless Parallel-start gate is rejected at validation —
// without a key every arm message would spawn its own stuck instance — while
// the same gate with a covering key passes.
func TestParallelStartRequiresCorrelationKey(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	keyless, err := gateways.NewEventBasedGateway(
		gateways.WithInstantiate(),
		gateways.WithEventGatewayType(gateways.ParallelEvents))
	require.NoError(t, err)
	ebLink(t, keyless, messageCatchArm(t, "a"))
	ebLink(t, keyless, messageCatchArm(t, "b"))

	err = keyless.Validate()
	require.ErrorContains(t, err, "must declare a CorrelationKey")
	require.ErrorContains(t, err, keyless.ID())

	keyed, err := gateways.NewEventBasedGateway(
		gateways.WithInstantiate(),
		gateways.WithEventGatewayType(gateways.ParallelEvents),
		gateways.WithCorrelationKey(corrKeyFor(t, "msg-a", "msg-b")))
	require.NoError(t, err)
	ebLink(t, keyed, messageCatchArm(t, "a"))
	ebLink(t, keyed, messageCatchArm(t, "b"))

	require.NoError(t, keyed.Validate())
}

// TestParallelStartArmMustCoverKey covers SRD-033 FR-2: a keyed Parallel-start
// gate whose arm message lacks a retrieval expression for a key property is
// rejected naming the arm and the uncovered property (BPMN §10.6.6 — the
// gate's messages MUST share the same correlation information).
func TestParallelStartArmMustCoverKey(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	// the key covers msg-a only; arm "b" consumes msg-b — uncovered.
	g, err := gateways.NewEventBasedGateway(
		gateways.WithInstantiate(),
		gateways.WithEventGatewayType(gateways.ParallelEvents),
		gateways.WithCorrelationKey(corrKeyFor(t, "msg-a")))
	require.NoError(t, err)
	ebLink(t, g, messageCatchArm(t, "a"))

	armB := messageCatchArm(t, "b")
	ebLink(t, g, armB)

	err = g.Validate()
	require.ErrorContains(t, err, "doesn't cover the gate's CorrelationKey")
	require.ErrorContains(t, err, armB.ID())
	require.ErrorContains(t, err, "msg-b")
	require.ErrorContains(t, err, "p") // the uncovered property name
}

// TestParallelStartCorrelationArmResolution covers armMessage's branches
// (SRD-033 §3.2): a Receive-Task arm resolves its message via its own
// Message(); a non-message arm carries no message and is left to the
// message-based start rule.
func TestParallelStartCorrelationArmResolution(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	t.Run("receive-task arms covered by the key", func(t *testing.T) {
		g, err := gateways.NewEventBasedGateway(
			gateways.WithInstantiate(),
			gateways.WithEventGatewayType(gateways.ParallelEvents),
			gateways.WithCorrelationKey(corrKeyFor(t, "msg-a", "msg-b")))
		require.NoError(t, err)
		ebLink(t, g, receiveArm(t, "a"))
		ebLink(t, g, receiveArm(t, "b"))

		require.NoError(t, g.Validate())
	})

	t.Run("uncovered receive-task arm rejected", func(t *testing.T) {
		g, err := gateways.NewEventBasedGateway(
			gateways.WithInstantiate(),
			gateways.WithEventGatewayType(gateways.ParallelEvents),
			gateways.WithCorrelationKey(corrKeyFor(t, "msg-a")))
		require.NoError(t, err)
		ebLink(t, g, receiveArm(t, "a"))
		ebLink(t, g, receiveArm(t, "b"))

		require.ErrorContains(t, g.Validate(),
			"doesn't cover the gate's CorrelationKey")
	})

	t.Run("non-message arm reported by the message-based rule",
		func(t *testing.T) {
			g, err := gateways.NewEventBasedGateway(
				gateways.WithInstantiate(),
				gateways.WithEventGatewayType(gateways.ParallelEvents),
				gateways.WithCorrelationKey(corrKeyFor(t, "msg-a")))
			require.NoError(t, err)
			ebLink(t, g, messageCatchArm(t, "a"))
			ebLink(t, g, signalArm(t, "b")) // event node, no message
			ebLink(t, g, newDummyNode("c")) // not an event node at all

			require.ErrorContains(t, g.Validate(), "message-based")
		})
}

// stubTaskBoundary is an activity-typed arm that reports boundary events.
type stubTaskBoundary struct {
	flow.BaseNode
	foundation.BaseElement
	defs     []flow.EventDefinition
	boundary []flow.EventNode
}

func newStubTaskBoundary(t *testing.T, name string) *stubTaskBoundary {
	t.Helper()

	fn, err := flow.NewBaseNode(name)
	require.NoError(t, err)

	return &stubTaskBoundary{
		BaseNode:    *fn,
		BaseElement: *foundation.MustBaseElement(),
		defs:        []flow.EventDefinition{signalDef(t, name)},
		boundary:    []flow.EventNode{signalArm(t, name+"-be")},
	}
}

func (a *stubTaskBoundary) Node() flow.Node { return a }
func (a *stubTaskBoundary) NodeType() flow.NodeType {
	return flow.ActivityNodeType
}
func (a *stubTaskBoundary) SupportOutgoingFlow(*flow.SequenceFlow) error { return nil }
func (a *stubTaskBoundary) AcceptIncomingFlow(*flow.SequenceFlow) error  { return nil }
func (a *stubTaskBoundary) Definitions() []flow.EventDefinition          { return a.defs }
func (a *stubTaskBoundary) EventClass() flow.EventClass {
	return flow.IntermediateEventClass
}

func (a *stubTaskBoundary) ProcessEvent(context.Context, flow.EventDefinition) error {
	return nil
}

func (a *stubTaskBoundary) BoundaryEvents() []flow.EventNode { return a.boundary }

func TestEventBasedGatewayArmForSkipsNonEvent(t *testing.T) {
	g, err := gateways.NewEventBasedGateway()
	require.NoError(t, err)

	d := signalDef(t, "a")
	ebLink(t, g, newDummyNode("plain")) // not an event node — armFor skips it
	arm := newStubArm(t, "a", d)
	ebLink(t, g, arm)

	require.NoError(t, g.ProcessEvent(context.Background(), d))
	require.Equal(t, d.ID(), arm.got.ID())
}

func TestNewEventBasedGatewayBadOption(t *testing.T) {
	_, err := gateways.NewEventBasedGateway(
		gateways.WithDirection(gateways.GDirection("bogus")))
	require.Error(t, err)
}

// TestWithCorrelationKeyNil covers the WithCorrelationKey nil guard: a nil key is
// rejected with a self-naming error rather than silently erasing the gate's
// correlation (the option-constructor validation rule).
func TestWithCorrelationKeyNil(t *testing.T) {
	_, err := gateways.NewEventBasedGateway(gateways.WithCorrelationKey(nil))
	require.ErrorContains(t, err, "WithCorrelationKey")
}

// minimalCorrKey builds a syntactically valid one-property CorrelationKey.
func minimalCorrKey(t *testing.T) *bpmncommon.CorrelationKey {
	t.Helper()

	return corrKeyFor(t, "m")
}

// corrKeyFor builds a one-property CorrelationKey whose retrieval expressions
// cover exactly the given message names (SRD-033: an arm message covers the key
// when every key property declares a retrieval expression for it).
func corrKeyFor(t *testing.T, msgNames ...string) *bpmncommon.CorrelationKey {
	t.Helper()

	res := make(
		[]bpmncommon.CorrelationPropertyRetrievalExpression, 0, len(msgNames))

	for _, mn := range msgNames {
		expr := goexpr.Must(nil, data.MustItemDefinition(values.NewVariable("")),
			func(_ context.Context, _ data.Source) (data.Value, error) {
				return values.NewVariable(""), nil
			})

		re, err := bpmncommon.NewCorrelationPropertyRetrievalExpression(expr,
			bpmncommon.MustMessage(mn, data.MustItemDefinition(
				values.NewVariable(""), foundation.WithID(mn+"_in"))))
		require.NoError(t, err)

		res = append(res, *re)
	}

	prop, err := bpmncommon.NewCorrelationProperty("p", "string", res)
	require.NoError(t, err)

	key, err := bpmncommon.NewCorrelationKey("k",
		[]bpmncommon.CorrelationProperty{*prop})
	require.NoError(t, err)

	return key
}

// TestEventBasedGatewayParallelStartAndKey covers the instantiating-start accessors: a
// Parallel instantiating gate stores and returns its CorrelationKey and reports
// ParallelStart; a plain gate carries neither (SRD-025 §4).
func TestEventBasedGatewayParallelStartAndKey(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	key := minimalCorrKey(t)

	g, err := gateways.NewEventBasedGateway(
		gateways.WithInstantiate(),
		gateways.WithEventGatewayType(gateways.ParallelEvents),
		gateways.WithCorrelationKey(key))
	require.NoError(t, err)
	require.True(t, g.ParallelStart())
	require.Equal(t, gateways.ParallelEvents, g.EventGatewayType())
	require.Equal(t, key, g.CorrelationKey())

	plain, err := gateways.NewEventBasedGateway()
	require.NoError(t, err)
	require.False(t, plain.ParallelStart())
	require.Nil(t, plain.CorrelationKey())
}

// TestEventBasedGatewayArmForMessageByName covers defMatches' message-name branch: a
// Parallel-start gate resolves its firing arm from the starter's snapshot definition
// against the instance's cloned arms — different objects, same message name (SRD-025 §4.3).
func TestEventBasedGatewayArmForMessageByName(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	g, err := gateways.NewEventBasedGateway()
	require.NoError(t, err)

	a := msgArm(t, "ma", "msg-a")
	ebLink(t, g, a)

	// a message def with the same name but a fresh id (a clone) matches by name.
	arm, ok := g.ArmFor(msgDef(t, "msg-a"))
	require.True(t, ok)
	require.Equal(t, a.ID(), arm.ID())

	// a different message name still misses.
	_, ok = g.ArmFor(msgDef(t, "msg-z"))
	require.False(t, ok)
}

// msgArm builds a message IntermediateCatchEvent arm waiting for msgName.
func msgArm(t *testing.T, id, msgName string) *events.IntermediateCatchEvent {
	t.Helper()

	ice, err := events.NewIntermediateCatchEvent(id, msgDef(t, msgName))
	require.NoError(t, err)

	return ice
}

// msgDef builds a stand-alone message event definition for msgName (a fresh id).
func msgDef(t *testing.T, msgName string) flow.EventDefinition {
	t.Helper()

	def, err := events.NewMessageEventDefinition(
		bpmncommon.MustMessage(msgName, data.MustItemDefinition(
			values.NewVariable(""), foundation.WithID(msgName+"_in"))), nil)
	require.NoError(t, err)

	return def
}

func TestEventBasedGatewayValidateReceiveArmBoundary(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates()) // receiveArm builds an ItemDefinition

	g, err := gateways.NewEventBasedGateway()
	require.NoError(t, err)

	ebLink(t, g, signalArm(t, "a"))
	ebLink(t, g, newStubTaskBoundary(t, "b"))

	require.ErrorContains(t, g.Validate(), "boundary events")
}

// TestEventBasedGatewayArmForSignalByName covers the broadcast case: a signal is
// delivered as the THROWER's definition (a different object, same name), so ArmFor must
// resolve it to the arm by signal name, not by id (defMatches' signal branch).
func TestEventBasedGatewayArmForSignalByName(t *testing.T) {
	g, err := gateways.NewEventBasedGateway()
	require.NoError(t, err)

	a, b := signalArm(t, "a"), signalArm(t, "b")
	ebLink(t, g, a)
	ebLink(t, g, b)

	fired := signalDef(t, "a") // name "sig-a", a fresh id (the thrower's def)
	arm, ok := g.ArmFor(fired)
	require.True(t, ok)
	require.Equal(t, a.ID(), arm.ID())

	// a signal name with no arm still misses (name-branch mismatch).
	_, ok = g.ArmFor(signalDef(t, "zzz"))
	require.False(t, ok)

	// a non-signal def with no matching id misses via the final branch.
	msgDef, err := events.NewMessageEventDefinition(
		bpmncommon.MustMessage("nm", data.MustItemDefinition(
			values.NewVariable(""), foundation.WithID("nm_in"))), nil)
	require.NoError(t, err)

	_, ok = g.ArmFor(msgDef)
	require.False(t, ok)
}
