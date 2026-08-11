package bpmn

import (
	"context"
	"strings"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/convert"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/expression"
	"github.com/dr-dobermann/gobpm/pkg/model/expression/lite"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
)

// catalogRoots are the four catalog objects the typed-event tests refer
// to, declared once so each document carries only what it is about.
const catalogRoots = `
  <bpmn:message id="m1" name="Order placed"/>
  <bpmn:signal id="sig1" name="Cancelled"/>
  <bpmn:error id="err1" name="Payment failed" errorCode="E_PAY"/>
  <bpmn:escalation id="esc1" name="Overdue" escalationCode="ESC_1"/>`

// eventDoc wraps one start-event body and one end-event body in a
// two-node process, which is the smallest graph a typed event can live
// in.
func eventDoc(startBody, endBody string) string {
	return `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">` +
		catalogRoots + `
  <bpmn:process id="P" name="P">
    <bpmn:startEvent id="s1" name="start">` + startBody + `</bpmn:startEvent>
    <bpmn:endEvent id="e1" name="end">` + endBody + `</bpmn:endEvent>
    <bpmn:sequenceFlow id="f1" sourceRef="s1" targetRef="e1"/>
  </bpmn:process>
</bpmn:definitions>`
}

// importEventDoc imports a document built by eventDoc and returns the
// process together with the report.
func importEventDoc(t *testing.T, doc string) (*convert.Result, error) {
	t.Helper()

	return importer{}.ImportDocument(context.Background(), strings.NewReader(doc))
}

// triggersOf returns the event triggers a node carries, so a test can
// assert what the definition made of the event rather than that it merely
// constructed.
func triggersOf(t *testing.T, n flow.Node) []flow.EventTrigger {
	t.Helper()

	e, ok := n.(interface{ Triggers() []flow.EventTrigger })
	if !ok {
		t.Fatalf("%T exposes no Triggers()", n)
	}

	return e.Triggers()
}

// nodeByID finds an imported node.
func nodeByID(t *testing.T, r *convert.Result, id string) flow.Node {
	t.Helper()

	for _, n := range r.Processes[0].Nodes() {
		if n.ID() == id {
			return n
		}
	}

	t.Fatalf("node %q is not in the imported process", id)

	return nil
}

// TestStartEventTriggers covers SRD-089.D §6 T-4 (FR-2, FR-3): a start
// event carrying a definition imports as the typed event that definition
// makes it, each through the option its builder produced.
func TestStartEventTriggers(t *testing.T) {
	for name, tc := range map[string]struct {
		def  string
		want flow.EventTrigger
	}{
		"message": {
			def:  `<bpmn:messageEventDefinition id="d1" messageRef="m1"/>`,
			want: flow.TriggerMessage,
		},
		"timer": {
			def: `<bpmn:timerEventDefinition id="d1">` +
				`<bpmn:timeDate>2026-08-11T10:00:00Z</bpmn:timeDate>` +
				`</bpmn:timerEventDefinition>`,
			want: flow.TriggerTimer,
		},
		"signal": {
			def:  `<bpmn:signalEventDefinition id="d1" signalRef="sig1"/>`,
			want: flow.TriggerSignal,
		},
		"error": {
			def:  `<bpmn:errorEventDefinition id="d1" errorRef="err1"/>`,
			want: flow.TriggerError,
		},
		"escalation": {
			def:  `<bpmn:escalationEventDefinition id="d1" escalationRef="esc1"/>`,
			want: flow.TriggerEscalation,
		},
	} {
		t.Run(name, func(t *testing.T) {
			r, err := importEventDoc(t, eventDoc(tc.def, ""))
			if err != nil {
				t.Fatalf("Import of a %s start event: %v", name, err)
			}

			got := triggersOf(t, nodeByID(t, r, "s1"))
			if len(got) != 1 || got[0] != tc.want {
				t.Errorf("triggers = %v, want [%v]", got, tc.want)
			}
		})
	}
}

// TestEndEventTriggers covers §6 T-5 (FR-2, FR-3): the definitions an end
// event throws.
func TestEndEventTriggers(t *testing.T) {
	for name, tc := range map[string]struct {
		def  string
		want flow.EventTrigger
	}{
		"terminate": {
			def:  `<bpmn:terminateEventDefinition id="d1"/>`,
			want: flow.TriggerTerminate,
		},
		"error": {
			def:  `<bpmn:errorEventDefinition id="d1" errorRef="err1"/>`,
			want: flow.TriggerError,
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
			r, err := importEventDoc(t, eventDoc("", tc.def))
			if err != nil {
				t.Fatalf("Import of a %s end event: %v", name, err)
			}

			got := triggersOf(t, nodeByID(t, r, "e1"))
			if len(got) != 1 || got[0] != tc.want {
				t.Errorf("triggers = %v, want [%v]", got, tc.want)
			}
		})
	}
}

// TestModelOwnsThePositionRules covers §6 T-11 (NFR-1, §4.3). Both of
// these definitions are built, attached and then refused — by the MODEL,
// which knows where each trigger may execute and is the copy the runtime
// obeys.
//
// The converter deliberately does not pre-empt either refusal. A second
// implementation of the position rules would be a second thing to keep
// correct, and when the two disagreed the converter's copy would win at
// import while the model's won at run time. What the converter adds is
// the one thing the model cannot: the id of the element in the file.
func TestModelOwnsThePositionRules(t *testing.T) {
	for name, tc := range map[string]struct {
		start, end string
		wantNode   string
		want       string
	}{
		"a conditional start event at top level": {
			start: `<bpmn:conditionalEventDefinition id="d1">` +
				`<bpmn:condition language="` + lite.Language + `">x &gt; 1</bpmn:condition>` +
				`</bpmn:conditionalEventDefinition>`,
			wantNode: "s1",
			want:     "Conditional trigger isn't supported on a top-level Start Event",
		},
		"a cancel end event outside a transaction": {
			end:      `<bpmn:cancelEventDefinition id="d1"/>`,
			wantNode: "e1",
			want:     "Cancel End Event is only allowed directly inside a Transaction",
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := importEventDoc(t, eventDoc(tc.start, tc.end))
			if err == nil {
				t.Fatal("Import = nil, want the model's refusal")
			}

			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want the model's own words: %q", err, tc.want)
			}

			if !strings.Contains(err.Error(), tc.wantNode) {
				t.Errorf("error = %q, want the file's element id %q — that is "+
					"what the converter adds to the model's refusal",
					err, tc.wantNode)
			}
		})
	}
}

// TestLinkOnAStartEventIsRefused covers §6 T-15 (§4.4): the standard
// confines a link event to the intermediate position, so the model offers
// no trigger option for it — and the refusal comes from that absence
// rather than from a rule the converter re-implemented.
func TestLinkOnAStartEventIsRefused(t *testing.T) {
	_, err := importEventDoc(t,
		eventDoc(`<bpmn:linkEventDefinition id="d1" name="hop"/>`, ""))
	if err == nil {
		t.Fatal("Import of a link start event = nil, want it refused")
	}

	if !strings.Contains(err.Error(), "intermediate") {
		t.Errorf("error = %q, want it to name the position BPMN confines a "+
			"link to", err)
	}
}

// TestTimerFormsThisEngineCannotExpress covers §6 T-18 (FR-2, §4.6). The
// model demands an expression declaring "int" or "Duration" and the
// expression language produces neither, so both forms are refused with
// the reason named — the same verdict .B recorded for Camunda's
// failedJobRetryTimeCycle, which is blocked by the same gap.
func TestTimerFormsThisEngineCannotExpress(t *testing.T) {
	for name, tc := range map[string]struct {
		child, wantType string
	}{
		"timeDuration": {
			child:    `<bpmn:timeDuration>PT5M</bpmn:timeDuration>`,
			wantType: "Duration",
		},
		"timeCycle": {
			child:    `<bpmn:timeCycle>R3/PT5M</bpmn:timeCycle>`,
			wantType: "int",
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := importEventDoc(t, eventDoc(
				`<bpmn:timerEventDefinition id="d1">`+tc.child+
					`</bpmn:timerEventDefinition>`, ""))
			if err == nil {
				t.Fatalf("Import of a <%s> timer = nil, want it refused", name)
			}

			if !strings.Contains(err.Error(), tc.wantType) {
				t.Errorf("error = %q, want it to name the result type %q the "+
					"expression language cannot declare", err, tc.wantType)
			}

			if !strings.Contains(err.Error(), "timeDate") {
				t.Errorf("error = %q, want it to name the form that DOES "+
					"import — a refusal with no way forward is a dead end", err)
			}
		})
	}
}

// TestTimeDateIsValidatedAtImport covers §6 T-19 (§4.6): BPMN constrains
// the literal's format not at all, while lite's time() accepts RFC3339
// alone. A value this engine cannot read is refused at import rather than
// at the first firing, when the file that caused it is long out of sight.
func TestTimeDateIsValidatedAtImport(t *testing.T) {
	for name, literal := range map[string]string{
		"zone-less ISO-8601": "2026-08-11T10:00:00",
		"a date alone":       "2026-08-11",
		"not a timestamp":    "tomorrow",
		"empty":              "",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := importEventDoc(t, eventDoc(
				`<bpmn:timerEventDefinition id="d1"><bpmn:timeDate>`+literal+
					`</bpmn:timeDate></bpmn:timerEventDefinition>`, ""))
			if err == nil {
				t.Fatalf("Import of <timeDate>%s = nil, want it refused", literal)
			}

			if !strings.Contains(err.Error(), "RFC3339") {
				t.Errorf("error = %q, want it to name the format required", err)
			}
		})
	}
}

// TestTimeDateEvaluatesToItsInstant covers §6 T-20: the minted expression
// yields the moment the file named. Construction alone would not show
// that — the model checks the DECLARED result type, so an expression that
// declares "Time" and cannot produce one would still build.
func TestTimeDateEvaluatesToItsInstant(t *testing.T) {
	const instant = "2026-08-11T10:00:00Z"

	r, err := importEventDoc(t, eventDoc(
		`<bpmn:timerEventDefinition id="d1"><bpmn:timeDate>`+instant+
			`</bpmn:timeDate></bpmn:timerEventDefinition>`, ""))
	if err != nil {
		t.Fatalf("Import of a timeDate timer: %v", err)
	}

	expr, err := timeDateExpression("startEvent \"s1\"", exprSpec{
		ownerID: "d1", role: tagTimeDate, body: instant,
	})
	if err != nil {
		t.Fatalf("timeDateExpression: %v", err)
	}

	if got := expr.ResultType(); got != "Time" {
		t.Fatalf("ResultType = %q, want %q — the model checks it", got, "Time")
	}

	reg, err := expression.NewRegistry(lite.New())
	if err != nil {
		t.Fatalf("expression.NewRegistry: %v", err)
	}

	v, err := reg.Evaluate(context.Background(), expr, emptySource{})
	if err != nil {
		t.Fatalf("evaluating the minted timeDate: %v — a declared result type "+
			"the expression cannot actually produce would pass construction "+
			"and fail at the first firing", err)
	}

	if v == nil {
		t.Fatal("the timeDate evaluated to nothing")
	}

	// The event itself imported as a timer.
	if got := triggersOf(t, nodeByID(t, r, "s1")); len(got) != 1 ||
		got[0] != flow.TriggerTimer {
		t.Errorf("triggers = %v, want [%v]", got, flow.TriggerTimer)
	}
}

// TestDefinitionReferenceMustResolve covers §6 T-13 and T-14 (FR-7): a
// reference to something the document never declared is an error naming
// the attribute, and a reference to an id of the WRONG kind says so
// instead — that id IS in the file, and calling it missing sends its
// author hunting a typo that is not there.
func TestDefinitionReferenceMustResolve(t *testing.T) {
	for name, tc := range map[string]struct{ def, want string }{
		"unknown messageRef": {
			def:  `<bpmn:messageEventDefinition id="d1" messageRef="ghost"/>`,
			want: `references message "ghost" in "messageRef"`,
		},
		"unknown signalRef": {
			def:  `<bpmn:signalEventDefinition id="d1" signalRef="ghost"/>`,
			want: `references signal "ghost" in "signalRef"`,
		},
		"unknown errorRef": {
			def:  `<bpmn:errorEventDefinition id="d1" errorRef="ghost"/>`,
			want: `references error "ghost" in "errorRef"`,
		},
		"unknown escalationRef": {
			def:  `<bpmn:escalationEventDefinition id="d1" escalationRef="ghost"/>`,
			want: `references escalation "ghost" in "escalationRef"`,
		},
		"messageRef naming a signal": {
			def:  `<bpmn:messageEventDefinition id="d1" messageRef="sig1"/>`,
			want: `"sig1" is a signal`,
		},
		"signalRef naming a flow node": {
			def:  `<bpmn:signalEventDefinition id="d1" signalRef="e1"/>`,
			want: `"e1" is a endEvent`,
		},
		"unknown operationRef": {
			def: `<bpmn:messageEventDefinition id="d1" messageRef="m1">` +
				`<bpmn:operationRef>ghost</bpmn:operationRef>` +
				`</bpmn:messageEventDefinition>`,
			want: `references operation "ghost"`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := importEventDoc(t, eventDoc(tc.def, ""))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Import = %v, want an error containing %q", err, tc.want)
			}
		})
	}
}

// TestCatalogAfterProcessResolves covers §6 T-23 (§4.7): the <message> a
// start event refers to is declared after the process, which BPMN allows
// and modeler output does routinely.
func TestCatalogAfterProcessResolves(t *testing.T) {
	doc := `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:process id="P" name="P">
    <bpmn:startEvent id="s1" name="start">
      <bpmn:messageEventDefinition id="d1" messageRef="m1"/>
    </bpmn:startEvent>
    <bpmn:endEvent id="e1" name="end"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s1" targetRef="e1"/>
  </bpmn:process>
  <bpmn:message id="m1" name="Order placed"/>
</bpmn:definitions>`

	r, err := importEventDoc(t, doc)
	if err != nil {
		t.Fatalf("Import with the message declared after the process: %v", err)
	}

	if got := triggersOf(t, nodeByID(t, r, "s1")); len(got) != 1 ||
		got[0] != flow.TriggerMessage {
		t.Errorf("triggers = %v, want [%v]", got, flow.TriggerMessage)
	}
}

// TestCompensationActivityRef covers the reference a definition makes to
// a FLOW NODE rather than to a catalog object: it resolves in either
// document order, and naming something that is not an activity is an
// error of its own.
func TestCompensationActivityRef(t *testing.T) {
	docWith := func(order string) string {
		return `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:process id="P" name="P">
    <bpmn:startEvent id="s1"/>
` + order + `
    <bpmn:sequenceFlow id="f1" sourceRef="s1" targetRef="t1"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t1" targetRef="e1"/>
  </bpmn:process>
</bpmn:definitions>`
	}

	activity := `    <bpmn:task id="t1" name="book"/>`
	event := `    <bpmn:endEvent id="e1">
      <bpmn:compensateEventDefinition id="d1" activityRef="t1"/>
    </bpmn:endEvent>`

	t.Run("activity declared first", func(t *testing.T) {
		if _, err := importEventDoc(t, docWith(activity+"\n"+event)); err != nil {
			t.Fatalf("Import: %v", err)
		}
	})

	t.Run("activity declared after the event naming it", func(t *testing.T) {
		if _, err := importEventDoc(t, docWith(event+"\n"+activity)); err != nil {
			t.Fatalf("Import with the activity declared later: %v — BPMN does "+
				"not order a process's flow elements", err)
		}
	})

	t.Run("activityRef naming something that is not an activity", func(t *testing.T) {
		_, err := importEventDoc(t, docWith(activity+`
    <bpmn:endEvent id="e1">
      <bpmn:compensateEventDefinition id="d1" activityRef="s1"/>
    </bpmn:endEvent>`))
		if err == nil || !strings.Contains(err.Error(), "is a startEvent") {
			t.Fatalf("Import = %v, want the wrong-kind reference named", err)
		}
	})

	t.Run("unknown activityRef", func(t *testing.T) {
		_, err := importEventDoc(t, docWith(activity+`
    <bpmn:endEvent id="e1">
      <bpmn:compensateEventDefinition id="d1" activityRef="ghost"/>
    </bpmn:endEvent>`))
		if err == nil || !strings.Contains(err.Error(), `activity "ghost"`) {
			t.Fatalf("Import = %v, want the missing activity named", err)
		}
	})
}

// TestDefinitionWithNothingToActOn refuses the three definitions whose
// required content the file did not supply. Each would otherwise import
// as an event that cannot fire, and fail with far less context than this.
func TestDefinitionWithNothingToActOn(t *testing.T) {
	for name, tc := range map[string]struct{ def, want string }{
		"conditional with no condition": {
			def:  `<bpmn:conditionalEventDefinition id="d1"/>`,
			want: "nothing to evaluate",
		},
		"timer with no time": {
			def:  `<bpmn:timerEventDefinition id="d1"/>`,
			want: "no moment to fire at",
		},
		"link with no name": {
			def:  `<bpmn:linkEventDefinition id="d1"/>`,
			want: "connects nothing",
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := importEventDoc(t, eventDoc(tc.def, ""))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Import = %v, want an error containing %q", err, tc.want)
			}
		})
	}
}

// TestStartEventAttributes covers the two attributes of FR-3. Only a
// non-default value produces an option: the model's defaults are the
// standard's, so passing them back would assert as a decision what the
// file never stated.
func TestStartEventAttributes(t *testing.T) {
	for name, tc := range map[string]struct {
		attrs               string
		parallel, interrupt bool
	}{
		"defaults":         {attrs: ``, parallel: false, interrupt: true},
		"parallelMultiple": {attrs: ` parallelMultiple="true"`, parallel: true, interrupt: true},
		"non-interrupting": {attrs: ` isInterrupting="false"`, parallel: false, interrupt: false},
	} {
		t.Run(name, func(t *testing.T) {
			doc := `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">` +
				catalogRoots + `
  <bpmn:process id="P" name="P">
    <bpmn:startEvent id="s1" name="start"` + tc.attrs + `>
      <bpmn:signalEventDefinition id="d1" signalRef="sig1"/>
      <bpmn:messageEventDefinition id="d2" messageRef="m1"/>
    </bpmn:startEvent>
    <bpmn:endEvent id="e1" name="end"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s1" targetRef="e1"/>
  </bpmn:process>
</bpmn:definitions>`

			r, err := importEventDoc(t, doc)
			if err != nil {
				t.Fatalf("Import: %v", err)
			}

			n := nodeByID(t, r, "s1")

			p, ok := n.(interface{ IsParallelMultiple() bool })
			if !ok {
				t.Fatalf("%T exposes no IsParallelMultiple()", n)
			}

			if got := p.IsParallelMultiple(); got != tc.parallel {
				t.Errorf("IsParallelMultiple() = %v, want %v — the flag only "+
					"means anything with more than one trigger "+
					"(events/event.go:435)", got, tc.parallel)
			}

			i, ok := n.(interface{ IsInterrupting() bool })
			if !ok {
				t.Fatalf("%T exposes no IsInterrupting()", n)
			}

			if got := i.IsInterrupting(); got != tc.interrupt {
				t.Errorf("IsInterrupting() = %v, want %v", got, tc.interrupt)
			}
		})
	}
}

// TestLinkPairingRefsAreReported pins that an explicit link source/target
// is reported rather than dropped: this engine pairs links by name, so a
// file whose counterpart is named differently imports as two links that
// do not connect — and only the report says so.
func TestLinkPairingRefsAreReported(t *testing.T) {
	doc := `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:process id="P" name="P">
    <bpmn:startEvent id="s1"/>
    <bpmn:endEvent id="e1">
      <bpmn:linkEventDefinition id="d1" name="hop">
        <bpmn:source>d2</bpmn:source>
      </bpmn:linkEventDefinition>
    </bpmn:endEvent>
    <bpmn:sequenceFlow id="f1" sourceRef="s1" targetRef="e1"/>
  </bpmn:process>
</bpmn:definitions>`

	_, err := importEventDoc(t, doc)
	if err == nil {
		t.Fatal("Import of a link end event = nil, want it refused — the " +
			"model has no trigger for a link outside the intermediate position")
	}

	// The report is collected during the parse, which precedes the refusal.
	if !strings.Contains(err.Error(), "intermediate") {
		t.Errorf("error = %q, want the link position refusal", err)
	}
}

// TestEventDefinitionDocumentation pins that a definition carries its own
// <documentation>, since a definition is a BaseElement like any other.
func TestEventDefinitionDocumentation(t *testing.T) {
	r, err := importEventDoc(t, eventDoc(
		`<bpmn:signalEventDefinition id="d1" signalRef="sig1">`+
			`<bpmn:documentation>fires on cancellation</bpmn:documentation>`+
			`</bpmn:signalEventDefinition>`, ""))
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	n := nodeByID(t, r, "s1")

	e, ok := n.(interface{ Definitions() []flow.EventDefinition })
	if !ok {
		t.Fatalf("%T exposes no Definitions()", n)
	}

	defs := e.Definitions()
	if len(defs) != 1 {
		t.Fatalf("definitions = %d, want 1", len(defs))
	}

	docs := defs[0].Docs()
	if len(docs) != 1 || docs[0].Text() != "fires on cancellation" {
		t.Errorf("definition docs = %v, want the documentation the file carried", docs)
	}
}

// TestUnknownDefinitionChildIsRefused pins the default disposition inside
// an event definition: an in-namespace child no parser claims is refused
// rather than swallowed.
func TestUnknownDefinitionChildIsRefused(t *testing.T) {
	_, err := importEventDoc(t, eventDoc(
		`<bpmn:signalEventDefinition id="d1" signalRef="sig1">`+
			`<bpmn:extensionElements/>`+
			`<bpmn:auditing id="a1"/>`+
			`</bpmn:signalEventDefinition>`, ""))

	var uee *convert.UnsupportedElementError
	if !strings.Contains(errString(err), "auditing") {
		t.Fatalf("Import = %v (%T), want the unclaimed child refused", err, uee)
	}
}

// errString is err.Error() for a possibly-nil error.
func errString(err error) string {
	if err == nil {
		return ""
	}

	return err.Error()
}

// emptySource is a data.Source that serves nothing. The minted timeDate
// reads no variables — it is a literal wrapped in time() — so a source
// that finds nothing is enough to evaluate it, and a richer one would
// hide a dependency the expression must not have.
type emptySource struct{}

func (emptySource) Find(_ context.Context, name string) (data.Data, error) {
	return nil, errs.New(
		errs.M("no data source is wired for %q", name),
		errs.C("TEST", errs.ObjectNotFound))
}
