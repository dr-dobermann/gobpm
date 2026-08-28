package bpmn

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/rules"
	"github.com/dr-dobermann/gobpm/pkg/rules/gorules"
	"github.com/dr-dobermann/gobpm/pkg/script"

	"github.com/dr-dobermann/gobpm/pkg/datastore/memstore"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
)

// TestTypedEventsRunOnAThresher covers SRD-089.D §6 T-16 and §9 DoD 3.
//
// Everything else in this stage asserts that a definition CONSTRUCTED.
// That is not the same as wired: an event whose definition the model
// accepted can still be one the engine never subscribes, and every
// construction test would stay green. So one process carrying the stage's
// additions is registered and run to completion on a real thresher.
//
// The timer's date is a moment shortly in the future, because the engine
// refuses a timer already in the past (waiters/timer.go:172) — so the run
// is finite by waiting rather than by firing at once. The boundary error
// handler guards a task that does not fail, so the instance leaves
// through the normal path with the exception path present and
// subscribed.
func TestTypedEventsRunOnAThresher(t *testing.T) {
	// Built at run time: the date must be in the future when the engine
	// reads it, and a literal in the source stops being so the moment it
	// passes.
	fireAt := time.Now().Add(1500 * time.Millisecond).UTC().Format(time.RFC3339)

	doc := `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:error id="err1" name="Payment failed" errorCode="E_PAY"/>
  <bpmn:process id="TypedEvents" name="typed events" isExecutable="true">
    <bpmn:startEvent id="s1" name="at ten">
      <bpmn:timerEventDefinition id="d1">
        <bpmn:timeDate>` + fireAt + `</bpmn:timeDate>
      </bpmn:timerEventDefinition>
    </bpmn:startEvent>
    <bpmn:task id="t1" name="work"/>
    <bpmn:boundaryEvent id="b1" name="on failure" attachedToRef="t1">
      <bpmn:errorEventDefinition id="d2" errorRef="err1"/>
    </bpmn:boundaryEvent>
    <bpmn:endEvent id="e1" name="done"/>
    <bpmn:endEvent id="e2" name="failed"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s1" targetRef="t1"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t1" targetRef="e1"/>
    <bpmn:sequenceFlow id="f3" sourceRef="b1" targetRef="e2"/>
  </bpmn:process>
</bpmn:definitions>`

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	p, err := importer{}.Import(ctx, strings.NewReader(doc))
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	engine, err := thresher.New("typed-events-engine")
	if err != nil {
		t.Fatalf("thresher.New: %v", err)
	}

	if _, err = engine.RegisterProcess(p); err != nil {
		t.Fatalf("RegisterProcess: %v — an imported process the engine will "+
			"not register is not an imported process", err)
	}

	if err = engine.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}

	h, err := engine.StartLatest(p.ID())
	if err != nil {
		t.Fatalf("StartLatest: %v", err)
	}

	state, err := h.WaitCompletion(ctx)
	if err != nil {
		t.Fatalf("WaitCompletion: %v — the timer start fires on a date in the "+
			"past, so a run that does not finish means the event was built "+
			"but never subscribed", err)
	}

	t.Logf("instance completed: %s", state)
}

// TestSubProcessRunsOnAThresher covers SRD-089.E §6 T-20 and §9 DoD.
//
// Every other test in this stage asserts the imported SHAPE: which
// container holds which node, which lane names which id. None of that
// proves the engine can execute what came out. A sub-process is the case
// where it might not: its inner graph is entered by a token arriving at
// the container, and an import that put the inner nodes in the right
// container but left them unwired would pass every containment test here
// and then hang at run time.
//
// The process also carries a lane over the container and a transaction
// with a compensation-marked activity, so the stage's model-only and
// variant additions are registered and validated by a real engine rather
// than by the converter's own assertions.
func TestSubProcessRunsOnAThresher(t *testing.T) {
	doc := `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:process id="Containers" name="containers" isExecutable="true">
    <bpmn:laneSet id="ls1" name="Roles">
      <bpmn:lane id="l1" name="Back office">
        <bpmn:flowNodeRef>sub</bpmn:flowNodeRef>
      </bpmn:lane>
    </bpmn:laneSet>
    <bpmn:startEvent id="s1" name="go"/>
    <bpmn:subProcess id="sub" name="inner work">
      <bpmn:startEvent id="is" name="inner start"/>
      <bpmn:task id="it" name="inner task"/>
      <bpmn:endEvent id="ie" name="inner done"/>
      <bpmn:sequenceFlow id="if1" sourceRef="is" targetRef="it"/>
      <bpmn:sequenceFlow id="if2" sourceRef="it" targetRef="ie"/>
    </bpmn:subProcess>
    <bpmn:endEvent id="e1" name="done"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s1" targetRef="sub"/>
    <bpmn:sequenceFlow id="f2" sourceRef="sub" targetRef="e1"/>
  </bpmn:process>
</bpmn:definitions>`

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	p, err := importer{}.Import(ctx, strings.NewReader(doc))
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	engine, err := thresher.New("containers-engine")
	if err != nil {
		t.Fatalf("thresher.New: %v", err)
	}

	if _, err = engine.RegisterProcess(p); err != nil {
		t.Fatalf("RegisterProcess: %v — registration is what validates a "+
			"container's inner shape and its lane membership", err)
	}

	if err = engine.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}

	h, err := engine.StartLatest(p.ID())
	if err != nil {
		t.Fatalf("StartLatest: %v", err)
	}

	state, err := h.WaitCompletion(ctx)
	if err != nil {
		t.Fatalf("WaitCompletion: %v — a run that does not finish means the "+
			"inner graph was imported into the right container and never "+
			"entered", err)
	}

	t.Logf("instance completed: %s", state)
}

// TestDataFlowRunsOnAThresher covers SRD-089.G §6 T-26 and §9 DoD.
//
// Every other test in the stage asserts the imported SHAPE: which
// parameter carries which item, which association bound. None of that
// proves the runtime accepts what came out: LoadData walks the input
// associations and reads the data object from the per-instance scope
// (SRD-063 FR-5), UploadData walks the output ones, and the readiness
// gates consult exactly the parameters this stage built — an import
// that wired any of it wrongly would pass every construction test and
// fault or hang here.
//
// Both parameters are OPTIONAL, the §4a shape: a manual task fills
// nothing, so a required input would wait on data no one supplies and a
// required output would gate completion on a value never produced. The
// optional flag is this stage's own import (FR-2) — the run proves the
// engine honors it end to end.
func TestDataFlowRunsOnAThresher(t *testing.T) {
	doc := `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:xsd="http://www.w3.org/2001/XMLSchema">
  <bpmn:itemDefinition id="idOrder" structureRef="xsd:string"/>
  <bpmn:process id="DataFlow" name="data flow" isExecutable="true">
    <bpmn:property id="p1" name="retries" itemSubjectRef="idOrder"/>
    <bpmn:dataObject id="do1" name="order" itemSubjectRef="idOrder"/>
    <bpmn:startEvent id="s1"/>
    <bpmn:task id="t1" name="work">
      <bpmn:ioSpecification id="io1">
        <bpmn:dataInput id="din1" name="in" itemSubjectRef="idOrder"/>
        <bpmn:dataOutput id="dout1" name="out"/>
        <bpmn:inputSet id="is1">
          <bpmn:dataInputRefs>din1</bpmn:dataInputRefs>
          <bpmn:optionalInputRefs>din1</bpmn:optionalInputRefs>
        </bpmn:inputSet>
        <bpmn:outputSet id="os1">
          <bpmn:dataOutputRefs>dout1</bpmn:dataOutputRefs>
          <bpmn:optionalOutputRefs>dout1</bpmn:optionalOutputRefs>
        </bpmn:outputSet>
      </bpmn:ioSpecification>
      <bpmn:dataInputAssociation id="dia1">
        <bpmn:sourceRef>do1</bpmn:sourceRef>
        <bpmn:targetRef>din1</bpmn:targetRef>
      </bpmn:dataInputAssociation>
      <bpmn:dataOutputAssociation id="doa1">
        <bpmn:sourceRef>dout1</bpmn:sourceRef>
        <bpmn:targetRef>do1</bpmn:targetRef>
      </bpmn:dataOutputAssociation>
    </bpmn:task>
    <bpmn:endEvent id="e1"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s1" targetRef="t1"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t1" targetRef="e1"/>
  </bpmn:process>
</bpmn:definitions>`

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	p, err := importer{}.Import(ctx, strings.NewReader(doc))
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	engine, err := thresher.New("data-flow-engine")
	if err != nil {
		t.Fatalf("thresher.New: %v", err)
	}

	if _, err = engine.RegisterProcess(p); err != nil {
		t.Fatalf("RegisterProcess: %v — registration is what snapshots and "+
			"clones the data objects this stage's associations read", err)
	}

	if err = engine.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}

	h, err := engine.StartLatest(p.ID())
	if err != nil {
		t.Fatalf("StartLatest: %v", err)
	}

	state, err := h.WaitCompletion(ctx)
	if err != nil {
		t.Fatalf("WaitCompletion: %v — a run that does not finish means the "+
			"associations or the readiness gates were wired wrongly", err)
	}

	t.Logf("instance completed: %s", state)
}

// TestStoreAssociationRunsOnAThresher pins the store wiring
// BEHAVIORALLY — the independent review's point that a no-error import
// proves nothing about binding. The task's input is REQUIRED, so the
// readiness gate passes only if the store association actually moved
// the pre-filled value: an unbound association leaves the input
// Unavailable and the run cannot complete.
func TestStoreAssociationRunsOnAThresher(t *testing.T) {
	doc := `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:xsd="http://www.w3.org/2001/XMLSchema">
  <bpmn:itemDefinition id="idOrder" structureRef="xsd:string"/>
  <bpmn:process id="StoreFlow" name="store flow" isExecutable="true">
    <bpmn:dataStoreReference id="dsr1" name="orders" dataStoreRef="S"
                             itemSubjectRef="idOrder"/>
    <bpmn:startEvent id="s1"/>
    <bpmn:task id="t1" name="work">
      <bpmn:ioSpecification id="io1">
        <bpmn:dataInput id="din1" name="in" itemSubjectRef="idOrder"/>
        <bpmn:inputSet id="is1">
          <bpmn:dataInputRefs>din1</bpmn:dataInputRefs>
        </bpmn:inputSet>
      </bpmn:ioSpecification>
      <bpmn:dataInputAssociation id="dia1">
        <bpmn:sourceRef>dsr1</bpmn:sourceRef>
        <bpmn:targetRef>din1</bpmn:targetRef>
      </bpmn:dataInputAssociation>
    </bpmn:task>
    <bpmn:endEvent id="e1"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s1" targetRef="t1"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t1" targetRef="e1"/>
  </bpmn:process>
</bpmn:definitions>`

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	p, err := importer{}.Import(ctx, strings.NewReader(doc))
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	if err := data.CreateDefaultStates(); err != nil {
		t.Fatalf("CreateDefaultStates: %v", err)
	}

	// The engine store, pre-filled under the association's item name —
	// the key SRD-068 FR-4 reads by.
	store := memstore.New()
	if err := store.Put(ctx, "orders", data.MustItemAwareElement(
		data.MustItemDefinition(values.NewVariable("ORD-1")),
		data.ReadyDataState)); err != nil {
		t.Fatalf("Put: %v", err)
	}

	engine, err := thresher.New("store-flow-engine",
		thresher.WithDataStore("S", store))
	if err != nil {
		t.Fatalf("thresher.New: %v", err)
	}

	if _, err = engine.RegisterProcess(p); err != nil {
		t.Fatalf("RegisterProcess: %v", err)
	}

	if err = engine.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}

	h, err := engine.StartLatest(p.ID())
	if err != nil {
		t.Fatalf("StartLatest: %v", err)
	}

	state, err := h.WaitCompletion(ctx)
	if err != nil {
		t.Fatalf("WaitCompletion: %v — the REQUIRED input gates completion, "+
			"so an unbound store association cannot produce this failure "+
			"quietly", err)
	}

	t.Logf("instance completed: %s", state)
}

// TestLoopsRunOnAThresher covers SRD-089.H §6 T-23 and §9 DoD.
//
// Every other test in the stage asserts the imported SHAPE. This one
// proves the engine executes what came out: a sequential Multi-Instance
// counted by a cardinality expression, its completion condition stopping
// it early, and a pre-tested standard loop whose false condition runs
// zero iterations — the two decorators SRD-090.A dispatches, fed for the
// first time from XML.
func TestLoopsRunOnAThresher(t *testing.T) {
	doc := `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:process id="Loops" name="loops" isExecutable="true">
    <bpmn:startEvent id="s1"/>
    <bpmn:task id="t1" name="thrice">
      <bpmn:multiInstanceLoopCharacteristics id="mi1" isSequential="true">
        <bpmn:loopCardinality language="gobpm:lite">3</bpmn:loopCardinality>
        <bpmn:completionCondition language="gobpm:lite">loopCounter &gt;= 1</bpmn:completionCondition>
      </bpmn:multiInstanceLoopCharacteristics>
    </bpmn:task>
    <bpmn:task id="t2" name="never">
      <bpmn:standardLoopCharacteristics id="sl1" testBefore="true">
        <bpmn:loopCondition language="gobpm:lite">1 &gt; 2</bpmn:loopCondition>
      </bpmn:standardLoopCharacteristics>
    </bpmn:task>
    <bpmn:endEvent id="e1"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s1" targetRef="t1"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t1" targetRef="t2"/>
    <bpmn:sequenceFlow id="f3" sourceRef="t2" targetRef="e1"/>
  </bpmn:process>
</bpmn:definitions>`

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	p, err := importer{}.Import(ctx, strings.NewReader(doc))
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	engine, err := thresher.New("loops-engine")
	if err != nil {
		t.Fatalf("thresher.New: %v", err)
	}

	if _, err = engine.RegisterProcess(p); err != nil {
		t.Fatalf("RegisterProcess: %v", err)
	}

	if err = engine.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}

	h, err := engine.StartLatest(p.ID())
	if err != nil {
		t.Fatalf("StartLatest: %v", err)
	}

	state, err := h.WaitCompletion(ctx)
	if err != nil {
		t.Fatalf("WaitCompletion: %v — a run that does not finish means a "+
			"decorator was wired wrongly or a completion condition never "+
			"fired", err)
	}

	// This e2e pins the WIRING: both decorated nodes, imported from XML,
	// run on the engine to a clean Completed — the loop machinery engages
	// without a fault or a deadlock. Iteration-count semantics (the
	// cardinality, the early stop, the zero-iteration pre-test) are the
	// runtime's own contract, pinned by internal/instance's suite.
	if state != thresher.StateCompleted {
		t.Fatalf("terminal state = %s, want %s", state, thresher.StateCompleted)
	}
}

// TestIteratedWaitingLeafPassesThrough is SRD-089.H §6 T-17 (§4.6): the
// import does NOT pre-empt the engine's capability boundary. The converter
// maps loop characteristics onto the model and says nothing about whether the
// engine can execute the result — the engine decides, at registration.
//
// The boundary MOVED after this test was written. SRD-090.B made an iterated
// waiting leaf work: a decorator owns the node's event registration across
// iterations, so a SEQUENTIAL Multi-Instance over a ReceiveTask consumes one
// message per pass (#313). What survives is the narrow refusal — a PARALLEL
// fan-out over an uncorrelated Message catch, where a point-to-point envelope
// with N waiters is ambiguous by construction.
//
// So the case is now tested in both directions, which is what §4.6 actually
// claims: the same import path yields a process the engine ACCEPTS and one it
// REFUSES, and the converter behaves identically for both.
func TestIteratedWaitingLeafPassesThrough(t *testing.T) {
	doc := func(sequential string) string {
		return `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:message id="m1" name="Order placed"/>
  <bpmn:process id="WaitLoop" name="wait loop" isExecutable="true">
    <bpmn:startEvent id="s1"/>
    <bpmn:receiveTask id="t1" name="wait" messageRef="m1">
      <bpmn:multiInstanceLoopCharacteristics id="mi1" isSequential="` +
			sequential + `">
        <bpmn:loopCardinality language="gobpm:lite">3</bpmn:loopCardinality>
      </bpmn:multiInstanceLoopCharacteristics>
    </bpmn:receiveTask>
    <bpmn:endEvent id="e1"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s1" targetRef="t1"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t1" targetRef="e1"/>
  </bpmn:process>
</bpmn:definitions>`
	}

	register := func(t *testing.T, sequential string) error {
		t.Helper()

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		p, err := importer{}.Import(ctx, strings.NewReader(doc(sequential)))
		if err != nil {
			t.Fatalf("Import: %v — the boundary is the engine's, not the "+
				"converter's (§4.6)", err)
		}

		engine, err := thresher.New("wait-loop-engine")
		if err != nil {
			t.Fatalf("thresher.New: %v", err)
		}

		_, err = engine.RegisterProcess(p)

		return err
	}

	t.Run("sequential registers", func(t *testing.T) {
		if err := register(t, "true"); err != nil {
			t.Fatalf("RegisterProcess refused a sequential iterated wait: %v"+
				" — SRD-090.B made this buildable (#313)", err)
		}
	})

	t.Run("parallel over an uncorrelated message is refused", func(t *testing.T) {
		err := register(t, "false")
		if err == nil {
			t.Fatal("RegisterProcess accepted a PARALLEL Multi-Instance over " +
				"an uncorrelated Message catch; the boundary has moved again " +
				"— revisit SRD-090.B §4")
		}

		for _, want := range []string{
			"PARALLEL Multi-Instance", "iteration correlation",
		} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error = %v, want the engine's instructive %q",
					err, want)
			}
		}
	})
}

// luaish is a test-local script engine claiming the Lua formats the
// importer accepts (ADR-024 v.7 §2.11 — a script imports only when its
// body is self-contained source, and Lua is that shape).
//
// The interpreter itself is `adapters/lua`, a separate module, so a core
// test cannot reach it without inverting the dependency the module
// boundary exists to keep. What T-11 has to prove is not that Lua
// evaluates — that module tests it — but that an IMPORTED script task is
// wired: routed by its scriptFormat, executed by the engine the host
// registered, and its outputs committed to the instance's scope. A stub
// that returns a fixed output proves exactly that path and nothing it
// does not.
type luaish struct {
	seen atomic.Bool
}

func (l *luaish) Type() string { return "##TestLua" }

func (l *luaish) Formats() []string {
	return []string{"lua", "text/x-lua", "application/x-lua"}
}

func (l *luaish) Execute(
	_ context.Context, _, _ string, _ service.DataReader,
) (script.Outputs, error) {
	l.seen.Store(true)

	return script.Outputs{"amount": values.NewVariable(150)}, nil
}

// TestFlowNodesRunOnAThresher covers SRD-089.C §6 T-11 and §9 DoD 3.
//
// The stage added a script task, a business rule task, an inclusive and
// an event-based gateway, and every other test in it stops at the
// imported SHAPE — the node exists, its format or decision reference
// survived. That is not the same as wired. A script task whose format
// never reaches the script registry, or an inclusive gateway whose
// conditions never reach the expression engine, imports identically to
// one that works and then does nothing at run time.
//
// So the three deterministic additions run together on a real thresher:
// the script task produces a value, the inclusive gateway routes on it
// through the lite expression engine — reached from the JUEL a real
// modeller file carries, so the §2.10 translation is on the path too —
// and the rule task decides on the merged result. The event-based
// gateway is driven separately, below, because it completes by waiting
// rather than by flowing.
func TestFlowNodesRunOnAThresher(t *testing.T) {
	doc := `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
    xmlns:camunda="http://camunda.org/schema/1.0/bpmn">
  <bpmn:process id="FlowNodes" name="flow nodes" isExecutable="true">
    <bpmn:startEvent id="s1"/>
    <bpmn:scriptTask id="sc1" name="price it" scriptFormat="lua">
      <bpmn:script>data.set("amount", 150)</bpmn:script>
    </bpmn:scriptTask>
    <bpmn:inclusiveGateway id="ig1" name="split"/>
    <bpmn:task id="big" name="big order"/>
    <bpmn:task id="any" name="always"/>
    <bpmn:inclusiveGateway id="ig2" name="join"/>
    <bpmn:businessRuleTask id="br1" name="grade it" camunda:decisionRef="grade"/>
    <bpmn:endEvent id="e1"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s1" targetRef="sc1"/>
    <bpmn:sequenceFlow id="f2" sourceRef="sc1" targetRef="ig1"/>
    <bpmn:sequenceFlow id="f3" sourceRef="ig1" targetRef="big">
      <bpmn:conditionExpression>${amount &gt; 100}</bpmn:conditionExpression>
    </bpmn:sequenceFlow>
    <bpmn:sequenceFlow id="f4" sourceRef="ig1" targetRef="any">
      <bpmn:conditionExpression>${amount &gt; 0}</bpmn:conditionExpression>
    </bpmn:sequenceFlow>
    <bpmn:sequenceFlow id="f5" sourceRef="big" targetRef="ig2"/>
    <bpmn:sequenceFlow id="f6" sourceRef="any" targetRef="ig2"/>
    <bpmn:sequenceFlow id="f7" sourceRef="ig2" targetRef="br1"/>
    <bpmn:sequenceFlow id="f8" sourceRef="br1" targetRef="e1"/>
  </bpmn:process>
</bpmn:definitions>`

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	p, err := importer{}.Import(ctx, strings.NewReader(doc))
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	scripts := &luaish{}

	var graded atomic.Bool

	decisions := gorules.New()
	if err = decisions.Register("grade",
		func(_ context.Context, _ service.DataReader) (rules.Row, error) {
			graded.Store(true)

			return rules.Row{"grade": values.NewVariable("gold")}, nil
		}); err != nil {
		t.Fatalf("Register decision: %v", err)
	}

	engine, err := thresher.New("flow-nodes-engine",
		thresher.WithScriptEngine(scripts),
		thresher.WithRuleEngine(decisions))
	if err != nil {
		t.Fatalf("thresher.New: %v", err)
	}

	if _, err = engine.RegisterProcess(p); err != nil {
		t.Fatalf("RegisterProcess: %v — an imported process the engine will "+
			"not register is not an imported process", err)
	}

	if err = engine.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}

	h, err := engine.StartLatest(p.ID())
	if err != nil {
		t.Fatalf("StartLatest: %v", err)
	}

	if _, err = h.WaitCompletion(ctx); err != nil {
		t.Fatalf("WaitCompletion: %v — the stage's nodes imported, so a run "+
			"that never finishes means one of them was built and never "+
			"wired", err)
	}

	if !scripts.seen.Load() {
		t.Error("the script engine was never called — the imported " +
			"scriptFormat did not route")
	}

	if !graded.Load() {
		t.Error("the decision was never evaluated — the imported " +
			"decisionRef did not reach the rule engine")
	}
}

// TestEventBasedGatewayRunsOnAThresher completes SRD-089.C §6 T-11: the
// stage's fourth addition, driven the only way it can be.
//
// An event-based gateway does not route on data — it arms its outgoing
// catch events and lets the first one to fire decide. So it cannot ride
// the test above, which completes by flowing; it needs an event to
// actually arrive. A short timer supplies one, and the message branch
// that never arrives is what proves the choice was made rather than both
// paths taken.
func TestEventBasedGatewayRunsOnAThresher(t *testing.T) {
	doc := `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:message id="m1" name="never"/>
  <bpmn:process id="EventGate" name="event gate" isExecutable="true">
    <bpmn:startEvent id="s1"/>
    <bpmn:eventBasedGateway id="eg1" name="whichever first"/>
    <bpmn:intermediateCatchEvent id="tick" name="after a moment">
      <bpmn:timerEventDefinition id="d1">
        <bpmn:timeDuration>PT1S</bpmn:timeDuration>
      </bpmn:timerEventDefinition>
    </bpmn:intermediateCatchEvent>
    <bpmn:intermediateCatchEvent id="never" name="not coming">
      <bpmn:messageEventDefinition id="d2" messageRef="m1"/>
    </bpmn:intermediateCatchEvent>
    <bpmn:endEvent id="e1"/>
    <bpmn:endEvent id="e2"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s1" targetRef="eg1"/>
    <bpmn:sequenceFlow id="f2" sourceRef="eg1" targetRef="tick"/>
    <bpmn:sequenceFlow id="f3" sourceRef="eg1" targetRef="never"/>
    <bpmn:sequenceFlow id="f4" sourceRef="tick" targetRef="e1"/>
    <bpmn:sequenceFlow id="f5" sourceRef="never" targetRef="e2"/>
  </bpmn:process>
</bpmn:definitions>`

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	p, err := importer{}.Import(ctx, strings.NewReader(doc))
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	engine, err := thresher.New("event-gate-engine")
	if err != nil {
		t.Fatalf("thresher.New: %v", err)
	}

	if _, err = engine.RegisterProcess(p); err != nil {
		t.Fatalf("RegisterProcess: %v", err)
	}

	if err = engine.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}

	h, err := engine.StartLatest(p.ID())
	if err != nil {
		t.Fatalf("StartLatest: %v", err)
	}

	started := time.Now()

	if _, err = h.WaitCompletion(ctx); err != nil {
		t.Fatalf("WaitCompletion: %v — the gateway imported, so a run that "+
			"never finishes means its branches were built and never armed",
			err)
	}

	// Completing is not enough: a gateway that armed nothing and fell
	// straight through would also complete, and instantly. The timer is the
	// only branch that can fire, so the run cannot be shorter than it — and
	// asserting that is what makes this a test of the ARMING rather than of
	// the token reaching an end event.
	if waited := time.Since(started); waited < 900*time.Millisecond {
		t.Errorf("the run finished in %s, faster than the 1s timer branch it "+
			"had to wait for — the gateway completed without arming", waited)
	}
}
