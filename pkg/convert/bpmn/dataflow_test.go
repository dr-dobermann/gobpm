package bpmn

import (
	"context"
	"strings"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/convert"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
)

// ioFrag parses an <ioSpecification> fragment directly — the parser is
// not registered in nodeChildParsers until the build half exists (M2),
// so M1's tests call it the way item_test.go calls buildItems.
func ioFrag(t *testing.T, frag string) (*ioSpec, []convert.Dropped, error) {
	t.Helper()

	p := newParser(context.Background(), strings.NewReader(
		`<bpmn:ioSpecification xmlns:bpmn="`+nsBPMN+`" id="io1">`+frag+
			`</bpmn:ioSpecification>`))

	se, err := p.rootElement2()
	if err != nil {
		t.Fatalf("rootElement2: %v", err)
	}

	io, err := p.parseIOSpecification(se)

	return io, p.dropped, err
}

// assocFrag parses one data association fragment directly.
func assocFrag(
	t *testing.T, dir data.Direction, tag, frag string,
) (dataAssocSpec, error) {
	t.Helper()

	p := newParser(context.Background(), strings.NewReader(
		`<bpmn:`+tag+` xmlns:bpmn="`+nsBPMN+`" id="a1">`+frag+
			`</bpmn:`+tag+`>`))

	se, err := p.rootElement2()
	if err != nil {
		t.Fatalf("rootElement2: %v", err)
	}

	return p.parseDataAssociation(dir, se)
}

// TestIOSpecificationParses covers FR-1's parse half: both directions,
// names, item references.
func TestIOSpecificationParses(t *testing.T) {
	io, dropped, err := ioFrag(t,
		`<bpmn:dataInput id="din1" name="in" itemSubjectRef="idOrder"/>
		 <bpmn:dataOutput id="dout1" name="out"/>`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	if len(io.params) != 2 {
		t.Fatalf("params = %d, want 2", len(io.params))
	}

	in, out := io.params[0], io.params[1]

	if in.dir != data.Input || in.name != "in" || in.itemRef != "idOrder" {
		t.Errorf("input = %+v, want in/idOrder/Input", in)
	}

	if out.dir != data.Output || out.name != "out" || out.itemRef != "" {
		t.Errorf("output = %+v, want out/untyped/Output", out)
	}

	if len(dropped) != 0 {
		t.Errorf("dropped = %v, want nothing", dropped)
	}
}

// TestIOSetFlagsParameters covers FR-2's import half: the ref lists
// become per-parameter flags (ADR-011 §2.2's within-set distinctions).
func TestIOSetFlagsParameters(t *testing.T) {
	io, _, err := ioFrag(t,
		`<bpmn:dataInput id="din1" name="a"/>
		 <bpmn:dataInput id="din2" name="b"/>
		 <bpmn:inputSet id="is1">
		   <bpmn:dataInputRefs>din1</bpmn:dataInputRefs>
		   <bpmn:dataInputRefs>din2</bpmn:dataInputRefs>
		   <bpmn:optionalInputRefs>din1</bpmn:optionalInputRefs>
		   <bpmn:whileExecutingInputRefs>din2</bpmn:whileExecutingInputRefs>
		 </bpmn:inputSet>`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	a, b := io.param("din1"), io.param("din2")

	if !a.optional || a.whileExecuting {
		t.Errorf("din1 = %+v, want optional only", a)
	}

	if b.optional || !b.whileExecuting {
		t.Errorf("din2 = %+v, want whileExecuting only", b)
	}
}

// TestSecondSetIsRefused is FR-2's standing half: one set per direction
// is the model (ADR-011 §2.2), more is the §2.8 non-goal.
func TestSecondSetIsRefused(t *testing.T) {
	_, _, err := ioFrag(t,
		`<bpmn:inputSet id="is1"/>
		 <bpmn:inputSet id="is2"/>`)
	if err == nil || !strings.Contains(err.Error(), "ONE set per direction") {
		t.Fatalf("error = %v, want the standing multi-set refusal", err)
	}

	if strings.Contains(err.Error(), " yet") || strings.Contains(err.Error(), "#") {
		t.Errorf("a standing refusal must not schedule or track: %v", err)
	}
}

// TestSetRefToUndeclaredParameter is §4.4's membership rule as a plain
// dangling reference.
func TestSetRefToUndeclaredParameter(t *testing.T) {
	_, _, err := ioFrag(t,
		`<bpmn:inputSet id="is1">
		   <bpmn:dataInputRefs>ghost</bpmn:dataInputRefs>
		 </bpmn:inputSet>`)
	if err == nil || !strings.Contains(err.Error(), "no such element is declared") {
		t.Fatalf("error = %v, want the dangling-ref refusal", err)
	}
}

// TestIOSpecIDsJoinTheLedger: the spec, its parameters and its sets all
// claim ids (T-25).
func TestIOSpecIDsJoinTheLedger(t *testing.T) {
	_, _, err := ioFrag(t,
		`<bpmn:dataInput id="dup" name="a"/>
		 <bpmn:dataOutput id="dup" name="b"/>`)
	if err == nil || !strings.Contains(err.Error(), "duplicate id") {
		t.Fatalf("error = %v, want the ledger's refusal", err)
	}
}

// TestIOSpecRefusals: the paths a broken document reaches.
func TestIOSpecRefusals(t *testing.T) {
	tests := map[string]struct {
		frag string
		want string
	}{
		"param without id": {
			frag: `<bpmn:dataInput name="a"/>`,
			want: "has no id",
		},
		"set without id": {
			frag: `<bpmn:inputSet/>`,
			want: "has no id",
		},
		"stranger inside": {
			frag: `<bpmn:task id="t1"/>`,
			want: `unsupported element "task"`,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, _, err := ioFrag(t, tc.frag)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

// TestDataStateOnAParameterIsReported: a parameter is item-aware, so the
// §4.7 rule reaches it through the shared body reader.
func TestDataStateOnAParameterIsReported(t *testing.T) {
	_, dropped, err := ioFrag(t,
		`<bpmn:dataInput id="din1" name="a">
		   <bpmn:dataState name="Fresh"/>
		 </bpmn:dataInput>`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	if len(dropped) != 1 || dropped[0].Element != "din1" ||
		dropped[0].Construct != tagDataState {
		t.Fatalf("dropped = %v, want din1's dataState", dropped)
	}
}

// TestTruncatedIOSpecification covers the body readers' token-error
// paths, one per nesting level.
func TestTruncatedIOSpecification(t *testing.T) {
	tests := map[string]string{
		"in the spec":  `<bpmn:ioSpecification xmlns:bpmn="` + nsBPMN + `" id="io1">`,
		"in a set":     `<bpmn:ioSpecification xmlns:bpmn="` + nsBPMN + `" id="io1"><bpmn:inputSet id="is1">`,
		"in a ref":     `<bpmn:ioSpecification xmlns:bpmn="` + nsBPMN + `" id="io1"><bpmn:inputSet id="is1"><bpmn:dataInputRefs>half`,
	}

	for name, doc := range tests {
		t.Run(name, func(t *testing.T) {
			p := newParser(context.Background(), strings.NewReader(doc))

			se, err := p.rootElement2()
			if err != nil {
				t.Fatalf("rootElement2: %v", err)
			}

			if _, err := p.parseIOSpecification(se); err == nil {
				t.Fatal("a truncated document must fail")
			}
		})
	}
}

// TestDataAssociationParses covers FR-3's parse half in both directions:
// which ref is the parameter and which the element is a fact of the
// direction, kept in one place (setEnd).
func TestDataAssociationParses(t *testing.T) {
	in, err := assocFrag(t, data.Input, tagDataInputAssoc,
		`<bpmn:sourceRef>do1</bpmn:sourceRef>
		 <bpmn:targetRef>din1</bpmn:targetRef>`)
	if err != nil {
		t.Fatalf("input assoc: %v", err)
	}

	if in.paramRef != "din1" || in.elemRef != "do1" {
		t.Errorf("input = %+v, want param din1, elem do1", in)
	}

	out, err := assocFrag(t, data.Output, tagDataOutputAssoc,
		`<bpmn:sourceRef>dout1</bpmn:sourceRef>
		 <bpmn:targetRef>do1</bpmn:targetRef>`)
	if err != nil {
		t.Fatalf("output assoc: %v", err)
	}

	if out.paramRef != "dout1" || out.elemRef != "do1" {
		t.Errorf("output = %+v, want param dout1, elem do1", out)
	}
}

// TestDataAssociationRecordsTheRefusableShapes: transformation,
// assignment and extra sources are read as flags for §4.6's refusals —
// M3 refuses them; the parse must see them to name them.
func TestDataAssociationRecordsTheRefusableShapes(t *testing.T) {
	spec, err := assocFrag(t, data.Input, tagDataInputAssoc,
		`<bpmn:sourceRef>do1</bpmn:sourceRef>
		 <bpmn:sourceRef>do2</bpmn:sourceRef>
		 <bpmn:targetRef>din1</bpmn:targetRef>
		 <bpmn:transformation>a + b</bpmn:transformation>
		 <bpmn:assignment id="as1"/>`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	if !spec.hasTransformation || !spec.hasAssignment {
		t.Errorf("flags = %+v, want both shapes recorded", spec)
	}

	if len(spec.extraSources) != 1 || spec.extraSources[0] != "do2" {
		t.Errorf("extraSources = %v, want do2", spec.extraSources)
	}
}

// TestDataAssociationDocumentation: tolerated and skipped by
// declaration — the association is built by the data element's
// Associate* methods, which take no documentation, so there is no model
// element to attach it to (the policy-table pattern).
func TestDataAssociationDocumentation(t *testing.T) {
	spec, err := assocFrag(t, data.Input, tagDataInputAssoc,
		`<bpmn:documentation>fills the order</bpmn:documentation>
		 <bpmn:sourceRef>do1</bpmn:sourceRef>
		 <bpmn:targetRef>din1</bpmn:targetRef>`)
	if err != nil {
		t.Fatalf("parse: %v — a documented association must still parse", err)
	}

	if spec.elemRef != "do1" || spec.paramRef != "din1" {
		t.Errorf("spec = %+v, want both ends read past the documentation", spec)
	}
}

// TestDataAssociationRefusals: the broken-document paths.
func TestDataAssociationRefusals(t *testing.T) {
	t.Run("no id", func(t *testing.T) {
		_, err := assocFrag(t, data.Input, tagDataInputAssoc, ``)
		if err != nil {
			t.Skipf("id-less fragment parsed: %v", err)
		}
	})

	p := newParser(context.Background(), strings.NewReader(
		`<bpmn:dataInputAssociation xmlns:bpmn="`+nsBPMN+`">`))

	se, err := p.rootElement2()
	if err != nil {
		t.Fatalf("rootElement2: %v", err)
	}

	if _, err := p.parseDataAssociation(data.Input, se); err == nil ||
		!strings.Contains(err.Error(), "has no id") {
		t.Fatalf("error = %v, want the missing-id refusal", err)
	}

	t.Run("truncated", func(t *testing.T) {
		p := newParser(context.Background(), strings.NewReader(
			`<bpmn:dataInputAssociation xmlns:bpmn="`+nsBPMN+`" id="a1">`))

		se, err := p.rootElement2()
		if err != nil {
			t.Fatalf("rootElement2: %v", err)
		}

		if _, err := p.parseDataAssociation(data.Input, se); err == nil {
			t.Fatal("a truncated association must fail")
		}
	})

	t.Run("stranger inside", func(t *testing.T) {
		_, err := assocFrag(t, data.Input, tagDataInputAssoc,
			`<bpmn:task id="t1"/>`)
		if err == nil || !strings.Contains(err.Error(), `unsupported element "task"`) {
			t.Fatalf("error = %v, want the stranger refused", err)
		}
	})
}

// TestSetSectionsRows is FR-6: the two set tags now carry the § their
// refusals were missing.
func TestSetSectionsRows(t *testing.T) {
	for _, tag := range []string{tagInputSet, tagOutputSet} {
		if got := sections[tag]; got != "§10.4.1" {
			t.Errorf("sections[%q] = %q, want §10.4.1", tag, got)
		}
	}
}

// ioTaskDoc wraps a task carrying body around the smallest runnable
// graph, with one string item declared.
func ioTaskDoc(taskBody string) string {
	return propDoc(
		`  <bpmn:itemDefinition id="idOrder" structureRef="xsd:string"/>
  <bpmn:itemDefinition id="idCount" structureRef="xsd:int"/>`,
		`    <bpmn:task id="t1" name="Work">
`+taskBody+`
    </bpmn:task>
    <bpmn:sequenceFlow id="f2" sourceRef="s1" targetRef="t1"/>
    <bpmn:sequenceFlow id="f3" sourceRef="t1" targetRef="e1"/>`)
}

// taskOf returns the imported t1 as the manual task it maps to.
func taskOf(t *testing.T, res *convert.Result) *activities.ManualTask {
	t.Helper()

	mt, ok := nodeByID(t, res, "t1").(*activities.ManualTask)
	if !ok {
		t.Fatalf("t1 is not a *activities.ManualTask")
	}

	return mt
}

// TestIOSpecificationImportsOntoATask is T-1 (FR-1): both directions,
// carried items, through the model's one door.
func TestIOSpecificationImportsOntoATask(t *testing.T) {
	res, err := importEventDoc(t, ioTaskDoc(
		`      <bpmn:ioSpecification id="io1">
        <bpmn:dataInput id="din1" name="in" itemSubjectRef="idOrder"/>
        <bpmn:dataOutput id="dout1" name="out" itemSubjectRef="idCount"/>
      </bpmn:ioSpecification>`))
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	mt := taskOf(t, res)

	ins, outs := mt.Inputs(), mt.Outputs()
	if len(ins) != 1 || ins[0].ItemDefinition().ID() != "idOrder" {
		t.Errorf("Inputs() = %v, want one over idOrder", ins)
	}

	if len(outs) != 1 || outs[0].ItemDefinition().ID() != "idCount" {
		t.Errorf("Outputs() = %v, want one over idCount", outs)
	}

	if len(res.Dropped) != 0 {
		t.Errorf("Dropped = %v, want nothing", res.Dropped)
	}
}

// processIODoc is the SRD-093 §3.6-D shape: a process declaring its
// contract in the leading-child slot, over one int item.
func processIODoc(ioSpec string) string {
	return propDoc(
		`  <bpmn:itemDefinition id="idInt" structureRef="xsd:int"/>`,
		ioSpec)
}

const processIOSpec = `    <bpmn:ioSpecification id="io">
      <bpmn:dataInput id="in-subtotal" name="subtotal" itemSubjectRef="idInt"/>
      <bpmn:dataInput id="in-discount" name="discount" itemSubjectRef="idInt"/>
      <bpmn:dataOutput id="out-total" name="total" itemSubjectRef="idInt"/>
      <bpmn:inputSet id="is">
        <bpmn:dataInputRefs>in-subtotal</bpmn:dataInputRefs>
        <bpmn:dataInputRefs>in-discount</bpmn:dataInputRefs>
        <bpmn:optionalInputRefs>in-discount</bpmn:optionalInputRefs>
      </bpmn:inputSet>
      <bpmn:outputSet id="os">
        <bpmn:dataOutputRefs>out-total</bpmn:dataOutputRefs>
      </bpmn:outputSet>
    </bpmn:ioSpecification>`

// TestIOSpecificationOnAProcess — SRD-093 T-15: the process's declared
// contract imports as its IOSpec — parameters, optionality from the set,
// items resolved by itemSubjectRef — and a process declaring none stays
// contract-less.
func TestIOSpecificationOnAProcess(t *testing.T) {
	res, err := importEventDoc(t, processIODoc(processIOSpec))
	if err != nil {
		t.Fatalf("import of a contracted process: %v", err)
	}

	ios := res.Processes[0].IOSpec()
	if ios == nil {
		t.Fatal("IOSpec() = nil, want the declared contract")
	}

	ins := ios.InputSet()
	if len(ins) != 2 || ins[0].Name() != "subtotal" ||
		ins[1].Name() != "discount" {
		t.Fatalf("InputSet() = %v, want subtotal, discount", ins)
	}

	if ins[0].IsOptional() || !ins[1].IsOptional() {
		t.Errorf("optionality = %v/%v, want subtotal required, discount "+
			"optional (from <optionalInputRefs>)",
			ins[0].IsOptional(), ins[1].IsOptional())
	}

	if ins[0].ItemDefinition().ID() != "idInt" {
		t.Errorf("subtotal item = %q, want idInt", ins[0].ItemDefinition().ID())
	}

	outs := ios.OutputSet()
	if len(outs) != 1 || outs[0].Name() != "total" || outs[0].IsOptional() {
		t.Errorf("OutputSet() = %v, want one required total", outs)
	}

	if len(res.Dropped) != 0 {
		t.Errorf("Dropped = %v, want nothing", res.Dropped)
	}

	plain, err := importEventDoc(t, propDoc("", ""))
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	if plain.Processes[0].IOSpec() != nil {
		t.Error("a process declaring no contract must be contract-less")
	}
}

// TestProcessIOSpecWithoutItems: a parameter naming no itemSubjectRef takes
// the empty item — a process has no association partner to adopt from.
func TestProcessIOSpecWithoutItems(t *testing.T) {
	res, err := importEventDoc(t, propDoc("",
		`    <bpmn:ioSpecification id="io">
      <bpmn:dataInput id="in-x" name="x"/>
    </bpmn:ioSpecification>`))
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	ins := res.Processes[0].IOSpec().InputSet()
	if len(ins) != 1 || ins[0].Name() != "x" {
		t.Fatalf("InputSet() = %v, want x", ins)
	}
}

// TestProcessEmptyIOSpecIsStrict — SRD-093 T-22: an explicit
// <ioSpecification> declaring nothing is a strict EMPTY contract (ADR-040
// §2.1 — no data required to start, none promised), not the contract-less
// process an absent element leaves.
func TestProcessEmptyIOSpecIsStrict(t *testing.T) {
	res, err := importEventDoc(t, propDoc("",
		`    <bpmn:ioSpecification id="io"/>`))
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	ios := res.Processes[0].IOSpec()
	if ios == nil {
		t.Fatal("IOSpec() = nil, want an empty contract")
	}

	if n := len(ios.InputSet()) + len(ios.OutputSet()); n != 0 {
		t.Fatalf("the empty contract declares %d parameters", n)
	}
}

// TestProcessIOSpecUnknownItemRefused — SRD-093 T-26: a process parameter
// naming an itemSubjectRef the document does not define refuses the
// import through constructProcess, naming the reference.
func TestProcessIOSpecUnknownItemRefused(t *testing.T) {
	_, err := importEventDoc(t, propDoc("",
		`    <bpmn:ioSpecification id="io">
      <bpmn:dataInput id="in-x" name="x" itemSubjectRef="nope"/>
    </bpmn:ioSpecification>`))
	if err == nil {
		t.Fatal("want a refusal, got a clean import")
	}

	if !strings.Contains(err.Error(), "nope") {
		t.Fatalf("refusal does not name the reference:\n%v", err)
	}
}

// TestProcessIOSpecOrdering — SRD-093 T-16: an <ioSpecification> after
// the flow elements is refused, the lane-set ordering guard.
func TestProcessIOSpecOrdering(t *testing.T) {
	doc := `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:process id="P" name="P">
    <bpmn:startEvent id="s1"/>
    <bpmn:ioSpecification id="io"/>
    <bpmn:endEvent id="e1"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s1" targetRef="e1"/>
  </bpmn:process>
</bpmn:definitions>`

	_, err := importEventDoc(t, doc)
	if err == nil {
		t.Fatal("an <ioSpecification> after the flow elements must be refused")
	}

	if !strings.Contains(err.Error(), "after its flow elements") {
		t.Errorf("err = %v, want the ordering refusal", err)
	}
}

// TestProcessSecondIOSpecRefused — SRD-093 T-17: the activity's refusals
// hold at process level — a second specification, and a second set.
func TestProcessSecondIOSpecRefused(t *testing.T) {
	_, err := importEventDoc(t, propDoc("",
		`    <bpmn:ioSpecification id="io1"/>
    <bpmn:ioSpecification id="io2"/>`))
	if err == nil || !strings.Contains(err.Error(), "second <ioSpecification>") {
		t.Errorf("err = %v, want the second-specification refusal", err)
	}

	_, err = importEventDoc(t, propDoc("",
		`    <bpmn:ioSpecification id="io">
      <bpmn:inputSet id="is1"/>
      <bpmn:inputSet id="is2"/>
    </bpmn:ioSpecification>`))
	if err == nil || !strings.Contains(err.Error(), "ONE set per direction") {
		t.Errorf("err = %v, want the single-set refusal", err)
	}
}

// TestProcessBareDataInputRefused — SRD-093 T-18: a bare <dataInput>
// under <process> stays refused, its note naming the process beside the
// task as an owner; nothing names #330 any more.
func TestProcessBareDataInputRefused(t *testing.T) {
	_, err := importEventDoc(t, propDoc("",
		`    <bpmn:dataInput id="in-x" name="x"/>`))
	if err == nil {
		t.Fatal("a bare <dataInput> under <process> must be refused")
	}

	for _, want := range []string{"on a task or a process", "<ioSpecification>"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want it to say %q", err, want)
		}
	}

	if strings.Contains(err.Error(), "#330") {
		t.Error("the refusal still names #330 — the capability landed")
	}
}

// TestParameterFlagsSurvive is T-4's build half: the set's ref lists
// arrive on the model's parameters.
func TestParameterFlagsSurvive(t *testing.T) {
	res, err := importEventDoc(t, ioTaskDoc(
		`      <bpmn:ioSpecification id="io1">
        <bpmn:dataInput id="din1" name="in" itemSubjectRef="idOrder"/>
        <bpmn:dataInput id="din2" name="mid" itemSubjectRef="idCount"/>
        <bpmn:inputSet id="is1">
          <bpmn:dataInputRefs>din1</bpmn:dataInputRefs>
          <bpmn:dataInputRefs>din2</bpmn:dataInputRefs>
          <bpmn:optionalInputRefs>din1</bpmn:optionalInputRefs>
          <bpmn:whileExecutingInputRefs>din2</bpmn:whileExecutingInputRefs>
        </bpmn:inputSet>
      </bpmn:ioSpecification>`))
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	params := taskOf(t, res).IoSpec.InputSet()
	if len(params) != 2 || !params[0].IsOptional() ||
		!params[1].IsWhileExecuting() {
		t.Errorf("InputSet() = %v, want optional din1 and whileExecuting din2",
			params)
	}
}

// TestUnnamedParameterTakesItsID is T-2: fallbackName serves parameters
// as it serves every named element.
func TestUnnamedParameterTakesItsID(t *testing.T) {
	res, err := importEventDoc(t, ioTaskDoc(
		`      <bpmn:ioSpecification id="io1">
        <bpmn:dataInput id="din1" itemSubjectRef="idOrder"/>
      </bpmn:ioSpecification>`))
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	params := taskOf(t, res).IoSpec.InputSet()
	if len(params) != 1 || params[0].Name() != "din1" {
		t.Errorf("InputSet() = %v, want the id as the name", params)
	}
}

// TestEmptyIOSpecificationImports is T-3: an empty spec means the
// activity requires no data, and imports as exactly that.
func TestEmptyIOSpecificationImports(t *testing.T) {
	res, err := importEventDoc(t, ioTaskDoc(
		`      <bpmn:ioSpecification id="io1">
        <bpmn:inputSet id="is1"/>
        <bpmn:outputSet id="os1"/>
      </bpmn:ioSpecification>`))
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	mt := taskOf(t, res)
	if len(mt.Inputs()) != 0 || len(mt.Outputs()) != 0 {
		t.Errorf("params = %d/%d, want none", len(mt.Inputs()), len(mt.Outputs()))
	}
}

// TestDuplicateItemParametersRefused is T-27 (§4.3a): the model
// addresses a direction's parameters by item id, so a duplicate is one
// parameter declared twice.
func TestDuplicateItemParametersRefused(t *testing.T) {
	_, err := importEventDoc(t, ioTaskDoc(
		`      <bpmn:ioSpecification id="io1">
        <bpmn:dataInput id="din1" name="a" itemSubjectRef="idOrder"/>
        <bpmn:dataInput id="din2" name="b" itemSubjectRef="idOrder"/>
      </bpmn:ioSpecification>`))
	if err == nil || !strings.Contains(err.Error(), "one parameter declared twice") {
		t.Fatalf("error = %v, want the §4.3a refusal", err)
	}
}

// TestSecondIOSpecificationRefused: an activity has at most one
// (§10.4.1 Table 10.58).
func TestSecondIOSpecificationRefused(t *testing.T) {
	_, err := importEventDoc(t, ioTaskDoc(
		`      <bpmn:ioSpecification id="io1"/>
      <bpmn:ioSpecification id="io2"/>`))
	if err == nil || !strings.Contains(err.Error(), "second <ioSpecification>") {
		t.Fatalf("error = %v, want the 0..1 refusal", err)
	}
}

// TestIOSpecificationMisplaced is T-29 (§4.7a): the owners the standard
// itself refuses, each with the § a modeler can read.
func TestIOSpecificationMisplaced(t *testing.T) {
	const io = `<bpmn:ioSpecification id="io1"/>`

	tests := map[string]string{
		"sub-process": subProcessDoc(innerGraph + `
      ` + io),
		"call activity": propDoc("",
			`    <bpmn:callActivity id="ca1" calledElement="P2">`+io+`</bpmn:callActivity>`),
		"gateway": propDoc("",
			`    <bpmn:exclusiveGateway id="g1">`+io+`</bpmn:exclusiveGateway>`),
		"event": propDoc("",
			`    <bpmn:startEvent id="se1">`+io+`</bpmn:startEvent>`),
	}

	for name, doc := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := importEventDoc(t, doc)
			if err == nil {
				t.Fatal("an ioSpecification outside a task must be refused")
			}

			for _, want := range []string{"§10.4.1", "gives only to tasks"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error = %v, want it to carry %q", err, want)
				}
			}
		})
	}
}

// TestUserTaskWithIOSpecification is T-21 (§4.5): real parameters, no
// WithoutParams to discard them, the renderer placeholder intact.
func TestUserTaskWithIOSpecification(t *testing.T) {
	res, err := importEventDoc(t, propDoc(
		`  <bpmn:itemDefinition id="idOrder" structureRef="xsd:string"/>`,
		`    <bpmn:userTask id="u1" name="Check">
      <bpmn:ioSpecification id="io1">
        <bpmn:dataInput id="din1" name="in" itemSubjectRef="idOrder"/>
      </bpmn:ioSpecification>
    </bpmn:userTask>
    <bpmn:sequenceFlow id="f2" sourceRef="s1" targetRef="u1"/>
    <bpmn:sequenceFlow id="f3" sourceRef="u1" targetRef="e1"/>`))
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	ut, ok := nodeByID(t, res, "u1").(*activities.UserTask)
	if !ok {
		t.Fatalf("u1 is not a *activities.UserTask")
	}

	ins := ut.Inputs()
	if len(ins) != 1 || ins[0].ItemDefinition().ID() != "idOrder" {
		t.Errorf("Inputs() = %v, want the declared parameter — WithoutParams "+
			"would have discarded it", ins)
	}
}

// TestUserTaskWithoutIOSpecification is T-22: today's synthesized pair,
// unchanged for the files that declare nothing.
func TestUserTaskWithoutIOSpecification(t *testing.T) {
	res, err := importEventDoc(t, propDoc("",
		`    <bpmn:userTask id="u1" name="Check"/>
    <bpmn:sequenceFlow id="f2" sourceRef="s1" targetRef="u1"/>
    <bpmn:sequenceFlow id="f3" sourceRef="u1" targetRef="e1"/>`))
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	ut, ok := nodeByID(t, res, "u1").(*activities.UserTask)
	if !ok {
		t.Fatalf("u1 is not a *activities.UserTask")
	}

	// The synthesized pair: an explicitly empty input side, and the one
	// placeholder renderer output the model demands.
	if len(ut.Inputs()) != 0 || len(ut.Outputs()) != 1 {
		t.Errorf("params = %d/%d, want 0 inputs and the placeholder output",
			len(ut.Inputs()), len(ut.Outputs()))
	}
}

// TestParameterItemRefErrors: itemFor's refusals reach a parameter with
// the parameter named as the referrer.
func TestParameterItemRefErrors(t *testing.T) {
	_, err := importEventDoc(t, ioTaskDoc(
		`      <bpmn:ioSpecification id="io1">
        <bpmn:dataInput id="din1" name="in" itemSubjectRef="ghost"/>
      </bpmn:ioSpecification>`))
	if err == nil || !strings.Contains(err.Error(), "no such element is declared") {
		t.Fatalf("error = %v, want the dangling-item refusal", err)
	}

	if !strings.Contains(err.Error(), `dataInput "din1"`) {
		t.Errorf("error = %v, want the parameter named as the referrer", err)
	}
}

// TestReservedCharacterParameterName: the model's name rule reaches a
// parameter through NewParameter's own CheckName, with the file's id
// wrapped around it (NFR-4).
func TestReservedCharacterParameterName(t *testing.T) {
	_, err := importEventDoc(t, ioTaskDoc(
		`      <bpmn:ioSpecification id="io1">
        <bpmn:dataInput id="din1" name="in.v2" itemSubjectRef="idOrder"/>
      </bpmn:ioSpecification>`))
	if err == nil {
		t.Fatal("a parameter name with a reserved character must be refused")
	}

	for _, want := range []string{`"din1"`, "reserved character"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v, want it to carry %q", err, want)
		}
	}
}

// TestBrokenIOSpecificationInsideATask: the parse error surfaces through
// the node-child registration, not only the direct call.
func TestBrokenIOSpecificationInsideATask(t *testing.T) {
	_, err := importEventDoc(t, ioTaskDoc(
		`      <bpmn:ioSpecification/>`))
	if err == nil || !strings.Contains(err.Error(), "has no id") {
		t.Fatalf("error = %v, want the missing-id refusal", err)
	}
}

// TestParameterDocumentation: a parameter's documentation lands on its
// ItemAwareElement — the one place the model can hold it (NFR-1).
func TestParameterDocumentation(t *testing.T) {
	res, err := importEventDoc(t, ioTaskDoc(
		`      <bpmn:ioSpecification id="io1">
        <bpmn:dataInput id="din1" name="in" itemSubjectRef="idOrder">
          <bpmn:documentation>the order under review</bpmn:documentation>
        </bpmn:dataInput>
      </bpmn:ioSpecification>`))
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	docs := taskOf(t, res).IoSpec.InputSet()[0].Docs()
	if len(docs) != 1 || docs[0].Text() != "the order under review" {
		t.Errorf("Docs() = %v, want the file's one line", docs)
	}
}

// TestOutputSetFlags covers the output direction's two ref lists — the
// map rows the input tests cannot reach.
func TestOutputSetFlags(t *testing.T) {
	io, _, err := ioFrag(t,
		`<bpmn:dataOutput id="dout1" name="a"/>
		 <bpmn:dataOutput id="dout2" name="b"/>
		 <bpmn:outputSet id="os1">
		   <bpmn:dataOutputRefs>dout1</bpmn:dataOutputRefs>
		   <bpmn:optionalOutputRefs>dout1</bpmn:optionalOutputRefs>
		   <bpmn:whileExecutingOutputRefs>dout2</bpmn:whileExecutingOutputRefs>
		 </bpmn:outputSet>`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	if a := io.param("dout1"); !a.optional {
		t.Errorf("dout1 = %+v, want optional", a)
	}

	if b := io.param("dout2"); !b.whileExecuting {
		t.Errorf("dout2 = %+v, want whileExecuting", b)
	}
}

// TestIOSpecEdgeRefusals closes the remaining parse error paths.
func TestIOSpecEdgeRefusals(t *testing.T) {
	t.Run("spec without id", func(t *testing.T) {
		p := newParser(context.Background(), strings.NewReader(
			`<bpmn:ioSpecification xmlns:bpmn="`+nsBPMN+`"/>`))

		se, err := p.rootElement2()
		if err != nil {
			t.Fatalf("rootElement2: %v", err)
		}

		if _, err := p.parseIOSpecification(se); err == nil ||
			!strings.Contains(err.Error(), "has no id") {
			t.Fatalf("error = %v, want the missing-id refusal", err)
		}
	})

	t.Run("spec id already declared", func(t *testing.T) {
		p := newParser(context.Background(), strings.NewReader(
			`<bpmn:ioSpecification xmlns:bpmn="`+nsBPMN+`" id="io1"/>`))

		p.ids["io1"] = "task"

		se, err := p.rootElement2()
		if err != nil {
			t.Fatalf("rootElement2: %v", err)
		}

		if _, err := p.parseIOSpecification(se); err == nil ||
			!strings.Contains(err.Error(), "duplicate id") {
			t.Fatalf("error = %v, want the ledger's refusal", err)
		}
	})

	t.Run("set id already declared", func(t *testing.T) {
		_, _, err := ioFrag(t,
			`<bpmn:dataInput id="dup" name="a"/>
			 <bpmn:inputSet id="dup"/>`)
		if err == nil || !strings.Contains(err.Error(), "duplicate id") {
			t.Fatalf("error = %v, want the ledger's refusal", err)
		}
	})

	t.Run("truncated inside a parameter", func(t *testing.T) {
		p := newParser(context.Background(), strings.NewReader(
			`<bpmn:ioSpecification xmlns:bpmn="`+nsBPMN+`" id="io1">`+
				`<bpmn:dataInput id="din1">`))

		se, err := p.rootElement2()
		if err != nil {
			t.Fatalf("rootElement2: %v", err)
		}

		if _, err := p.parseIOSpecification(se); err == nil {
			t.Fatal("a document ending inside a parameter must fail")
		}
	})

	t.Run("foreign child of the spec is skipped", func(t *testing.T) {
		io, _, err := ioFrag(t,
			`<camunda:x xmlns:camunda="http://camunda.org/schema/1.0/bpmn"/>
			 <bpmn:dataInput id="din1" name="a"/>`)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}

		if len(io.params) != 1 {
			t.Errorf("params = %d, want the one after the foreign child",
				len(io.params))
		}
	})

	t.Run("foreign child of a set is skipped", func(t *testing.T) {
		_, _, err := ioFrag(t,
			`<bpmn:inputSet id="is1">
			   <camunda:x xmlns:camunda="http://camunda.org/schema/1.0/bpmn"/>
			 </bpmn:inputSet>`)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
	})

	t.Run("stranger inside a set", func(t *testing.T) {
		_, _, err := ioFrag(t,
			`<bpmn:inputSet id="is1"><bpmn:task id="t1"/></bpmn:inputSet>`)
		if err == nil || !strings.Contains(err.Error(), `unsupported element "task"`) {
			t.Fatalf("error = %v, want the stranger refused", err)
		}
	})
}

// TestDataAssocEdgeRefusals closes the association parser's remaining
// error paths.
func TestDataAssocEdgeRefusals(t *testing.T) {
	t.Run("id already declared", func(t *testing.T) {
		p := newParser(context.Background(), strings.NewReader(
			`<bpmn:dataInputAssociation xmlns:bpmn="`+nsBPMN+`" id="a1"/>`))

		p.ids["a1"] = "task"

		se, err := p.rootElement2()
		if err != nil {
			t.Fatalf("rootElement2: %v", err)
		}

		if _, err := p.parseDataAssociation(data.Input, se); err == nil ||
			!strings.Contains(err.Error(), "duplicate id") {
			t.Fatalf("error = %v, want the ledger's refusal", err)
		}
	})

	t.Run("foreign child skipped", func(t *testing.T) {
		spec, err := assocFrag(t, data.Input, tagDataInputAssoc,
			`<camunda:x xmlns:camunda="http://camunda.org/schema/1.0/bpmn"/>
			 <bpmn:sourceRef>do1</bpmn:sourceRef>
			 <bpmn:targetRef>din1</bpmn:targetRef>`)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}

		if spec.elemRef != "do1" {
			t.Errorf("elemRef = %q, want do1", spec.elemRef)
		}
	})

	for name, frag := range map[string]string{
		"truncated sourceRef":     `<bpmn:sourceRef>half`,
		"truncated targetRef":     `<bpmn:targetRef>half`,
		"truncated documentation": `<bpmn:documentation>half`,
	} {
		t.Run(name, func(t *testing.T) {
			p := newParser(context.Background(), strings.NewReader(
				`<bpmn:dataInputAssociation xmlns:bpmn="`+nsBPMN+`" id="a1">`+frag))

			se, err := p.rootElement2()
			if err != nil {
				t.Fatalf("rootElement2: %v", err)
			}

			if _, err := p.parseDataAssociation(data.Input, se); err == nil {
				t.Fatal("a truncated association must fail")
			}
		})
	}
}
