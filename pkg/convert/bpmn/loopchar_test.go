package bpmn

import (
	"context"
	"strings"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/convert"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
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

// loopDoc wraps a task carrying marker around the smallest runnable
// graph.
func loopDoc(marker string) string {
	return propDoc("",
		`    <bpmn:task id="t1" name="Work">
`+marker+`
    </bpmn:task>
    <bpmn:sequenceFlow id="f2" sourceRef="s1" targetRef="t1"/>
    <bpmn:sequenceFlow id="f3" sourceRef="t1" targetRef="e1"/>`)
}

// loopOf returns the imported t1's loop characteristics.
func loopOf(t *testing.T, res *convert.Result) activities.LoopCharacteristics {
	t.Helper()

	mt, ok := nodeByID(t, res, "t1").(*activities.ManualTask)
	if !ok {
		t.Fatalf("t1 is not a *activities.ManualTask")
	}

	return mt.LoopCharacteristics()
}

// TestStandardLoopImports is T-1 (FR-1): the condition-driven kind lands
// on its activity through WithLoop.
func TestStandardLoopImports(t *testing.T) {
	res, err := importEventDoc(t, loopDoc(
		`      <bpmn:standardLoopCharacteristics id="lc1">
        <bpmn:loopCondition language="gobpm:lite">1 &gt; 2</bpmn:loopCondition>
      </bpmn:standardLoopCharacteristics>`))
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	sl, ok := loopOf(t, res).(*activities.StandardLoopCharacteristics)
	if !ok {
		t.Fatalf("LoopCharacteristics() is %T, want the standard kind",
			loopOf(t, res))
	}

	if sl.TestBefore() {
		t.Error("TestBefore() = true; the §13.3.6 default is post-tested")
	}

	if _, bounded := sl.LoopMaximum(); bounded {
		t.Error("LoopMaximum() bounded; absent must mean unbounded")
	}

	if sl.LoopCondition() == nil {
		t.Error("LoopCondition() = nil, want the imported expression")
	}
}

// TestStandardLoopAttributesSurvive is T-2's build half.
func TestStandardLoopAttributesSurvive(t *testing.T) {
	res, err := importEventDoc(t, loopDoc(
		`      <bpmn:standardLoopCharacteristics id="lc1" testBefore="true"
                                        loopMaximum="3">
        <bpmn:loopCondition language="gobpm:lite">1 &gt; 2</bpmn:loopCondition>
      </bpmn:standardLoopCharacteristics>`))
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	sl := loopOf(t, res).(*activities.StandardLoopCharacteristics)

	if !sl.TestBefore() {
		t.Error("TestBefore() = false, want the attribute honored")
	}

	if n, ok := sl.LoopMaximum(); !ok || n != 3 {
		t.Errorf("LoopMaximum() = %d/%v, want 3", n, ok)
	}
}

// TestConditionlessStandardLoopRefused is T-3 (§4.2): the converter's
// wording, with the § — not the model's constructor name.
func TestConditionlessStandardLoopRefused(t *testing.T) {
	_, err := importEventDoc(t, loopDoc(
		`      <bpmn:standardLoopCharacteristics id="lc1"/>`))
	if err == nil {
		t.Fatal("a condition-less standard loop must be refused")
	}

	for _, want := range []string{"§13.3.6", "loopCondition", `"t1"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v, want it to carry %q", err, want)
		}
	}
}

// TestLoopMaximumRefusals is T-4: the converter refuses what the model
// never sees (non-integer text), the model refuses its own (a
// non-positive), each with the file's id attached.
func TestLoopMaximumRefusals(t *testing.T) {
	tests := map[string]struct {
		max  string
		want string
	}{
		"not an integer": {max: "abc", want: "not an integer"},
		"non-positive":   {max: "0", want: "must be positive"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := importEventDoc(t, loopDoc(
				`      <bpmn:standardLoopCharacteristics id="lc1" loopMaximum="`+
					tc.max+`">
        <bpmn:loopCondition language="gobpm:lite">1 &gt; 2</bpmn:loopCondition>
      </bpmn:standardLoopCharacteristics>`))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

// TestJUELLoopCondition is T-5: the condition rides the same language
// machinery a flow condition does — JUEL translated by its delimiters.
func TestJUELLoopCondition(t *testing.T) {
	res, err := importEventDoc(t, loopDoc(
		`      <bpmn:standardLoopCharacteristics id="lc1">
        <bpmn:loopCondition>${count &lt; 3}</bpmn:loopCondition>
      </bpmn:standardLoopCharacteristics>`))
	if err != nil {
		t.Fatalf("import: %v — the JUEL translator must serve a loop "+
			"condition as it serves a flow's", err)
	}

	if loopOf(t, res).(*activities.StandardLoopCharacteristics).LoopCondition() == nil {
		t.Error("LoopCondition() = nil, want the translated expression")
	}
}

// TestStandardLoopOnContainers is T-15's standard half: the extract
// lists every activity kind as an owner.
func TestStandardLoopOnContainers(t *testing.T) {
	const marker = `<bpmn:standardLoopCharacteristics id="lc1">
        <bpmn:loopCondition language="gobpm:lite">1 &gt; 2</bpmn:loopCondition>
      </bpmn:standardLoopCharacteristics>`

	res, err := importEventDoc(t, subProcessDoc(innerGraph+`
      `+marker))
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	sub := containerOf(t, nodeByID(t, res, "sub"))
	if sub.LoopCharacteristics() == nil {
		t.Error("the sub-process lost its loop marker")
	}
}

// TestLoopOnAGateway: not an activity — the model refuses the option
// itself, wrapped with the node's id.
func TestLoopOnAGateway(t *testing.T) {
	_, err := importEventDoc(t, propDoc("",
		`    <bpmn:exclusiveGateway id="g1">
      <bpmn:standardLoopCharacteristics id="lc1">
        <bpmn:loopCondition language="gobpm:lite">1 &gt; 2</bpmn:loopCondition>
      </bpmn:standardLoopCharacteristics>
    </bpmn:exclusiveGateway>
    <bpmn:sequenceFlow id="f2" sourceRef="s1" targetRef="g1"/>
    <bpmn:sequenceFlow id="f3" sourceRef="g1" targetRef="e1"/>`))
	if err == nil || !strings.Contains(err.Error(), `"g1"`) {
		t.Fatalf("error = %v, want the gateway's refusal under its id", err)
	}
}

// TestBrokenLoopMarkerInsideATask: the parse error surfaces through the
// node-child registration.
func TestBrokenLoopMarkerInsideATask(t *testing.T) {
	_, err := importEventDoc(t, loopDoc(
		`      <bpmn:standardLoopCharacteristics id="s1"/>`))
	if err == nil || !strings.Contains(err.Error(), "duplicate id") {
		t.Fatalf("error = %v, want the ledger's refusal via registration", err)
	}
}

// TestRefusedLanguageLoopCondition: the .B language policy reaches a
// loop condition — an explicit XPath declaration refuses by name.
func TestRefusedLanguageLoopCondition(t *testing.T) {
	_, err := importEventDoc(t, loopDoc(
		`      <bpmn:standardLoopCharacteristics id="lc1">
        <bpmn:loopCondition language="http://www.w3.org/1999/XPath">x</bpmn:loopCondition>
      </bpmn:standardLoopCharacteristics>`))
	if err == nil || !strings.Contains(err.Error(), "cannot evaluate") {
		t.Fatalf("error = %v, want the language refusal", err)
	}
}

// TestSecondLoopMarkerRefused is T-14 (FR-5): 0..1 on Activity.
func TestSecondLoopMarkerRefused(t *testing.T) {
	_, err := importEventDoc(t, loopDoc(
		`      <bpmn:standardLoopCharacteristics id="lc1">
        <bpmn:loopCondition language="gobpm:lite">1 &gt; 2</bpmn:loopCondition>
      </bpmn:standardLoopCharacteristics>
      <bpmn:standardLoopCharacteristics id="lc2">
        <bpmn:loopCondition language="gobpm:lite">1 &gt; 2</bpmn:loopCondition>
      </bpmn:standardLoopCharacteristics>`))
	if err == nil || !strings.Contains(err.Error(), "second loop marker") {
		t.Fatalf("error = %v, want the 0..1 refusal", err)
	}
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
