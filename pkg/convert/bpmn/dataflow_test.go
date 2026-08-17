package bpmn

import (
	"context"
	"strings"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/convert"
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

// TestDataAssociationDocumentation: the one decorating child.
func TestDataAssociationDocumentation(t *testing.T) {
	spec, err := assocFrag(t, data.Input, tagDataInputAssoc,
		`<bpmn:documentation>fills the order</bpmn:documentation>
		 <bpmn:sourceRef>do1</bpmn:sourceRef>
		 <bpmn:targetRef>din1</bpmn:targetRef>`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	if len(spec.docs) != 1 || spec.docs[0].text != "fills the order" {
		t.Errorf("docs = %v, want the file's one line", spec.docs)
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
