package bpmn

import (
	"context"
	"strings"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/convert"
)

// loopFrag parses one loop-marker fragment directly — the parser is not
// registered in nodeChildParsers until the build half exists (M2/M3), so
// M1's tests call it the way the .G M1 tests called theirs.
func loopFrag(t *testing.T, tag, frag string) (*loopSpec, []convert.Dropped, error) {
	t.Helper()

	p := newParser(context.Background(), strings.NewReader(
		`<bpmn:`+tag+` xmlns:bpmn="`+nsBPMN+`" id="lc1">`+frag+
			`</bpmn:`+tag+`>`))

	se, err := p.rootElement2()
	if err != nil {
		t.Fatalf("rootElement2: %v", err)
	}

	spec, err := p.parseLoopChar(se)

	return spec, p.dropped, err
}

// TestStandardLoopParses covers FR-1's parse half: both attributes and
// the condition child in the exprSpec shape.
func TestStandardLoopParses(t *testing.T) {
	spec, dropped, err := loopFrag(t, tagStandardLoop,
		`<bpmn:loopCondition id="c1">count &lt; 3</bpmn:loopCondition>`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	if spec.kind != tagStandardLoop || spec.testBefore || spec.loopMaximum != "" {
		t.Errorf("spec = %+v, want a bare post-tested unbounded loop", spec)
	}

	if spec.condition == nil || spec.condition.body != "count < 3" ||
		spec.condition.role != "loopCondition" {
		t.Errorf("condition = %+v, want the child's body under its role",
			spec.condition)
	}

	if len(dropped) != 0 {
		t.Errorf("dropped = %v, want nothing", dropped)
	}
}

// TestStandardLoopAttributes is T-2's parse half.
func TestStandardLoopAttributes(t *testing.T) {
	p := newParser(context.Background(), strings.NewReader(
		`<bpmn:standardLoopCharacteristics xmlns:bpmn="`+nsBPMN+
			`" id="lc1" testBefore="true" loopMaximum="3"/>`))

	se, err := p.rootElement2()
	if err != nil {
		t.Fatalf("rootElement2: %v", err)
	}

	spec, err := p.parseLoopChar(se)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	if !spec.testBefore || spec.loopMaximum != "3" {
		t.Errorf("spec = %+v, want pre-tested with maximum 3", spec)
	}
}

// TestMultiInstanceParses covers FR-2's parse half: the sequential flag,
// the collection pair, the completion condition.
func TestMultiInstanceParses(t *testing.T) {
	spec, _, err := loopFrag(t, tagMultiInstance,
		`<bpmn:loopDataInputRef>do1</bpmn:loopDataInputRef>
		 <bpmn:inputDataItem id="item1" name="order"/>
		 <bpmn:completionCondition>loopCounter &gt;= 2</bpmn:completionCondition>`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	if spec.inputRef != "do1" || spec.inputItem != "order" {
		t.Errorf("collection pair = %q/%q, want do1/order",
			spec.inputRef, spec.inputItem)
	}

	if spec.completion == nil || spec.completion.body != "loopCounter >= 2" {
		t.Errorf("completion = %+v, want the child's body", spec.completion)
	}

	if spec.isSequential {
		t.Error("isSequential defaulted true; the extract's default is false")
	}
}

// TestMultiInstanceCardinalityAndBehavior: the other attribute set.
func TestMultiInstanceCardinalityAndBehavior(t *testing.T) {
	p := newParser(context.Background(), strings.NewReader(
		`<bpmn:multiInstanceLoopCharacteristics xmlns:bpmn="`+nsBPMN+
			`" id="lc1" isSequential="true" behavior="None"`+
			` noneBehaviorEventRef="def1">`+
			`<bpmn:loopCardinality>3</bpmn:loopCardinality>`+
			`</bpmn:multiInstanceLoopCharacteristics>`))

	se, err := p.rootElement2()
	if err != nil {
		t.Fatalf("rootElement2: %v", err)
	}

	spec, err := p.parseLoopChar(se)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	if !spec.isSequential || spec.behavior != "None" || spec.noneRef != "def1" {
		t.Errorf("spec = %+v, want sequential None with def1", spec)
	}

	if spec.cardinality == nil || spec.cardinality.body != "3" {
		t.Errorf("cardinality = %+v, want the literal 3", spec.cardinality)
	}
}

// TestUnnamedInputDataItemTakesItsID: the model's binding is a name; the
// id is the standing fallback.
func TestUnnamedInputDataItemTakesItsID(t *testing.T) {
	spec, _, err := loopFrag(t, tagMultiInstance,
		`<bpmn:loopDataInputRef>do1</bpmn:loopDataInputRef>
		 <bpmn:inputDataItem id="item1"/>`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	if spec.inputItem != "item1" {
		t.Errorf("inputItem = %q, want the id fallback", spec.inputItem)
	}
}

// TestItemSubjectRefOnAnItemIsReported is T-19 (§4.5): the binding is a
// name, the declared type has no model slot.
func TestItemSubjectRefOnAnItemIsReported(t *testing.T) {
	_, dropped, err := loopFrag(t, tagMultiInstance,
		`<bpmn:inputDataItem id="item1" name="order" itemSubjectRef="idOrder"/>`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	if len(dropped) != 1 || dropped[0].Construct != attrItemSubjectRef {
		t.Fatalf("dropped = %v, want the itemSubjectRef report", dropped)
	}
}

// TestLoopMarkerIDsJoinTheLedger is T-18's ledger half: the marker's id,
// the items' ids and a complex definition's id all claim.
func TestLoopMarkerIDsJoinTheLedger(t *testing.T) {
	tests := map[string]string{
		"item vs marker":    `<bpmn:inputDataItem id="lc1"/>`,
		"complex vs marker": `<bpmn:complexBehaviorDefinition id="lc1"/>`,
	}

	for name, frag := range tests {
		t.Run(name, func(t *testing.T) {
			_, _, err := loopFrag(t, tagMultiInstance, frag)
			if err == nil || !strings.Contains(err.Error(), "duplicate id") {
				t.Fatalf("error = %v, want the ledger's refusal", err)
			}
		})
	}
}

// TestComplexBehaviorParses: both halves recorded for M4's builder.
func TestComplexBehaviorParses(t *testing.T) {
	spec, _, err := loopFrag(t, tagMultiInstance,
		`<bpmn:complexBehaviorDefinition id="cb1">
		   <bpmn:condition>done == true</bpmn:condition>
		   <bpmn:event id="ev1">
		     <bpmn:signalEventDefinition id="sd1" signalRef="sig1"/>
		   </bpmn:event>
		 </bpmn:complexBehaviorDefinition>`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	if len(spec.complex) != 1 {
		t.Fatalf("complex = %d, want 1", len(spec.complex))
	}

	cx := spec.complex[0]
	if cx.condition == nil || !cx.hasEvent || len(cx.eventDefs) != 1 {
		t.Errorf("complex = %+v, want condition + one event definition", cx)
	}
}

// TestLoopMarkerRefusals: the broken-document paths.
func TestLoopMarkerRefusals(t *testing.T) {
	tests := map[string]struct {
		frag string
		want string
	}{
		"stranger inside": {
			frag: `<bpmn:task id="t9"/>`,
			want: `unsupported element "task"`,
		},
		"stranger inside a complex definition": {
			frag: `<bpmn:complexBehaviorDefinition id="cb1"><bpmn:task id="t9"/></bpmn:complexBehaviorDefinition>`,
			want: `unsupported element "task"`,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, _, err := loopFrag(t, tagMultiInstance, tc.frag)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

// TestTruncatedLoopMarker covers the body readers' token-error paths.
func TestTruncatedLoopMarker(t *testing.T) {
	tests := map[string]string{
		"in the marker":              `<bpmn:multiInstanceLoopCharacteristics xmlns:bpmn="` + nsBPMN + `" id="lc1">`,
		"in a ref":                   `<bpmn:multiInstanceLoopCharacteristics xmlns:bpmn="` + nsBPMN + `" id="lc1"><bpmn:loopDataInputRef>half`,
		"in a complex":               `<bpmn:multiInstanceLoopCharacteristics xmlns:bpmn="` + nsBPMN + `" id="lc1"><bpmn:complexBehaviorDefinition id="cb1">`,
		"in the event":               `<bpmn:multiInstanceLoopCharacteristics xmlns:bpmn="` + nsBPMN + `" id="lc1"><bpmn:complexBehaviorDefinition id="cb1"><bpmn:event id="ev1">`,
		"in the event's foreign kid": `<bpmn:multiInstanceLoopCharacteristics xmlns:bpmn="` + nsBPMN + `" id="lc1"><bpmn:complexBehaviorDefinition id="cb1"><bpmn:event id="ev1"><x:y xmlns:x="http://x">`,
		"in the event's definition":  `<bpmn:multiInstanceLoopCharacteristics xmlns:bpmn="` + nsBPMN + `" id="lc1"><bpmn:complexBehaviorDefinition id="cb1"><bpmn:event id="ev1"><bpmn:signalEventDefinition id="sd1">`,
	}

	for name, doc := range tests {
		t.Run(name, func(t *testing.T) {
			p := newParser(context.Background(), strings.NewReader(doc))

			se, err := p.rootElement2()
			if err != nil {
				t.Fatalf("rootElement2: %v", err)
			}

			if _, err := p.parseLoopChar(se); err == nil {
				t.Fatal("a truncated marker must fail")
			}
		})
	}
}

// TestLoopMarkerEdges closes the remaining parse branches.
func TestLoopMarkerEdges(t *testing.T) {
	t.Run("marker id already declared", func(t *testing.T) {
		p := newParser(context.Background(), strings.NewReader(
			`<bpmn:standardLoopCharacteristics xmlns:bpmn="`+nsBPMN+`" id="lc1"/>`))

		p.ids["lc1"] = "task"

		se, err := p.rootElement2()
		if err != nil {
			t.Fatalf("rootElement2: %v", err)
		}

		if _, err := p.parseLoopChar(se); err == nil ||
			!strings.Contains(err.Error(), "duplicate id") {
			t.Fatalf("error = %v, want the ledger's refusal", err)
		}
	})

	t.Run("foreign child of the marker is skipped", func(t *testing.T) {
		spec, _, err := loopFrag(t, tagMultiInstance,
			`<camunda:x xmlns:camunda="http://camunda.org/schema/1.0/bpmn"/>
			 <bpmn:loopDataInputRef>do1</bpmn:loopDataInputRef>`)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}

		if spec.inputRef != "do1" {
			t.Errorf("inputRef = %q, want the ref read past the foreign child",
				spec.inputRef)
		}
	})

	t.Run("the output pair parses", func(t *testing.T) {
		spec, _, err := loopFrag(t, tagMultiInstance,
			`<bpmn:loopDataOutputRef>do2</bpmn:loopDataOutputRef>
			 <bpmn:outputDataItem id="item2" name="result"/>`)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}

		if spec.outputRef != "do2" || spec.outputItem != "result" {
			t.Errorf("output pair = %q/%q, want do2/result",
				spec.outputRef, spec.outputItem)
		}
	})

	t.Run("an empty expression child means absent", func(t *testing.T) {
		spec, _, err := loopFrag(t, tagStandardLoop,
			`<bpmn:loopCondition>   </bpmn:loopCondition>`)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}

		if spec.condition != nil {
			t.Errorf("condition = %+v, want nil for whitespace-only body",
				spec.condition)
		}
	})

	t.Run("truncated expression child", func(t *testing.T) {
		p := newParser(context.Background(), strings.NewReader(
			`<bpmn:standardLoopCharacteristics xmlns:bpmn="`+nsBPMN+
				`" id="lc1"><bpmn:loopCondition>half`))

		se, err := p.rootElement2()
		if err != nil {
			t.Fatalf("rootElement2: %v", err)
		}

		if _, err := p.parseLoopChar(se); err == nil {
			t.Fatal("a truncated condition must fail")
		}
	})
}

// TestComplexBehaviorEdges closes the complex definition's remaining
// branches.
func TestComplexBehaviorEdges(t *testing.T) {
	t.Run("foreign child skipped", func(t *testing.T) {
		spec, _, err := loopFrag(t, tagMultiInstance,
			`<bpmn:complexBehaviorDefinition id="cb1">
			   <camunda:x xmlns:camunda="http://camunda.org/schema/1.0/bpmn"/>
			   <bpmn:condition>a</bpmn:condition>
			 </bpmn:complexBehaviorDefinition>`)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}

		if spec.complex[0].condition == nil {
			t.Error("the condition after the foreign child was lost")
		}
	})

	t.Run("empty condition means absent", func(t *testing.T) {
		spec, _, err := loopFrag(t, tagMultiInstance,
			`<bpmn:complexBehaviorDefinition id="cb1">
			   <bpmn:condition> </bpmn:condition>
			 </bpmn:complexBehaviorDefinition>`)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}

		if spec.complex[0].condition != nil {
			t.Error("a whitespace-only condition must read as absent")
		}
	})

	t.Run("truncated condition", func(t *testing.T) {
		p := newParser(context.Background(), strings.NewReader(
			`<bpmn:multiInstanceLoopCharacteristics xmlns:bpmn="`+nsBPMN+
				`" id="lc1"><bpmn:complexBehaviorDefinition id="cb1">`+
				`<bpmn:condition>half`))

		se, err := p.rootElement2()
		if err != nil {
			t.Fatalf("rootElement2: %v", err)
		}

		if _, err := p.parseLoopChar(se); err == nil {
			t.Fatal("a truncated condition must fail")
		}
	})

	t.Run("event id joins the ledger", func(t *testing.T) {
		_, _, err := loopFrag(t, tagMultiInstance,
			`<bpmn:complexBehaviorDefinition id="cb1">
			   <bpmn:event id="lc1"/>
			 </bpmn:complexBehaviorDefinition>`)
		if err == nil || !strings.Contains(err.Error(), "duplicate id") {
			t.Fatalf("error = %v, want the ledger's refusal", err)
		}
	})

	t.Run("foreign child of the event skipped", func(t *testing.T) {
		spec, _, err := loopFrag(t, tagMultiInstance,
			`<bpmn:complexBehaviorDefinition id="cb1">
			   <bpmn:event id="ev1">
			     <camunda:x xmlns:camunda="http://camunda.org/schema/1.0/bpmn"/>
			   </bpmn:event>
			 </bpmn:complexBehaviorDefinition>`)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}

		if !spec.complex[0].hasEvent {
			t.Error("the event's presence was lost")
		}
	})

	t.Run("stranger inside the event", func(t *testing.T) {
		_, _, err := loopFrag(t, tagMultiInstance,
			`<bpmn:complexBehaviorDefinition id="cb1">
			   <bpmn:event id="ev1"><bpmn:task id="t9"/></bpmn:event>
			 </bpmn:complexBehaviorDefinition>`)
		if err == nil || !strings.Contains(err.Error(), `unsupported element "task"`) {
			t.Fatalf("error = %v, want the stranger refused", err)
		}
	})
}

// TestLoopSectionsRows is FR-6: the corrected § pins, on the extract's
// own heading.
func TestLoopSectionsRows(t *testing.T) {
	if got := sections[tagStandardLoop]; got != "§13.3.6" {
		t.Errorf("sections[standardLoop] = %q, want §13.3.6", got)
	}

	if got := sections[tagMultiInstance]; got != "§13.3.7" {
		t.Errorf("sections[multiInstance] = %q, want §13.3.7", got)
	}
}
