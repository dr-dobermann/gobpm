package bpmn

import (
	"strings"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/expression/lite"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
)

// intermediateDoc puts one intermediate event between a start and an end
// event, which is the only place BPMN allows one.
func intermediateDoc(tag, body string) string {
	return `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">` +
		catalogRoots + `
  <bpmn:process id="P" name="P">
    <bpmn:startEvent id="s1"/>
    <bpmn:` + tag + ` id="i1" name="wait">` + body + `</bpmn:` + tag + `>
    <bpmn:endEvent id="e1"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s1" targetRef="i1"/>
    <bpmn:sequenceFlow id="f2" sourceRef="i1" targetRef="e1"/>
  </bpmn:process>
</bpmn:definitions>`
}

// TestIntermediateCatchEvents covers SRD-089.D §6 T-6 (FR-4): a catch
// event takes its definition POSITIONALLY, which is the one place the
// model accepts a link — the position the standard confines it to.
func TestIntermediateCatchEvents(t *testing.T) {
	for name, tc := range map[string]struct {
		def  string
		want flow.EventTrigger
	}{
		"timer": {
			def: `<bpmn:timerEventDefinition id="d1">` +
				`<bpmn:timeDate>2026-08-11T10:00:00Z</bpmn:timeDate>` +
				`</bpmn:timerEventDefinition>`,
			want: flow.TriggerTimer,
		},
		"message": {
			def:  `<bpmn:messageEventDefinition id="d1" messageRef="m1"/>`,
			want: flow.TriggerMessage,
		},
		"signal": {
			def:  `<bpmn:signalEventDefinition id="d1" signalRef="sig1"/>`,
			want: flow.TriggerSignal,
		},
		"conditional": {
			def: `<bpmn:conditionalEventDefinition id="d1">` +
				`<bpmn:condition language="` + lite.Language + `">x &gt; 1</bpmn:condition>` +
				`</bpmn:conditionalEventDefinition>`,
			want: flow.TriggerConditional,
		},
	} {
		t.Run(name, func(t *testing.T) {
			r, err := importEventDoc(t,
				intermediateDoc(tagIntermediateCatch, tc.def))
			if err != nil {
				t.Fatalf("Import of a %s catch event: %v", name, err)
			}

			got := triggersOf(t, nodeByID(t, r, "i1"))
			if len(got) != 1 || got[0] != tc.want {
				t.Errorf("triggers = %v, want [%v]", got, tc.want)
			}
		})
	}
}

// TestIntermediateThrowEvents covers §6 T-7 (FR-4).
func TestIntermediateThrowEvents(t *testing.T) {
	for name, tc := range map[string]struct {
		def  string
		want flow.EventTrigger
	}{
		"signal": {
			def:  `<bpmn:signalEventDefinition id="d1" signalRef="sig1"/>`,
			want: flow.TriggerSignal,
		},
		"escalation": {
			def:  `<bpmn:escalationEventDefinition id="d1" escalationRef="esc1"/>`,
			want: flow.TriggerEscalation,
		},
		"compensation": {
			def:  `<bpmn:compensateEventDefinition id="d1"/>`,
			want: flow.TriggerCompensation,
		},
	} {
		t.Run(name, func(t *testing.T) {
			r, err := importEventDoc(t,
				intermediateDoc(tagIntermediateThrow, tc.def))
			if err != nil {
				t.Fatalf("Import of a %s throw event: %v", name, err)
			}

			got := triggersOf(t, nodeByID(t, r, "i1"))
			if len(got) != 1 || got[0] != tc.want {
				t.Errorf("triggers = %v, want [%v]", got, tc.want)
			}
		})
	}
}

// TestIntermediateEventDefinitionCount covers §6 T-8 (FR-4) and §4.10.
//
// Neither zero nor two is buildable, and neither is quietly reduced: the
// constructor takes exactly one definition positionally, so importing the
// first of several would produce an event waiting for less than the file
// asked for — with nothing downstream to say so.
func TestIntermediateEventDefinitionCount(t *testing.T) {
	for _, tag := range []string{tagIntermediateCatch, tagIntermediateThrow} {
		t.Run(tag+"/none", func(t *testing.T) {
			_, err := importEventDoc(t, intermediateDoc(tag, ""))
			if err == nil || !strings.Contains(err.Error(), "no event definition") {
				t.Fatalf("Import = %v, want the missing definition named", err)
			}
		})

		t.Run(tag+"/two", func(t *testing.T) {
			_, err := importEventDoc(t, intermediateDoc(tag,
				`<bpmn:signalEventDefinition id="d1" signalRef="sig1"/>`+
					`<bpmn:escalationEventDefinition id="d2" escalationRef="esc1"/>`))
			if err == nil || !strings.Contains(err.Error(), "exactly one") {
				t.Fatalf("Import = %v, want the count refused", err)
			}
		})
	}
}

// TestIntermediateTriggerPositionIsTheModels pins that WHICH triggers may
// catch and which may throw stays the model's rule (NFR-1): a terminate
// definition on a catch event is refused by the constructor's own trigger
// set, not by a table the converter maintains in parallel.
func TestIntermediateTriggerPositionIsTheModels(t *testing.T) {
	_, err := importEventDoc(t, intermediateDoc(tagIntermediateCatch,
		`<bpmn:terminateEventDefinition id="d1"/>`))
	if err == nil {
		t.Fatal("Import of a terminate catch event = nil, want the model's refusal")
	}

	if !strings.Contains(err.Error(), "isn't allowed for an IntermediateCatchEvent") {
		t.Errorf("error = %q, want the model's own words", err)
	}

	if !strings.Contains(err.Error(), "i1") {
		t.Errorf("error = %q, want the file's element id attached", err)
	}
}

// TestIntermediateEventKeepsItsName pins the name fallback: BPMN makes
// name optional and the model demands one, so an unlabelled event takes
// its id rather than being refused over a cosmetic field.
func TestIntermediateEventKeepsItsName(t *testing.T) {
	doc := strings.Replace(
		intermediateDoc(tagIntermediateCatch,
			`<bpmn:signalEventDefinition id="d1" signalRef="sig1"/>`),
		` name="wait"`, "", 1)

	r, err := importEventDoc(t, doc)
	if err != nil {
		t.Fatalf("Import of an unnamed intermediate event: %v", err)
	}

	if got := nodeByID(t, r, "i1").Name(); got != "i1" {
		t.Errorf("name = %q, want the id %q", got, "i1")
	}
}

// TestLinkPairImports covers the link event, which the standard confines
// to the intermediate position — the one place this model takes a
// definition positionally.
//
// The pair is imported together because the model refuses half of one: a
// catch with no throw, or a throw with no catch, is a jump that goes
// nowhere. That rule is the model's, and the converter neither
// re-implements nor pre-empts it.
func TestLinkPairImports(t *testing.T) {
	doc := `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:process id="P" name="P">
    <bpmn:startEvent id="s1"/>
    <bpmn:intermediateThrowEvent id="jump" name="go">
      <bpmn:linkEventDefinition id="d1" name="hop"/>
    </bpmn:intermediateThrowEvent>
    <bpmn:intermediateCatchEvent id="land" name="arrive">
      <bpmn:linkEventDefinition id="d2" name="hop"/>
    </bpmn:intermediateCatchEvent>
    <bpmn:endEvent id="e1"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s1" targetRef="jump"/>
    <bpmn:sequenceFlow id="f2" sourceRef="land" targetRef="e1"/>
  </bpmn:process>
</bpmn:definitions>`

	r, err := importEventDoc(t, doc)
	if err != nil {
		t.Fatalf("Import of a link pair: %v", err)
	}

	for id := range map[string]struct{}{"jump": {}, "land": {}} {
		got := triggersOf(t, nodeByID(t, r, id))
		if len(got) != 1 || got[0] != flow.TriggerLink {
			t.Errorf("%s triggers = %v, want [%v]", id, got, flow.TriggerLink)
		}
	}
}

// TestHalfALinkIsRefused pins that the pairing rule stays the model's: a
// throw whose counterpart the file never declared is refused there, with
// the link's name in the message.
func TestHalfALinkIsRefused(t *testing.T) {
	_, err := importEventDoc(t, intermediateDoc(tagIntermediateThrow,
		`<bpmn:linkEventDefinition id="d1" name="hop"/>`))
	if err == nil {
		t.Fatal("Import of an unpaired link = nil, want the model's refusal")
	}

	if !strings.Contains(err.Error(), "no target catch") {
		t.Errorf("error = %q, want the model's own words about the pairing", err)
	}
}
