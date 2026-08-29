package bpmn

import (
	"strings"
	"testing"
)

// refusalKind is what a refusal is telling its reader to do.
type refusalKind int

const (
	// kindStanding: the answer will not change. The message offers the route
	// that exists instead, and cites no issue — a number would read as
	// tracked work on something nobody intends to build.
	kindStanding refusalKind = iota
	// kindBlocked: legal BPMN the engine could execute, waiting on a model
	// capability. The message names the capability AND its issue, because
	// that number is the specification of the work that removes it.
	kindBlocked
	// kindMalformed: the FILE is wrong, not the engine. The message says
	// what to fix in the document and cites no issue — nothing is waiting on
	// anyone. This kind arrived with SRD-096: reading calledElement as a
	// QName made "the prefix is not declared" expressible for the first
	// time.
	kindMalformed
)

// TestRefusalsSayWhichKindTheyAre sweeps one refusal of each kind and checks
// the WORDING, because the wording is the whole deliverable: ADR-024 §2.16
// says the kinds must not read alike, and a reader who cannot tell them apart
// either waits for something that is not coming, rebuilds something that is
// already correct, or hunts for an engine bug in their own file.
//
// A per-refusal test proves each refuses. Only a sweep proves they still
// differ FROM EACH OTHER, which is what erodes — and it erodes exactly when a
// refusal is retired and its row is deleted rather than replaced, leaving the
// sweep with nothing to contrast.
func TestRefusalsSayWhichKindTheyAre(t *testing.T) {
	tests := map[string]struct {
		doc  string
		kind refusalKind
		// wants are phrases the refusal has to carry: the capability for a
		// blocked one, the alternative for a standing one, the repair for a
		// malformed file.
		wants []string
	}{
		"ad-hoc sub-process": {
			doc: laneDoc(`    <bpmn:adHocSubProcess id="ah" name="Free"/>`),
			// ADR-035 §2.1: entered by a host-supplied Router.
			kind:  kindStanding,
			wants: []string{"Router", "programmatically"},
		},
		// A transaction's method=store is no longer an import refusal: the
		// model carries any method and registration refuses one no
		// coordinator performs (SRD-095 FR-4/FR-6; pinned by
		// pkg/thresher TestValidateTransactionCoverage).
		//
		// Nor is a foreign calledElement: SRD-096 replaced #325's refusal
		// with the QName dispositions, so the blocked row moved to a
		// capability that is still blocked.
		"property as a data-association end": {
			doc: propDoc(
				`  <bpmn:itemDefinition id="idOrder" structureRef="xsd:string"/>`,
				`    <bpmn:property id="p1" name="note" itemSubjectRef="idOrder"/>
    <bpmn:task id="t1" name="Work">
      <bpmn:ioSpecification id="io1">
        <bpmn:dataInput id="din1" name="in" itemSubjectRef="idOrder"/>
      </bpmn:ioSpecification>
      <bpmn:dataInputAssociation id="dia1">
        <bpmn:sourceRef>p1</bpmn:sourceRef>
        <bpmn:targetRef>din1</bpmn:targetRef>
      </bpmn:dataInputAssociation>
    </bpmn:task>
    <bpmn:sequenceFlow id="f2" sourceRef="s1" targetRef="t1"/>
    <bpmn:sequenceFlow id="f3" sourceRef="t1" targetRef="e1"/>`),
			kind:  kindBlocked,
			wants: []string{"#331", "capability"},
		},
		"calledElement qualified by an undeclared prefix": {
			doc: callDoc(
				`<bpmn:callActivity id="ca" name="F" calledElement="ghost:Proc"/>`),
			kind:  kindMalformed,
			wants: []string{"no xmlns declaration binds", "Declare the prefix"},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := importEventDoc(t, tc.doc)
			if err == nil {
				t.Fatal("want a refusal, got a clean import")
			}

			msg := err.Error()

			for _, want := range tc.wants {
				if !strings.Contains(msg, want) {
					t.Errorf("refusal does not mention %q:\n%s", want, msg)
				}
			}

			// "yet" is the word that turns a permanent property of the
			// engine into a promise. A capability-blocked refusal may not
			// use it either — the wait is on a model change nobody has
			// scheduled, and the issue number is the honest form of that.
			if strings.Contains(msg, " yet") {
				t.Errorf("refusal says \"yet\", which reads as a schedule:\n%s",
					msg)
			}

			cites := strings.Contains(msg, "#")

			switch tc.kind {
			case kindStanding:
				if cites {
					t.Errorf("a standing refusal cites an issue, which reads "+
						"as tracked work on something nobody intends to "+
						"build:\n%s", msg)
				}

			case kindBlocked:
				if !cites {
					t.Errorf("a capability-blocked refusal names no issue, "+
						"so it reads as a verdict on the file rather than as "+
						"work with a name:\n%s", msg)
				}

			case kindMalformed:
				if cites {
					t.Errorf("a malformed-file refusal cites an issue, which "+
						"sends the reader to the tracker for a problem in "+
						"their own document:\n%s", msg)
				}
			}
		})
	}
}

// TestDataFamilyRefusalWordings replaces the T-24 staged sweep: after
// SRD-089.G nothing in the family is staged — a task's family imports,
// an event's data imports since SRD-094, and every remaining refusal
// names the position the standard reserves. Never "yet", and nothing
// names #329 any more.
func TestDataFamilyRefusalWordings(t *testing.T) {
	onTask := func(child string) string {
		return propDoc("", `    <bpmn:task id="t1" name="T">
      `+child+`
    </bpmn:task>`)
	}

	tests := map[string]struct {
		doc   string
		wants []string
	}{
		// A bare parameter or set outside an ioSpecification on a task:
		// the note points inside the spec.
		"dataInput": {
			doc:   onTask(`<bpmn:dataInput id="di1"/>`),
			wants: []string{"<ioSpecification>", "§10.4.1"},
		},
		"dataOutput": {
			doc:   onTask(`<bpmn:dataOutput id="do1"/>`),
			wants: []string{"<ioSpecification>"},
		},
		"inputSet": {
			doc:   onTask(`<bpmn:inputSet id="is1"/>`),
			wants: []string{"<ioSpecification>"},
		},
		"outputSet": {
			doc:   onTask(`<bpmn:outputSet id="os1"/>`),
			wants: []string{"<ioSpecification>"},
		},
		"dataOutput on a gateway": {
			doc: propDoc("", `    <bpmn:exclusiveGateway id="g2">
      <bpmn:dataOutput id="do1"/>
    </bpmn:exclusiveGateway>`),
			wants: []string{"§10.4.1", "§10.4.2"},
		},
		"association under the process": {
			doc:   propDoc("", `    <bpmn:dataInputAssociation id="dia1"/>`),
			wants: []string{"lives on the activity"},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := importEventDoc(t, tc.doc)
			if err == nil {
				t.Fatal("want a refusal, got a clean import")
			}

			msg := err.Error()

			for _, want := range tc.wants {
				if !strings.Contains(msg, want) {
					t.Errorf("refusal does not mention %q:\n%s", want, msg)
				}
			}

			if strings.Contains(msg, " yet") {
				t.Errorf("refusal says \"yet\", which reads as a schedule:\n%s",
					msg)
			}

			if strings.Contains(msg, "SRD-089.G") {
				t.Errorf("refusal still names the landed stage as a plan:\n%s",
					msg)
			}

			if strings.Contains(msg, "#329") {
				t.Errorf("refusal names #329 — the capability landed:\n%s", msg)
			}
		})
	}
}
