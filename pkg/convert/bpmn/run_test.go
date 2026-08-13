package bpmn

import (
	"context"
	"strings"
	"testing"
	"time"

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
