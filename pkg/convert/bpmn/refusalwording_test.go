package bpmn

import (
	"strings"
	"testing"
)

// TestRefusalsSayWhichKindTheyAre sweeps the four things this stage
// refuses and checks the WORDING of each, because the wording is the
// whole deliverable: ADR-038 §2.5 says a standing boundary and a
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
		"plain association": {
			doc: laneDoc(`    <bpmn:textAnnotation id="note"><bpmn:text>x</bpmn:text></bpmn:textAnnotation>
    <bpmn:association id="a1" sourceRef="note" targetRef="t1"/>`),
			standing: false,
			wants:    []string{"#323", "artifacts.Association"},
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
