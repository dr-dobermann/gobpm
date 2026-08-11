package bpmn

import (
	"strings"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/flow"
)

// boundaryDoc puts a task with a boundary event in a linear process. The
// boundary's own outgoing flow leads to a second end event, which is the
// exception path a boundary exists to open.
func boundaryDoc(attrs, def string) string {
	return `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">` +
		catalogRoots + `
  <bpmn:process id="P" name="P">
    <bpmn:startEvent id="s1"/>
    <bpmn:task id="t1" name="work"/>
    <bpmn:boundaryEvent id="b1" name="guard"` + attrs + `>` + def + `</bpmn:boundaryEvent>
    <bpmn:endEvent id="e1"/>
    <bpmn:endEvent id="e2"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s1" targetRef="t1"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t1" targetRef="e1"/>
    <bpmn:sequenceFlow id="f3" sourceRef="b1" targetRef="e2"/>
  </bpmn:process>
</bpmn:definitions>`
}

// TestBoundaryEventImports covers SRD-089.D §6 T-9 (FR-5): the event
// attaches to the activity attachedToRef names, and cancelActivity
// defaults to the standard's true (elements/events.md:252).
func TestBoundaryEventImports(t *testing.T) {
	for name, tc := range map[string]struct {
		attrs, def string
		want       flow.EventTrigger
		interrupts bool
	}{
		"error, interrupting by default": {
			attrs:      ` attachedToRef="t1"`,
			def:        `<bpmn:errorEventDefinition id="d1" errorRef="err1"/>`,
			want:       flow.TriggerError,
			interrupts: true,
		},
		"timer, interrupting by default": {
			attrs: ` attachedToRef="t1"`,
			def: `<bpmn:timerEventDefinition id="d1">` +
				`<bpmn:timeDate>2026-08-11T10:00:00Z</bpmn:timeDate>` +
				`</bpmn:timerEventDefinition>`,
			want:       flow.TriggerTimer,
			interrupts: true,
		},
		"non-interrupting message": {
			attrs:      ` attachedToRef="t1" cancelActivity="false"`,
			def:        `<bpmn:messageEventDefinition id="d1" messageRef="m1"/>`,
			want:       flow.TriggerMessage,
			interrupts: false,
		},
		"non-interrupting signal": {
			attrs:      ` attachedToRef="t1" cancelActivity="false"`,
			def:        `<bpmn:signalEventDefinition id="d1" signalRef="sig1"/>`,
			want:       flow.TriggerSignal,
			interrupts: false,
		},
	} {
		t.Run(name, func(t *testing.T) {
			r, err := importEventDoc(t, boundaryDoc(tc.attrs, tc.def))
			if err != nil {
				t.Fatalf("Import of a %s boundary: %v", name, err)
			}

			n := nodeByID(t, r, "b1")

			if got := triggersOf(t, n); len(got) != 1 || got[0] != tc.want {
				t.Errorf("triggers = %v, want [%v]", got, tc.want)
			}

			c, ok := n.(interface{ CancelActivity() bool })
			if !ok {
				t.Fatalf("%T exposes no CancelActivity()", n)
			}

			if got := c.CancelActivity(); got != tc.interrupts {
				t.Errorf("CancelActivity() = %v, want %v", got, tc.interrupts)
			}
		})
	}
}

// TestBoundaryAttachedToRef covers §6 T-10 (FR-5, FR-7): the host must
// exist and must be an activity, and each failure says which of the two
// it was.
func TestBoundaryAttachedToRef(t *testing.T) {
	const def = `<bpmn:errorEventDefinition id="d1" errorRef="err1"/>`

	for name, tc := range map[string]struct{ attrs, want string }{
		"missing": {
			attrs: ``,
			want:  "names no attachedToRef",
		},
		"unknown id": {
			attrs: ` attachedToRef="ghost"`,
			want:  `references activity "ghost" in "attachedToRef"`,
		},
		"a gateway is not an activity": {
			attrs: ` attachedToRef="s1"`,
			want:  `"s1" is a startEvent`,
		},
		"a catalog object is not an activity": {
			attrs: ` attachedToRef="m1"`,
			want:  `"m1" is a message`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := importEventDoc(t, boundaryDoc(tc.attrs, def))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Import = %v, want an error containing %q", err, tc.want)
			}
		})
	}
}

// TestBoundaryDeclaredBeforeItsHost pins that a boundary event may be
// declared before the activity it guards, which BPMN permits — a
// process's flow elements are no more ordered than a document's root
// ones.
func TestBoundaryDeclaredBeforeItsHost(t *testing.T) {
	doc := `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">` +
		catalogRoots + `
  <bpmn:process id="P" name="P">
    <bpmn:startEvent id="s1"/>
    <bpmn:boundaryEvent id="b1" name="guard" attachedToRef="t1">
      <bpmn:errorEventDefinition id="d1" errorRef="err1"/>
    </bpmn:boundaryEvent>
    <bpmn:task id="t1" name="work"/>
    <bpmn:endEvent id="e1"/>
    <bpmn:endEvent id="e2"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s1" targetRef="t1"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t1" targetRef="e1"/>
    <bpmn:sequenceFlow id="f3" sourceRef="b1" targetRef="e2"/>
  </bpmn:process>
</bpmn:definitions>`

	if _, err := importEventDoc(t, doc); err != nil {
		t.Fatalf("Import with the boundary before its host: %v", err)
	}
}

// TestBoundaryRulesAreTheModels covers §6 T-11 (NFR-1, §4.3): the trigger
// matrix and the interrupting rules belong to the model, and the
// converter reports its refusal with the file's element id rather than
// keeping a second copy of the matrix.
func TestBoundaryRulesAreTheModels(t *testing.T) {
	for name, tc := range map[string]struct{ attrs, def, want string }{
		"an Error boundary is always interrupting": {
			attrs: ` attachedToRef="t1" cancelActivity="false"`,
			def:   `<bpmn:errorEventDefinition id="d1" errorRef="err1"/>`,
			want:  "an Error boundary is always interrupting",
		},
		"a Cancel boundary needs a transaction host": {
			attrs: ` attachedToRef="t1"`,
			def:   `<bpmn:cancelEventDefinition id="d1"/>`,
			want:  "attaches only to a Transaction Sub-Process",
		},
		"a terminate trigger cannot sit on a boundary": {
			attrs: ` attachedToRef="t1"`,
			def:   `<bpmn:terminateEventDefinition id="d1"/>`,
			want:  "isn't allowed for a boundary event",
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := importEventDoc(t, boundaryDoc(tc.attrs, tc.def))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Import = %v, want the model's words %q", err, tc.want)
			}

			if !strings.Contains(err.Error(), "b1") {
				t.Errorf("error = %q, want the file's element id attached", err)
			}
		})
	}
}

// TestCompensationBoundaryAwaitsAssociations pins the one boundary this
// stage cannot build. Its handler is named by an <association>, which
// arrives with the composites stage — so the refusal says what is missing
// from the IMPORT, rather than letting the model's error name a Go
// constructor a modeler cannot call.
func TestCompensationBoundaryAwaitsAssociations(t *testing.T) {
	_, err := importEventDoc(t, boundaryDoc(` attachedToRef="t1"`,
		`<bpmn:compensateEventDefinition id="d1"/>`))
	if err == nil {
		t.Fatal("Import of a compensation boundary = nil, want it refused")
	}

	if !strings.Contains(err.Error(), "association") {
		t.Errorf("error = %q, want it to name what the import is missing", err)
	}
}

// TestBoundaryDefinitionCount pins that a boundary takes exactly one
// definition, like the intermediate events — the constructor's shape, not
// a rule of the converter's own.
func TestBoundaryDefinitionCount(t *testing.T) {
	_, err := importEventDoc(t, boundaryDoc(` attachedToRef="t1"`, ""))
	if err == nil || !strings.Contains(err.Error(), "no event definition") {
		t.Fatalf("Import = %v, want the missing definition named", err)
	}
}
