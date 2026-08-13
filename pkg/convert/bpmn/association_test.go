package bpmn

import (
	"strings"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/events"
)

// compensationDoc is a task guarded by a compensation boundary, the
// handler that undoes it, and the association naming one from the other.
// handlerAttrs is spliced onto the handler so a test can take
// isForCompensation away.
func compensationDoc(handlerAttrs, assoc string) string {
	return `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:process id="P" name="P">
    <bpmn:startEvent id="s1"/>
    <bpmn:task id="charge" name="Charge"/>
    <bpmn:endEvent id="e1"/>
    <bpmn:task id="refund" name="Refund"` + handlerAttrs + `/>
    <bpmn:boundaryEvent id="cb" attachedToRef="charge">
      <bpmn:compensateEventDefinition id="ced"/>
    </bpmn:boundaryEvent>
` + assoc + `
    <bpmn:sequenceFlow id="f1" sourceRef="s1" targetRef="charge"/>
    <bpmn:sequenceFlow id="f2" sourceRef="charge" targetRef="e1"/>
  </bpmn:process>
</bpmn:definitions>`
}

// handlerLink is the association BPMN draws from the boundary event to
// the activity that compensates.
const handlerLink = `    <bpmn:association id="a1" sourceRef="cb" targetRef="refund"/>`

// TestCompensationBoundaryTakesItsHandler is FR-6, and it closes the
// refusal SRD-089.D §4.11 wrote against exactly this element.
func TestCompensationBoundaryTakesItsHandler(t *testing.T) {
	res, err := importEventDoc(t,
		compensationDoc(` isForCompensation="true"`, handlerLink))
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	n := nodeByID(t, res, "cb")

	b, ok := n.(*events.BoundaryEvent)
	if !ok {
		t.Fatalf("cb is a %T, want a *events.BoundaryEvent", n)
	}

	h := b.CompensationHandler()
	if h == nil {
		t.Fatal("CompensationHandler() = nil — the association named one")
	}

	if h.ID() != "refund" {
		t.Errorf("CompensationHandler() = %q, want the associated activity",
			h.ID())
	}
}

// TestCompensationHandlerMustBeMarked leaves the eligibility rule with
// the model, whose message names the option the file is missing.
func TestCompensationHandlerMustBeMarked(t *testing.T) {
	_, err := importEventDoc(t, compensationDoc("", handlerLink))
	if err == nil {
		t.Fatal("an unmarked handler must be refused")
	}

	if !strings.Contains(err.Error(), "isForCompensation") {
		t.Errorf("error = %v, want the model's own eligibility message", err)
	}
}

// TestCompensationBoundaryWithoutAnAssociation refuses the shape that has
// nothing to run: the trigger fires and BPMN names the handler through a
// link this file did not draw.
func TestCompensationBoundaryWithoutAnAssociation(t *testing.T) {
	_, err := importEventDoc(t, compensationDoc(` isForCompensation="true"`, ""))
	if err == nil {
		t.Fatal("a compensation boundary with no association must be refused")
	}

	if strings.Contains(err.Error(), "does not import yet") {
		t.Error("the refusal still says associations are unimported — they are")
	}
}

// TestAssociationDirectionIsNotReversed pins the reading direction. BPMN
// draws the link from the event to the handler; reading it both ways
// would make a diagram's meaning depend on which end the modeler dragged
// first.
func TestAssociationDirectionIsNotReversed(t *testing.T) {
	_, err := importEventDoc(t, compensationDoc(` isForCompensation="true"`,
		`    <bpmn:association id="a1" sourceRef="refund" targetRef="cb"/>`))
	if err == nil {
		t.Fatal("a reversed association must not be read as a handler link")
	}
}

// TestAssociationToANonActivity refuses a handler that cannot be one.
func TestAssociationToANonActivity(t *testing.T) {
	_, err := importEventDoc(t, compensationDoc(` isForCompensation="true"`,
		`    <bpmn:association id="a1" sourceRef="cb" targetRef="e1"/>`))
	if err == nil {
		t.Fatal("an end event cannot be a compensation handler")
	}

	if !strings.Contains(err.Error(), "rather than an activity") {
		t.Errorf("error = %v, want it to say what the target is", err)
	}
}

// TestAssociationToNothing refuses a dangling targetRef by naming the id,
// the same way a dangling sequence flow is refused.
func TestAssociationToNothing(t *testing.T) {
	_, err := importEventDoc(t, compensationDoc(` isForCompensation="true"`,
		`    <bpmn:association id="a1" sourceRef="cb" targetRef="ghost"/>`))
	if err == nil {
		t.Fatal("an association to no element must be refused")
	}

	if !strings.Contains(err.Error(), "ghost") {
		t.Errorf("error = %v, want it to name the missing id", err)
	}
}

// TestPlainAssociationIsRefused is FR-7: the annotation is imported (as a
// skip), the line is not, and the refusal names the capability rather
// than a schedule.
func TestPlainAssociationIsRefused(t *testing.T) {
	doc := `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:process id="P" name="P">
    <bpmn:startEvent id="s1"/>
    <bpmn:task id="t1" name="Work"/>
    <bpmn:endEvent id="e1"/>
    <bpmn:textAnnotation id="note"><bpmn:text>Careful</bpmn:text></bpmn:textAnnotation>
    <bpmn:association id="a1" sourceRef="note" targetRef="t1"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s1" targetRef="t1"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t1" targetRef="e1"/>
  </bpmn:process>
</bpmn:definitions>`

	_, err := importEventDoc(t, doc)
	if err == nil {
		t.Fatal("a plain association must be refused, not dropped")
	}

	for _, want := range []string{"#323", "artifacts.Association"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v, want it to name %q", err, want)
		}
	}

	if strings.Contains(err.Error(), "yet") {
		t.Error("the refusal says \"yet\" — this waits on a model capability, " +
			"and saying \"yet\" invites waiting for a converter that is done")
	}
}

// TestAssociationNeedsBothEnds refuses a half-drawn link rather than
// treating a missing end as an absent association.
func TestAssociationNeedsBothEnds(t *testing.T) {
	_, err := importEventDoc(t, compensationDoc(` isForCompensation="true"`,
		`    <bpmn:association id="a1" sourceRef="cb"/>`))
	if err == nil {
		t.Fatal("an association missing an end must be refused")
	}

	if !strings.Contains(err.Error(), "sourceRef and targetRef") {
		t.Errorf("error = %v, want the both-ends refusal", err)
	}
}
