package instance

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"sync"
	"testing"

	"github.com/dr-dobermann/gobpm/internal/enginert"
	"github.com/dr-dobermann/gobpm/internal/eventproc"
	"github.com/dr-dobermann/gobpm/internal/instance/snapshot"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/stretchr/testify/require"
)

// testCorrKey builds a CorrelationKey whose single property extracts the message
// payload (bound under item id "order_in") as the key, for msgName messages.
func testCorrKey(t *testing.T, msgName string) *bpmncommon.CorrelationKey {
	t.Helper()

	mp := goexpr.Must(nil, data.MustItemDefinition(values.NewVariable("")),
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

	return key
}

// fakeEventProducer is an EventProducer that records AddEventKey calls (the
// extend-parked seam — SRD-017 §4.5). With failAdd set, AddEventKey errors, to
// exercise the extendReceivers debug-log branch.
type fakeEventProducer struct {
	added   map[string]string
	failAdd bool
}

func (fakeEventProducer) RegisterEvent(
	eventproc.EventProcessor, flow.EventDefinition,
) error {
	return nil
}

func (fakeEventProducer) UnregisterEvent(
	eventproc.EventProcessor, string,
) error {
	return nil
}

func (fakeEventProducer) PropagateEvent(
	context.Context, flow.EventDefinition,
) error {
	return nil
}

func (f *fakeEventProducer) AddEventKey(eDefID, key string) error {
	if f.failAdd {
		return fmt.Errorf("add-event-key boom")
	}

	f.added[eDefID] = key

	return nil
}

// failingCorrKey builds a CorrelationKey whose MessagePath errors on evaluation,
// to exercise the derive-error branch of validateAndAssociate.
func failingCorrKey(t *testing.T, msgName string) *bpmncommon.CorrelationKey {
	t.Helper()

	mp := goexpr.Must(nil, data.MustItemDefinition(values.NewVariable("")),
		func(context.Context, data.Source) (data.Value, error) {
			return nil, fmt.Errorf("derive boom")
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

	return key
}

// plainEventProducer is an EventProducer WITHOUT AddEventKey (the no-adder
// branch of extendReceivers).
type plainEventProducer struct{}

func (plainEventProducer) RegisterEvent(
	eventproc.EventProcessor, flow.EventDefinition,
) error {
	return nil
}

func (plainEventProducer) UnregisterEvent(
	eventproc.EventProcessor, string,
) error {
	return nil
}

func (plainEventProducer) PropagateEvent(
	context.Context, flow.EventDefinition,
) error {
	return nil
}

func msgEDef(t *testing.T, name, value string) *events.MessageEventDefinition {
	t.Helper()

	return events.MustMessageEventDefinition(
		bpmncommon.MustMessage(name, data.MustItemDefinition(
			values.NewVariable(value), foundation.WithID("order_in"))), nil)
}

// msgEDefID is msgEDef with a fixed definition id. A test seeds the loop's msgIdx with that
// id (via evWaiting.msgDefIDs) and then delivers a same-id message — mirroring how the real
// fire path clones the registered definition with its id preserved (CloneEventDefinition, SRD-027 FR-8),
// so a fired message resolves back to the parked track through the index.
func msgEDefID(t *testing.T, name, value, id string) *events.MessageEventDefinition {
	t.Helper()

	return events.MustMessageEventDefinition(
		bpmncommon.MustMessage(name, data.MustItemDefinition(
			values.NewVariable(value), foundation.WithID("order_in"))), nil,
		foundation.WithID(id))
}

// TestAssociateConversationKeySetIfAbsent verifies the set-if-absent semantics
// of the conversation key-set (SRD-017 FR-1): the first value for a key wins, a
// later value for a held key does not overwrite, and empty inputs are no-ops.
func TestAssociateConversationKeySetIfAbsent(t *testing.T) {
	inst := &Instance{convKeys: map[string]string{}}

	inst.AssociateConversationKey("orderKey", "ORD-1")
	if got := inst.convKeys["orderKey"]; got != "ORD-1" {
		t.Fatalf("first associate: got %q, want ORD-1", got)
	}

	// set-if-absent: a later value for a held key must not overwrite.
	inst.AssociateConversationKey("orderKey", "ORD-2")
	if got := inst.convKeys["orderKey"]; got != "ORD-1" {
		t.Fatalf("associate overwrote a held key: got %q, want ORD-1", got)
	}

	// empty name or value is a no-op.
	inst.AssociateConversationKey("", "X")
	inst.AssociateConversationKey("shipKey", "")
	if len(inst.convKeys) != 1 {
		t.Fatalf("empty associate must be a no-op: keys = %v", inst.convKeys)
	}

	// a distinct key is added.
	inst.AssociateConversationKey("shipKey", "SHP-9")
	if got := inst.convKeys["shipKey"]; got != "SHP-9" {
		t.Fatalf("second key: got %q, want SHP-9", got)
	}
}

// TestAssociateConversationKeyConcurrent exercises the convMu guard under
// concurrent association from many goroutines (forked tracks run concurrently).
func TestAssociateConversationKeyConcurrent(t *testing.T) {
	inst := &Instance{convKeys: map[string]string{}}

	var wg sync.WaitGroup
	for i := range 50 {
		wg.Add(1)

		go func(n int) {
			defer wg.Done()

			inst.AssociateConversationKey("k"+strconv.Itoa(n), "v")
		}(i)
	}

	wg.Wait()

	if len(inst.convKeys) != 50 {
		t.Fatalf("concurrent associate: %d keys, want 50", len(inst.convKeys))
	}
}

// TestConversationKeyValues verifies the snapshot accessor (empty -> nil; all
// values otherwise) and that the Instance exposes its values as the declared
// subscription filter (SRD-017 §4.3, SRD-027 FR-8).
func TestConversationKeyValues(t *testing.T) {
	inst := &Instance{convKeys: map[string]string{}}

	if vals := inst.conversationKeyValues(); vals != nil {
		t.Fatalf("empty instance: got %v, want nil", vals)
	}

	inst.AssociateConversationKey("orderKey", "ORD-1")
	inst.AssociateConversationKey("shipKey", "SHP-2")

	vals := inst.conversationKeyValues()
	sort.Strings(vals)

	if !slices.Equal(vals, []string{"ORD-1", "SHP-2"}) {
		t.Fatalf("values: got %v, want [ORD-1 SHP-2]", vals)
	}

	// the Instance exposes its values as the declared subscription filter — ownership
	// moved here from the track when the Instance became the message processor (FR-8).
	tk := inst.CorrelationKeys()
	sort.Strings(tk)

	if !slices.Equal(tk, []string{"ORD-1", "SHP-2"}) {
		t.Fatalf("instance keys: got %v, want [ORD-1 SHP-2]", tk)
	}
}

// TestDeriveAndAssociate verifies lazy association on delivery (SRD-017 §4.5):
// a declared key derived from the message is associated with the conversation
// and the instance's message receivers are extended with it.
func TestDeriveAndAssociate(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	catch, err := events.NewIntermediateCatchEvent("catch", msgEDef(t, "reply", ""))
	require.NoError(t, err)

	fep := &fakeEventProducer{added: map[string]string{}}
	inst := &Instance{
		EngineRuntime:       enginert.Default(),
		convKeys:            map[string]string{},
		parentEventProducer: fep,
		s: &snapshot.Snapshot{
			CorrelationKeys: []*bpmncommon.CorrelationKey{testCorrKey(t, "reply")},
			Nodes:           map[string]flow.Node{"catch": catch},
		},
	}

	inst.validateAndAssociate(context.Background(), msgEDef(t, "reply", "ORD-1"))

	if inst.convKeys["orderKey"] != "ORD-1" {
		t.Fatalf("derived key: got %q, want ORD-1", inst.convKeys["orderKey"])
	}

	// the parked message receiver was extended with the learned value.
	id := catch.Definitions()[0].ID()
	if fep.added[id] != "ORD-1" {
		t.Fatalf("receiver not extended: added=%v", fep.added)
	}
}

// TestDeriveAndAssociateNoOp covers the early returns: no declared keys, and a
// non-message event definition.
func TestDeriveAndAssociateNoOp(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	inst := &Instance{
		EngineRuntime: enginert.Default(),
		convKeys:      map[string]string{},
		s:             &snapshot.Snapshot{},
	}

	// no declared correlation keys -> no-op.
	inst.validateAndAssociate(context.Background(), msgEDef(t, "reply", "ORD-1"))
	require.Empty(t, inst.convKeys)

	// a non-message event definition -> no Message() -> no-op.
	inst.s = &snapshot.Snapshot{
		CorrelationKeys: []*bpmncommon.CorrelationKey{testCorrKey(t, "reply")},
	}
	inst.validateAndAssociate(context.Background(),
		events.MustSignalEventDefinition(&events.Signal{}))
	require.Empty(t, inst.convKeys)
}

// TestExtendReceiversNoAdder covers the branch where the parent event producer
// doesn't implement AddEventKey (extend is a no-op, no panic).
func TestExtendReceiversNoAdder(t *testing.T) {
	inst := &Instance{
		parentEventProducer: plainEventProducer{},
		s:                   &snapshot.Snapshot{Nodes: map[string]flow.Node{}},
	}

	inst.extendReceivers("ORD-1") // must not panic
}

// TestValidateAndAssociateMismatch verifies the §8.4.2 mismatch guard: a derived
// value that differs from a held key reports mismatch and associates nothing.
func TestValidateAndAssociateMismatch(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	fep := &fakeEventProducer{added: map[string]string{}}
	inst := &Instance{
		EngineRuntime:       enginert.Default(),
		convKeys:            map[string]string{"orderKey": "ORD-1"},
		parentEventProducer: fep,
		s: &snapshot.Snapshot{
			CorrelationKeys: []*bpmncommon.CorrelationKey{testCorrKey(t, "reply")},
			Nodes:           map[string]flow.Node{},
		},
	}

	// a message deriving orderKey=ORD-2 conflicts with the held ORD-1.
	if !inst.validateAndAssociate(context.Background(), msgEDef(t, "reply", "ORD-2")) {
		t.Fatal("expected mismatch=true for a conflicting key value")
	}

	if inst.convKeys["orderKey"] != "ORD-1" {
		t.Fatalf("held key must be unchanged on mismatch: %q", inst.convKeys["orderKey"])
	}
}

// TestValidateAndAssociateSameValue verifies a derived value equal to the held
// key is no mismatch and a benign no-op (the steady-state case).
func TestValidateAndAssociateSameValue(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	fep := &fakeEventProducer{added: map[string]string{}}
	inst := &Instance{
		EngineRuntime:       enginert.Default(),
		convKeys:            map[string]string{"orderKey": "ORD-1"},
		parentEventProducer: fep,
		s: &snapshot.Snapshot{
			CorrelationKeys: []*bpmncommon.CorrelationKey{testCorrKey(t, "reply")},
			Nodes:           map[string]flow.Node{},
		},
	}

	if inst.validateAndAssociate(context.Background(), msgEDef(t, "reply", "ORD-1")) {
		t.Fatal("same-value message must not be a mismatch")
	}

	if len(fep.added) != 0 {
		t.Fatalf("same-value message must not extend receivers: %v", fep.added)
	}
}

// TestValidateAndAssociateDeriveError covers the derive-error branch: a key whose
// MessagePath errors is logged and skipped (no mismatch, no association).
func TestValidateAndAssociateDeriveError(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	inst := &Instance{
		EngineRuntime:       enginert.Default(),
		convKeys:            map[string]string{},
		parentEventProducer: &fakeEventProducer{added: map[string]string{}},
		s: &snapshot.Snapshot{
			CorrelationKeys: []*bpmncommon.CorrelationKey{failingCorrKey(t, "reply")},
			Nodes:           map[string]flow.Node{},
		},
	}

	if inst.validateAndAssociate(context.Background(), msgEDef(t, "reply", "ORD-1")) {
		t.Fatal("a derivation error must not be reported as a mismatch")
	}

	require.Empty(t, inst.convKeys)
}

// TestValidateAndAssociateUnresolvedKey covers the not-derived skip: a key whose
// retrieval expression's MessageRef doesn't match the message yields ok=false.
func TestValidateAndAssociateUnresolvedKey(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	inst := &Instance{
		EngineRuntime:       enginert.Default(),
		convKeys:            map[string]string{},
		parentEventProducer: &fakeEventProducer{added: map[string]string{}},
		s: &snapshot.Snapshot{
			// the key derives from "other", not the received "reply" message.
			CorrelationKeys: []*bpmncommon.CorrelationKey{testCorrKey(t, "other")},
			Nodes:           map[string]flow.Node{},
		},
	}

	if inst.validateAndAssociate(context.Background(), msgEDef(t, "reply", "ORD-1")) {
		t.Fatal("an unresolved key must not be a mismatch")
	}

	require.Empty(t, inst.convKeys)
}

// TestExtendReceiversBranches covers the node-iteration branches: a non-event
// node is skipped, a non-message event definition is skipped, and an AddEventKey
// failure on a message receiver is logged (not fatal).
func TestExtendReceiversBranches(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	task, err := activities.NewServiceTask("task",
		service.MustOperation("op", nil, nil, nil), activities.WithoutParams())
	require.NoError(t, err)

	signalStart, err := events.NewStartEvent("sig",
		events.WithSignalTrigger(events.MustSignalEventDefinition(&events.Signal{})))
	require.NoError(t, err)

	msgCatch, err := events.NewIntermediateCatchEvent("catch", msgEDef(t, "reply", ""))
	require.NoError(t, err)

	inst := &Instance{
		EngineRuntime: enginert.Default(), // the debug-log branch needs Logger()
		// failAdd exercises the debug-log branch on the message receiver.
		parentEventProducer: &fakeEventProducer{
			added: map[string]string{}, failAdd: true,
		},
		s: &snapshot.Snapshot{Nodes: map[string]flow.Node{
			"task": task, "sig": signalStart, "catch": msgCatch,
		}},
	}

	inst.extendReceivers("ORD-1") // must not panic; covers all three branches
}

// The track's old synchronous mismatch path (ProcessEvent → ErrRejected) is gone:
// under ADR-017 the loop runs validateAndAssociate and drops a mismatch while the
// track stays parked. The gate function itself is covered by
// TestValidateAndAssociateMismatch; the loop-level drop is covered by
// TestLoopKeepsParkedOnCorrelationMismatch (inbound_delivery_test.go).
