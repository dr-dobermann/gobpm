package bpmn

import (
	"strings"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/artifacts"
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

	if !strings.Contains(err.Error(), "cb") {
		t.Errorf("error = %v, want the boundary event named — a reversed "+
			"link must fail as a MISSING handler link, not something else", err)
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

// TestPlainAssociationImports is SRD-092 T-6 — the case that closed #323.
// The annotation and the line drawn to it both import, and nothing is
// dropped: the exact file the converter refused with an
// UnsupportedElementError before ADR-039 landed the artifact tier.
func TestPlainAssociationImports(t *testing.T) {
	doc := `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:process id="P" name="P">
    <bpmn:startEvent id="s1"/>
    <bpmn:task id="t1" name="Work"/>
    <bpmn:endEvent id="e1"/>
    <bpmn:textAnnotation id="note">
      <bpmn:text>Careful</bpmn:text>
    </bpmn:textAnnotation>
    <bpmn:association id="a1" sourceRef="note" targetRef="t1"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s1" targetRef="t1"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t1" targetRef="e1"/>
  </bpmn:process>
</bpmn:definitions>`

	res, err := importEventDoc(t, doc)
	if err != nil {
		t.Fatalf("the annotated file must import: %v", err)
	}

	if len(res.Dropped) != 0 {
		t.Errorf("Dropped = %+v, want nothing lost", res.Dropped)
	}

	arts := res.Processes[0].Artifacts()
	if len(arts) != 2 {
		t.Fatalf("artifacts = %d, want the annotation and the association",
			len(arts))
	}

	a, ok := arts[1].(*artifacts.Association)
	if !ok {
		t.Fatalf("artifact 1 is a %T, want *Association", arts[1])
	}

	if a.ID() != "a1" || a.Source().ID() != "note" ||
		a.Target().ID() != "t1" || a.Direction() != artifacts.None {
		t.Errorf("association = %q %q→%q dir %q, want a1 note→t1 None "+
			"(the absent attribute takes the standard's default)",
			a.ID(), a.Source().ID(), a.Target().ID(), a.Direction())
	}
}

// TestAssociationEnds covers the resolution universe (SRD-092 T-9 and
// FR-9): an end may be a sequence flow, a data object, or another carried
// artifact — anything the import built.
func TestAssociationEnds(t *testing.T) {
	doc := `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:process id="P" name="P">
    <bpmn:startEvent id="s1"/>
    <bpmn:task id="t1" name="Work"/>
    <bpmn:endEvent id="e1"/>
    <bpmn:dataObject id="do1" name="order"/>
    <bpmn:textAnnotation id="note">
      <bpmn:text>x</bpmn:text>
    </bpmn:textAnnotation>
    <bpmn:group id="g1"/>
    <bpmn:association id="af" sourceRef="note" targetRef="f1"
                      associationDirection="One"/>
    <bpmn:association id="ad" sourceRef="note" targetRef="do1"
                      associationDirection="Both"/>
    <bpmn:association id="ag" sourceRef="note" targetRef="g1"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s1" targetRef="t1"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t1" targetRef="e1"/>
  </bpmn:process>
</bpmn:definitions>`

	res, err := importEventDoc(t, doc)
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	byID := map[string]*artifacts.Association{}

	for _, art := range res.Processes[0].Artifacts() {
		if a, ok := art.(*artifacts.Association); ok {
			byID[a.ID()] = a
		}
	}

	for id, want := range map[string]struct {
		target string
		dir    artifacts.AssociationDirection
	}{
		"af": {"f1", artifacts.One},
		"ad": {"do1", artifacts.Both},
		"ag": {"g1", artifacts.None},
	} {
		a, ok := byID[id]
		if !ok {
			t.Errorf("association %q was not carried", id)

			continue
		}

		if a.Target().ID() != want.target || a.Direction() != want.dir {
			t.Errorf("association %q = →%q dir %q, want →%q %q",
				id, a.Target().ID(), a.Direction(), want.target, want.dir)
		}
	}
}

// TestAssociationInsideSubProcess is the association half of SRD-092 T-7:
// a link declared inside a container lands on that container.
func TestAssociationInsideSubProcess(t *testing.T) {
	res, err := importEventDoc(t, subProcessDoc(innerGraph+`
      <bpmn:textAnnotation id="in-note">
        <bpmn:text>inner</bpmn:text>
      </bpmn:textAnnotation>
      <bpmn:association id="in-a" sourceRef="in-note" targetRef="it"/>`))
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	sp := containerOf(t, nodeByID(t, res, "sub"))

	arts := sp.Artifacts()
	if len(arts) != 2 || arts[1].ID() != "in-a" {
		t.Fatalf("sub-process artifacts = %v, want the annotation and in-a",
			arts)
	}

	if got := len(res.Processes[0].Artifacts()); got != 0 {
		t.Errorf("process artifacts = %d, want 0", got)
	}
}

// TestAssociationDanglingEndIsReported is SRD-092 T-10: an end naming
// nothing this import built drops THAT association with a report — the
// file survives.
func TestAssociationDanglingEndIsReported(t *testing.T) {
	doc := `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:process id="P" name="P">
    <bpmn:startEvent id="s1"/>
    <bpmn:endEvent id="e1"/>
    <bpmn:association id="a1" sourceRef="s1" targetRef="missing"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s1" targetRef="e1"/>
  </bpmn:process>
</bpmn:definitions>`

	res, err := importEventDoc(t, doc)
	if err != nil {
		t.Fatalf("a dangling association end must not refuse the file: %v",
			err)
	}

	if got := len(res.Processes[0].Artifacts()); got != 0 {
		t.Errorf("artifacts = %d, want 0", got)
	}

	if len(res.Dropped) != 1 {
		t.Fatalf("Dropped = %+v, want exactly the association's entry",
			res.Dropped)
	}

	d := res.Dropped[0]
	if d.Element != "a1" || d.Construct != tagAssociation ||
		!strings.Contains(d.Reason, `"missing"`) {
		t.Errorf("report = %+v, want it naming a1, association and the "+
			"unresolved ref", d)
	}

	// The same degradation from the other end: a dangling sourceRef.
	doc = strings.Replace(doc,
		`sourceRef="s1" targetRef="missing"`,
		`sourceRef="missing" targetRef="s1"`, 1)

	res, err = importEventDoc(t, doc)
	if err != nil {
		t.Fatalf("a dangling sourceRef must not refuse the file: %v", err)
	}

	if len(res.Dropped) != 1 ||
		!strings.Contains(res.Dropped[0].Reason, "sourceRef") {
		t.Errorf("Dropped = %+v, want the sourceRef report", res.Dropped)
	}
}

// TestCompensationAssociationNotDuplicated is SRD-092 T-11: the consumed
// association becomes the handler wiring and nothing else — one document
// fact, one model representation (ADR-039 §2.4).
func TestCompensationAssociationNotDuplicated(t *testing.T) {
	res, err := importEventDoc(t,
		compensationDoc(` isForCompensation="true"`, handlerLink))
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	if got := len(res.Processes[0].Artifacts()); got != 0 {
		t.Errorf("artifacts = %d, want 0 — the compensation link is the "+
			"boundary's wiring, not an artifact", got)
	}
}

// TestAssociationInvalidDirectionIsRefused: associationDirection is a
// closed enumeration (§8.4.1), and a value outside it is a broken file,
// not a droppable datum.
func TestAssociationInvalidDirectionIsRefused(t *testing.T) {
	doc := `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:process id="P" name="P">
    <bpmn:startEvent id="s1"/>
    <bpmn:endEvent id="e1"/>
    <bpmn:association id="a1" sourceRef="s1" targetRef="e1"
                      associationDirection="Sideways"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s1" targetRef="e1"/>
  </bpmn:process>
</bpmn:definitions>`

	_, err := importEventDoc(t, doc)
	if err == nil {
		t.Fatal("an out-of-enumeration direction must refuse the document")
	}

	if !strings.Contains(err.Error(), "Sideways") {
		t.Errorf("err = %v, want it naming the invalid value", err)
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
