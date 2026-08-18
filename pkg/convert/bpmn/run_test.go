package bpmn

import (
	"context"
	"strings"
	"testing"
	"time"

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

	t.Logf("instance completed: %s", state)
}

// TestIteratedWaitingLeafPassesThrough is SRD-089.H §6 T-17 (§4.6): the
// import does NOT pre-empt the engine's capability boundary — the file
// imports cleanly, and registration refuses with the engine's own
// instructive message naming #313 and the remodeling.
func TestIteratedWaitingLeafPassesThrough(t *testing.T) {
	doc := `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:message id="m1" name="Order placed"/>
  <bpmn:process id="WaitLoop" name="wait loop" isExecutable="true">
    <bpmn:startEvent id="s1"/>
    <bpmn:receiveTask id="t1" name="wait" messageRef="m1">
      <bpmn:multiInstanceLoopCharacteristics id="mi1" isSequential="true">
        <bpmn:loopCardinality language="gobpm:lite">3</bpmn:loopCardinality>
      </bpmn:multiInstanceLoopCharacteristics>
    </bpmn:receiveTask>
    <bpmn:endEvent id="e1"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s1" targetRef="t1"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t1" targetRef="e1"/>
  </bpmn:process>
</bpmn:definitions>`

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	p, err := importer{}.Import(ctx, strings.NewReader(doc))
	if err != nil {
		t.Fatalf("Import: %v — the boundary is the engine's, not the "+
			"converter's (§4.6)", err)
	}

	engine, err := thresher.New("wait-loop-engine")
	if err != nil {
		t.Fatalf("thresher.New: %v", err)
	}

	_, err = engine.RegisterProcess(p)
	if err == nil {
		t.Fatal("RegisterProcess accepted an iterated waiting leaf; the " +
			"#313 boundary has moved — revisit SRD-089.H §4.6")
	}

	for _, want := range []string{"iterates and waits", "#313", "Sub-Process"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v, want the engine's instructive %q", err, want)
		}
	}
}
