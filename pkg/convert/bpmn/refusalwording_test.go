package bpmn

import (
	"strings"
	"testing"
)

// TestRefusalsSayWhichKindTheyAre sweeps the four things this stage
// refuses and checks the WORDING of each, because the wording is the
// whole deliverable: ADR-024 §2.16 says a standing boundary and a
// capability-blocked one must not read alike, and a reader who cannot
// tell them apart either waits for something that is not coming or
// rebuilds something that is already correct.
//
// A per-refusal test proves each refuses. Only a sweep proves they still
// differ from each other, which is what erodes.
func TestRefusalsSayWhichKindTheyAre(t *testing.T) {
	tests := map[string]struct {
		doc string
		// standing: the answer will not change, so the message must offer
		// the route that exists instead and must NOT say "yet".
		standing bool
		// wants are phrases the refusal has to carry: the capability for a
		// blocked one, the alternative for a standing one.
		wants []string
	}{
		"ad-hoc sub-process": {
			doc: laneDoc(`    <bpmn:adHocSubProcess id="ah" name="Free"/>`),
			// ADR-035 §2.1: entered by a host-supplied Router.
			standing: true,
			wants:    []string{"Router", "programmatically"},
		},
		"transaction method=store": {
			doc:      variantDoc(`<bpmn:transaction id="sub" name="C" method="store">`),
			standing: true,
			wants:    []string{"ADR-028", "compensate"},
		},
		"foreign calledElement": {
			doc: callDoc(
				`<bpmn:callActivity id="ca" name="F" calledElement="other:Proc"/>`),
			standing: false,
			wants:    []string{"#325", "another definitions document"},
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

			if tc.standing && strings.Contains(msg, "#") {
				t.Errorf("a standing refusal cites an issue, which reads as "+
					"tracked work:\n%s", msg)
			}

			if !tc.standing && !strings.Contains(msg, "#") {
				t.Errorf("a capability-blocked refusal names no issue, so it "+
					"reads as a verdict on the file:\n%s", msg)
			}
		})
	}
}

// TestDataFamilyRefusalWordings replaces the T-24 staged sweep: after
// SRD-089.G nothing in the family is staged — a task's family imports,
// and every remaining refusal names either a capability row (#329,
// #330) or the position the standard reserves. Never "yet".
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
		"ioSpecification on a process": {
			doc:   propDoc("", `    <bpmn:ioSpecification id="io1"/>`),
			wants: []string{"#330", "ADR-011 §2.5"},
		},
		// A bare parameter or set outside an ioSpecification: on a task
		// the note points inside the spec; the same note carries the
		// event capability, since one settle path serves both owners.
		"dataInput": {
			doc:   onTask(`<bpmn:dataInput id="di1"/>`),
			wants: []string{"<ioSpecification>", "§10.4.1", "#329"},
		},
		"dataOutput": {
			doc:   onTask(`<bpmn:dataOutput id="do1"/>`),
			wants: []string{"<ioSpecification>", "#329"},
		},
		"inputSet": {
			doc:   onTask(`<bpmn:inputSet id="is1"/>`),
			wants: []string{"<ioSpecification>", "#329"},
		},
		"outputSet": {
			doc:   onTask(`<bpmn:outputSet id="os1"/>`),
			wants: []string{"<ioSpecification>", "#329"},
		},
		"dataInput on an event": {
			doc: propDoc("", `    <bpmn:endEvent id="ev2">
      <bpmn:dataInput id="di1"/>
    </bpmn:endEvent>`),
			wants: []string{"#329"},
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
		})
	}
}
