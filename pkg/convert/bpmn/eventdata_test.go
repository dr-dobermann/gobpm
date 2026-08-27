package bpmn

import (
	"strings"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/events"
)

// eventDataDecls declares a string item and a message over it — the
// item-bearing definition an event's data pairs with.
const eventDataDecls = `  <bpmn:itemDefinition id="idStr" structureRef="xsd:string"/>
  <bpmn:message id="m1" name="order placed" itemRef="idStr"/>`

// eventDataDoc builds a document around body: the declarations above, a
// typed data object, and the process's s1/e1 graph propDoc adds.
func eventDataDoc(body string) string {
	return propDoc(eventDataDecls,
		`    <bpmn:dataObject id="do1" name="order" itemSubjectRef="idStr"/>
`+body)
}

// startOf / endOf / catchOf / throwOf fetch a built event by id.
func startOf(t *testing.T, doc, id string) *events.StartEvent {
	t.Helper()

	res, err := importEventDoc(t, doc)
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	s, ok := nodeByID(t, res, id).(*events.StartEvent)
	if !ok {
		t.Fatalf("%q is not a start event", id)
	}

	return s
}

// TestImportEventAssociations — SRD-094 T-13: a data object end on a
// start, an intermediate catch, an end and an intermediate throw imports,
// and the association is bound on the event.
func TestImportEventAssociations(t *testing.T) {
	t.Run("a start's output association into a data object", func(t *testing.T) {
		s := startOf(t, eventDataDoc(`    <bpmn:startEvent id="s2">
      <bpmn:messageEventDefinition messageRef="m1"/>
      <bpmn:dataOutput id="s2-out" itemSubjectRef="idStr"/>
      <bpmn:dataOutputAssociation id="oa1">
        <bpmn:sourceRef>s2-out</bpmn:sourceRef>
        <bpmn:targetRef>do1</bpmn:targetRef>
      </bpmn:dataOutputAssociation>
    </bpmn:startEvent>
    <bpmn:sequenceFlow id="f2" sourceRef="s2" targetRef="e1"/>`), "s2")

		aa := s.OutputAssociations()
		if len(aa) != 1 || aa[0].TargetName() != "order" {
			t.Fatalf("OutputAssociations() = %v, want one onto \"order\"", aa)
		}
	})

	t.Run("an intermediate catch's output and a throw's input", func(t *testing.T) {
		res, err := importEventDoc(t, eventDataDoc(`    <bpmn:intermediateCatchEvent id="c1">
      <bpmn:messageEventDefinition messageRef="m1"/>
      <bpmn:dataOutput id="c1-out" itemSubjectRef="idStr"/>
      <bpmn:dataOutputAssociation id="oa1">
        <bpmn:sourceRef>c1-out</bpmn:sourceRef>
        <bpmn:targetRef>do1</bpmn:targetRef>
      </bpmn:dataOutputAssociation>
    </bpmn:intermediateCatchEvent>
    <bpmn:intermediateThrowEvent id="t1">
      <bpmn:messageEventDefinition messageRef="m1"/>
      <bpmn:dataInput id="t1-in" itemSubjectRef="idStr"/>
      <bpmn:dataInputAssociation id="ia1">
        <bpmn:sourceRef>do1</bpmn:sourceRef>
        <bpmn:targetRef>t1-in</bpmn:targetRef>
      </bpmn:dataInputAssociation>
    </bpmn:intermediateThrowEvent>
    <bpmn:sequenceFlow id="f2" sourceRef="s1" targetRef="c1"/>
    <bpmn:sequenceFlow id="f3" sourceRef="c1" targetRef="t1"/>
    <bpmn:sequenceFlow id="f4" sourceRef="t1" targetRef="e1"/>`))
		if err != nil {
			t.Fatalf("import: %v", err)
		}

		c, _ := nodeByID(t, res, "c1").(*events.IntermediateCatchEvent)
		if c == nil || len(c.OutputAssociations()) != 1 {
			t.Fatalf("the catch carries no output association")
		}

		th, _ := nodeByID(t, res, "t1").(*events.IntermediateThrowEvent)
		if th == nil || len(th.InputAssociations()) != 1 ||
			th.InputAssociations()[0].SourceNames()[0] != "order" {
			t.Fatalf("the throw carries no input association from \"order\"")
		}
	})

	t.Run("an end's input association from a data object", func(t *testing.T) {
		res, err := importEventDoc(t, eventDataDoc(`    <bpmn:endEvent id="e2">
      <bpmn:messageEventDefinition messageRef="m1"/>
      <bpmn:dataInput id="e2-in" itemSubjectRef="idStr"/>
      <bpmn:dataInputAssociation id="ia1">
        <bpmn:sourceRef>do1</bpmn:sourceRef>
        <bpmn:targetRef>e2-in</bpmn:targetRef>
      </bpmn:dataInputAssociation>
    </bpmn:endEvent>
    <bpmn:sequenceFlow id="f2" sourceRef="s1" targetRef="e2"/>`))
		if err != nil {
			t.Fatalf("import: %v", err)
		}

		e, _ := nodeByID(t, res, "e2").(*events.EndEvent)
		if e == nil || len(e.InputAssociations()) != 1 {
			t.Fatalf("the end carries no input association")
		}
	})
}

// processEndsDoc is the Start/End special case: the process declares
// "order" in and "total" out; the start's output association targets the
// input, the end's input association sources the output.
func processEndsDoc(startRef, endRef string) string {
	return propDoc(eventDataDecls,
		`    <bpmn:ioSpecification id="io">
      <bpmn:dataInput id="p-in" name="order" itemSubjectRef="idStr"/>
      <bpmn:dataOutput id="p-out" name="total" itemSubjectRef="idStr"/>
    </bpmn:ioSpecification>
    <bpmn:startEvent id="s2">
      <bpmn:messageEventDefinition messageRef="m1"/>
      <bpmn:dataOutput id="s2-out" itemSubjectRef="idStr"/>
      <bpmn:dataOutputAssociation id="oa1">
        <bpmn:sourceRef>s2-out</bpmn:sourceRef>
        <bpmn:targetRef>`+startRef+`</bpmn:targetRef>
      </bpmn:dataOutputAssociation>
    </bpmn:startEvent>
    <bpmn:endEvent id="e2">
      <bpmn:messageEventDefinition messageRef="m1"/>
      <bpmn:dataInput id="e2-in" itemSubjectRef="idStr"/>
      <bpmn:dataInputAssociation id="ia1">
        <bpmn:sourceRef>`+endRef+`</bpmn:sourceRef>
        <bpmn:targetRef>e2-in</bpmn:targetRef>
      </bpmn:dataInputAssociation>
    </bpmn:endEvent>
    <bpmn:sequenceFlow id="f2" sourceRef="s2" targetRef="e2"/>`)
}

// TestImportStartToProcessInput — SRD-094 T-14: the process's own
// parameters as association ends wire through the process; a catch that
// is not the Start Event is refused naming the two positions.
func TestImportStartToProcessInput(t *testing.T) {
	t.Run("the start fills the input, the end reads the output", func(t *testing.T) {
		res, err := importEventDoc(t, processEndsDoc("p-in", "p-out"))
		if err != nil {
			t.Fatalf("import: %v", err)
		}

		s, _ := nodeByID(t, res, "s2").(*events.StartEvent)
		if s == nil || len(s.OutputAssociations()) != 1 ||
			s.OutputAssociations()[0].TargetName() != "order" {
			t.Fatalf("the start's association does not target the input \"order\"")
		}

		e, _ := nodeByID(t, res, "e2").(*events.EndEvent)
		if e == nil || len(e.InputAssociations()) != 1 ||
			e.InputAssociations()[0].SourceNames()[0] != "total" {
			t.Fatalf("the end's association does not source the output \"total\"")
		}
	})

	t.Run("a data object end beside a contract still wires as a data object",
		func(t *testing.T) {
			res, err := importEventDoc(t, propDoc(eventDataDecls,
				`    <bpmn:ioSpecification id="io">
      <bpmn:dataInput id="p-in" name="order" itemSubjectRef="idStr"/>
    </bpmn:ioSpecification>
    <bpmn:dataObject id="do1" name="order-copy" itemSubjectRef="idStr"/>
    <bpmn:startEvent id="s2">
      <bpmn:messageEventDefinition messageRef="m1"/>
      <bpmn:dataOutput id="s2-out" itemSubjectRef="idStr"/>
      <bpmn:dataOutputAssociation id="oa1">
        <bpmn:sourceRef>s2-out</bpmn:sourceRef>
        <bpmn:targetRef>do1</bpmn:targetRef>
      </bpmn:dataOutputAssociation>
    </bpmn:startEvent>
    <bpmn:sequenceFlow id="f2" sourceRef="s2" targetRef="e1"/>`))
			if err != nil {
				t.Fatalf("import: %v", err)
			}

			s, _ := nodeByID(t, res, "s2").(*events.StartEvent)
			if s == nil || s.OutputAssociations()[0].TargetName() != "order-copy" {
				t.Fatalf("the start's association does not target the data object")
			}
		})

	t.Run("a process output as a start's target is refused", func(t *testing.T) {
		_, err := importEventDoc(t, processEndsDoc("p-out", "p-out"))
		if err == nil || !strings.Contains(err.Error(), "Start Event's output associations") {
			t.Fatalf("error = %v, want the two-positions refusal", err)
		}
	})

	t.Run("a catch that is not the start is refused", func(t *testing.T) {
		_, err := importEventDoc(t, propDoc(eventDataDecls,
			`    <bpmn:ioSpecification id="io">
      <bpmn:dataInput id="p-in" name="order" itemSubjectRef="idStr"/>
    </bpmn:ioSpecification>
    <bpmn:intermediateCatchEvent id="c1">
      <bpmn:messageEventDefinition messageRef="m1"/>
      <bpmn:dataOutput id="c1-out" itemSubjectRef="idStr"/>
      <bpmn:dataOutputAssociation id="oa1">
        <bpmn:sourceRef>c1-out</bpmn:sourceRef>
        <bpmn:targetRef>p-in</bpmn:targetRef>
      </bpmn:dataOutputAssociation>
    </bpmn:intermediateCatchEvent>
    <bpmn:sequenceFlow id="f2" sourceRef="s1" targetRef="c1"/>
    <bpmn:sequenceFlow id="f3" sourceRef="c1" targetRef="e1"/>`))
		if err == nil || !strings.Contains(err.Error(), "and to nothing else") {
			t.Fatalf("error = %v, want the two-positions refusal", err)
		}
	})

	t.Run("an item mismatch with the process parameter is refused", func(t *testing.T) {
		_, err := importEventDoc(t, propDoc(eventDataDecls+`
  <bpmn:itemDefinition id="idInt" structureRef="xsd:int"/>`,
			`    <bpmn:ioSpecification id="io">
      <bpmn:dataInput id="p-in" name="order" itemSubjectRef="idInt"/>
    </bpmn:ioSpecification>
    <bpmn:startEvent id="s2">
      <bpmn:messageEventDefinition messageRef="m1"/>
      <bpmn:dataOutput id="s2-out" itemSubjectRef="idStr"/>
      <bpmn:dataOutputAssociation id="oa1">
        <bpmn:sourceRef>s2-out</bpmn:sourceRef>
        <bpmn:targetRef>p-in</bpmn:targetRef>
      </bpmn:dataOutputAssociation>
    </bpmn:startEvent>
    <bpmn:sequenceFlow id="f2" sourceRef="s2" targetRef="e1"/>`))
		if err == nil || !strings.Contains(err.Error(), "itemDefinitions match") {
			t.Fatalf("error = %v, want the item-match refusal", err)
		}
	})
}

// TestImportEventIOPairing — SRD-094 T-15's order and identity rules: two
// item-bearing definitions on one event pair with two bare parameters by
// position; a bare parameter's itemSubjectRef must be the one its
// definition's element named; a duplicate parameter id is refused by the
// document's id ledger.
func TestImportEventIOPairing(t *testing.T) {
	decls := eventDataDecls + `
  <bpmn:itemDefinition id="idInt" structureRef="xsd:int"/>
  <bpmn:message id="m2" name="order paid" itemRef="idInt"/>`

	t.Run("two definitions, two parameters, in order", func(t *testing.T) {
		res, err := importEventDoc(t, propDoc(decls, `    <bpmn:startEvent id="s2">
      <bpmn:messageEventDefinition messageRef="m1"/>
      <bpmn:messageEventDefinition messageRef="m2"/>
      <bpmn:dataOutput id="o-placed"/>
      <bpmn:dataOutput id="o-paid"/>
    </bpmn:startEvent>
    <bpmn:sequenceFlow id="f2" sourceRef="s2" targetRef="e1"/>`))
		if err != nil {
			t.Fatalf("import: %v", err)
		}

		s, _ := nodeByID(t, res, "s2").(*events.StartEvent)
		outs := s.Outputs()
		defs := s.Definitions()

		if len(outs) != 2 || outs[0].ID() != "o-placed" || outs[1].ID() != "o-paid" ||
			outs[0].ItemDefinition().ID() != defs[0].GetItemsList()[0].ID() ||
			outs[1].ItemDefinition().ID() != defs[1].GetItemsList()[0].ID() {
			t.Fatalf("the parameters did not pair by position: %v", outs)
		}
	})

	t.Run("an itemSubjectRef other than the definition's is refused",
		func(t *testing.T) {
			_, err := importEventDoc(t, propDoc(decls, `    <bpmn:startEvent id="s2">
      <bpmn:messageEventDefinition messageRef="m1"/>
      <bpmn:dataOutput id="o-placed" itemSubjectRef="idInt"/>
    </bpmn:startEvent>
    <bpmn:sequenceFlow id="f2" sourceRef="s2" targetRef="e1"/>`))
			if err == nil || !strings.Contains(err.Error(), "makes them the same itemDefinition") {
				t.Fatalf("error = %v, want the p217 refusal", err)
			}
		})

	t.Run("a duplicate parameter id is refused", func(t *testing.T) {
		_, err := importEventDoc(t, eventDataDoc(`    <bpmn:startEvent id="s2">
      <bpmn:messageEventDefinition messageRef="m1"/>
      <bpmn:dataOutput id="do1"/>
    </bpmn:startEvent>
    <bpmn:sequenceFlow id="f2" sourceRef="s2" targetRef="e1"/>`))
		if err == nil || !strings.Contains(err.Error(), "do1") {
			t.Fatalf("error = %v, want the duplicate-id refusal", err)
		}
	})

	t.Run("a data store reference wires into an end's input by id", func(t *testing.T) {
		res, err := importEventDoc(t, propDoc(eventDataDecls+`
  <bpmn:dataStore id="S" name="orders"/>`,
			`    <bpmn:dataStoreReference id="dsr1" name="orders" dataStoreRef="S" itemSubjectRef="idStr"/>
    <bpmn:endEvent id="e2">
      <bpmn:messageEventDefinition messageRef="m1"/>
      <bpmn:dataInput id="e2-in" itemSubjectRef="idStr"/>
      <bpmn:dataInputAssociation id="ia1">
        <bpmn:sourceRef>dsr1</bpmn:sourceRef>
        <bpmn:targetRef>e2-in</bpmn:targetRef>
      </bpmn:dataInputAssociation>
    </bpmn:endEvent>
    <bpmn:sequenceFlow id="f2" sourceRef="s1" targetRef="e2"/>`))
		if err != nil {
			t.Fatalf("import: %v", err)
		}

		e, _ := nodeByID(t, res, "e2").(*events.EndEvent)
		if e == nil || len(e.InputAssociations()) != 1 ||
			e.InputAssociations()[0].DataStoreRef() != "S" {
			t.Fatalf("the end's association does not carry the store ref")
		}
	})
}

// TestImportBareEventIO — SRD-094 T-15: a bare parameter on an event
// declares its data — adopting the definition's item when it names none;
// the wrong direction, a parameter pairing with no definition, and a
// bare parameter on a task are refused.
func TestImportBareEventIO(t *testing.T) {
	t.Run("with and without itemSubjectRef", func(t *testing.T) {
		res, err := importEventDoc(t, eventDataDoc(`    <bpmn:startEvent id="s2">
      <bpmn:messageEventDefinition messageRef="m1"/>
      <bpmn:dataOutput id="s2-out"/>
    </bpmn:startEvent>
    <bpmn:endEvent id="e2">
      <bpmn:messageEventDefinition messageRef="m1"/>
      <bpmn:dataInput id="e2-in" itemSubjectRef="idStr"/>
    </bpmn:endEvent>
    <bpmn:sequenceFlow id="f2" sourceRef="s2" targetRef="e2"/>`))
		if err != nil {
			t.Fatalf("import: %v", err)
		}

		// the parameter carries the file's id and the DEFINITION's item —
		// the converter's placeholder for the message payload, which is
		// what the engine binds a delivery by
		s, _ := nodeByID(t, res, "s2").(*events.StartEvent)
		if s == nil || len(s.Outputs()) != 1 || s.Outputs()[0].ID() != "s2-out" ||
			s.Outputs()[0].ItemDefinition().ID() !=
				s.Definitions()[0].GetItemsList()[0].ID() {
			t.Fatalf("the start's bare output did not adopt the message's item")
		}

		e, _ := nodeByID(t, res, "e2").(*events.EndEvent)
		if e == nil || len(e.Inputs()) != 1 || e.Inputs()[0].ID() != "e2-in" {
			t.Fatalf("the end's bare input was not declared")
		}
	})

	refusals := map[string]struct{ body, want string }{
		"a dataInput on a catch": {
			body: `    <bpmn:startEvent id="s2">
      <bpmn:messageEventDefinition messageRef="m1"/>
      <bpmn:dataInput id="s2-in" itemSubjectRef="idStr"/>
    </bpmn:startEvent>
    <bpmn:sequenceFlow id="f2" sourceRef="s2" targetRef="e1"/>`,
			want: "data outputs only",
		},
		"an input association on a catch": {
			body: `    <bpmn:startEvent id="s2">
      <bpmn:messageEventDefinition messageRef="m1"/>
      <bpmn:dataInputAssociation id="ia1">
        <bpmn:sourceRef>do1</bpmn:sourceRef>
        <bpmn:targetRef>x</bpmn:targetRef>
      </bpmn:dataInputAssociation>
    </bpmn:startEvent>
    <bpmn:sequenceFlow id="f2" sourceRef="s2" targetRef="e1"/>`,
			want: "output associations only",
		},
		"a parameter pairing with no definition": {
			body: `    <bpmn:endEvent id="e2">
      <bpmn:dataInput id="e2-in"/>
    </bpmn:endEvent>
    <bpmn:sequenceFlow id="f2" sourceRef="s1" targetRef="e2"/>`,
			want: "no item-bearing definition",
		},
		"a dataInput on an intermediate catch": {
			body: `    <bpmn:intermediateCatchEvent id="c1">
      <bpmn:messageEventDefinition messageRef="m1"/>
      <bpmn:dataInput id="c1-in" itemSubjectRef="idStr"/>
    </bpmn:intermediateCatchEvent>
    <bpmn:sequenceFlow id="f2" sourceRef="s1" targetRef="c1"/>
    <bpmn:sequenceFlow id="f3" sourceRef="c1" targetRef="e1"/>`,
			want: "data outputs only",
		},
		"a dataOutput on an intermediate throw": {
			body: `    <bpmn:intermediateThrowEvent id="t1">
      <bpmn:messageEventDefinition messageRef="m1"/>
      <bpmn:dataOutput id="t1-out" itemSubjectRef="idStr"/>
    </bpmn:intermediateThrowEvent>
    <bpmn:sequenceFlow id="f2" sourceRef="s1" targetRef="t1"/>
    <bpmn:sequenceFlow id="f3" sourceRef="t1" targetRef="e1"/>`,
			want: "data inputs only",
		},
		"a dataInput on a boundary": {
			body: `    <bpmn:task id="t2" name="T"/>
    <bpmn:boundaryEvent id="b1" attachedToRef="t2">
      <bpmn:messageEventDefinition messageRef="m1"/>
      <bpmn:dataInput id="b1-in" itemSubjectRef="idStr"/>
    </bpmn:boundaryEvent>
    <bpmn:sequenceFlow id="f2" sourceRef="s1" targetRef="t2"/>
    <bpmn:sequenceFlow id="f3" sourceRef="t2" targetRef="e1"/>
    <bpmn:sequenceFlow id="f4" sourceRef="b1" targetRef="e1"/>`,
			want: "data outputs only",
		},
		"a parameter without an id": {
			body: `    <bpmn:endEvent id="e2">
      <bpmn:messageEventDefinition messageRef="m1"/>
      <bpmn:dataInput/>
    </bpmn:endEvent>
    <bpmn:sequenceFlow id="f2" sourceRef="s1" targetRef="e2"/>`,
			want: "id",
		},
		"a parameter naming an undeclared item": {
			body: `    <bpmn:endEvent id="e2">
      <bpmn:messageEventDefinition messageRef="m1"/>
      <bpmn:dataInput id="e2-in" itemSubjectRef="ghost"/>
    </bpmn:endEvent>
    <bpmn:sequenceFlow id="f2" sourceRef="s1" targetRef="e2"/>`,
			want: "ghost",
		},
		"a bare parameter on a task": {
			body: `    <bpmn:task id="t2" name="T">
      <bpmn:dataInput id="t2-in" itemSubjectRef="idStr"/>
    </bpmn:task>
    <bpmn:sequenceFlow id="f2" sourceRef="s1" targetRef="t2"/>
    <bpmn:sequenceFlow id="f3" sourceRef="t2" targetRef="e1"/>`,
			want: "inside its <ioSpecification>",
		},
	}

	for name, tc := range refusals {
		t.Run(name, func(t *testing.T) {
			_, err := importEventDoc(t, eventDataDoc(tc.body))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}
