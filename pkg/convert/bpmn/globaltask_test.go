package bpmn

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/convert"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
)

// globalDoc builds a document carrying one global task and one ordinary
// process, which is the shape a modeler produces when they factor a task out
// for reuse.
func globalDoc(global string) string {
	return fmt.Sprintf(`<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="%s"
    xmlns:camunda="http://camunda.org/schema/1.0/bpmn">
%s
  <bpmn:process id="P" name="P">
    <bpmn:startEvent id="s1"/>
    <bpmn:callActivity id="ca" name="Reuse" calledElement="g1"/>
    <bpmn:endEvent id="e1"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s1" targetRef="ca"/>
    <bpmn:sequenceFlow id="f2" sourceRef="ca" targetRef="e1"/>
  </bpmn:process>
</bpmn:definitions>`, nsBPMN, global)
}

// callableOf imports doc and returns the process the global task became.
func callableOf(t *testing.T, doc string) *process.Process {
	t.Helper()

	res, err := importer{}.ImportDocument(
		context.Background(), strings.NewReader(doc))
	if err != nil {
		t.Fatalf("ImportDocument: %v", err)
	}

	for _, p := range res.Processes {
		if p.ID() == "g1" {
			return p
		}
	}

	t.Fatalf("no callable with id g1 among %d processes", len(res.Processes))

	return nil
}

// nodeOf returns the callable's node with the given id.
func nodeOf(t *testing.T, p *process.Process, id string) flow.Node {
	t.Helper()

	for _, n := range p.Nodes() {
		if n.ID() == id {
			return n
		}
	}

	ids := make([]string, 0, len(p.Nodes()))
	for _, n := range p.Nodes() {
		ids = append(ids, n.ID())
	}

	t.Fatalf("no node %q in the callable; got %v", id, ids)

	return nil
}

// TestGlobalTaskBecomesACallableProcess is SRD-096 T-14: each member of the
// family imports as a process the registry can serve.
//
// The shape is what §13.3.4 requires of anything a call activity invokes: a
// called process is entered by its None Start Event, so a callable with no
// flow of its own is not callable until it has one.
func TestGlobalTaskBecomesACallableProcess(t *testing.T) {
	for tag, want := range map[string]string{
		"globalTask":             "*activities.ManualTask",
		"globalManualTask":       "*activities.ManualTask",
		"globalUserTask":         "*activities.UserTask",
		"globalBusinessRuleTask": "*activities.BusinessRuleTask",
	} {
		t.Run(tag, func(t *testing.T) {
			decl := fmt.Sprintf(`  <bpmn:%s id="g1" name="Approve"`, tag)
			if tag == "globalBusinessRuleTask" {
				decl += ` camunda:decisionRef="grade"`
			}

			p := callableOf(t, globalDoc(decl+"/>"))

			if p.Name() != "Approve" {
				t.Errorf("Name() = %q, want the global task's own name", p.Name())
			}

			if n := len(p.Nodes()); n != 3 {
				t.Fatalf("nodes = %d, want 3 (None start, task, None end)", n)
			}

			task := nodeOf(t, p, "g1.task")
			if got := fmt.Sprintf("%T", task); got != want {
				t.Errorf("g1.task is a %s, want %s — the tag rewrite must "+
					"reach the in-process builder, not a second reading",
					got, want)
			}

			// The scaffolding, under ids derived from the global task's own.
			nodeOf(t, p, "g1.start")
			nodeOf(t, p, "g1.end")

			if n := len(p.Flows()); n != 2 {
				t.Fatalf("flows = %d, want 2 joining the three nodes", n)
			}

			// Derived, so a re-import mints the same ids and two versions of
			// one definition stay comparable.
			ids := map[string]bool{}
			for _, f := range p.Flows() {
				ids[f.ID()] = true
			}

			for _, want := range []string{"g1.start-task", "g1.task-end"} {
				if !ids[want] {
					t.Errorf("no flow %q; got %v — the ids are derived from "+
						"the global task's own", want, ids)
				}
			}
		})
	}
}

// TestGlobalScriptTaskCarriesItsScript covers the family member whose body
// decides what is built: the same <script> reader the in-process form uses.
func TestGlobalScriptTaskCarriesItsScript(t *testing.T) {
	p := callableOf(t, globalDoc(
		`  <bpmn:globalScriptTask id="g1" name="Compute" scriptFormat="lua">
    <bpmn:script>data.set("total", 42)</bpmn:script>
  </bpmn:globalScriptTask>`))

	st, ok := nodeOf(t, p, "g1.task").(*activities.ScriptTask)
	if !ok {
		t.Fatalf("g1.task is a %T, want a *activities.ScriptTask",
			nodeOf(t, p, "g1.task"))
	}

	if !strings.Contains(st.Script(), "data.set") {
		t.Errorf("Script() = %q, want the body the file wrote", st.Script())
	}
}

// TestGlobalTaskContractIsTheProcessContract is SRD-096 T-15 and FR-7a.
//
// The callable's <ioSpecification> is ONE element in the standard, and the
// engine needs it in two places: as the process's declared contract, which a
// caller binds against, and as the task's own parameters, which is what
// actually produces a declared output.
//
// It belongs to the PROCESS, and not also to the task inside it. Copying it
// onto the task was the draft's reading and it does not run: a task's
// parameters are filled by data associations, a callable declares none, so a
// required input would be declared and unfillable. What the contract reads at
// completion is the root scope, which is where the task's work lands anyway
// — proven by examples/bpmn-callable, where the value crosses the boundary
// and comes back.
func TestGlobalTaskContractIsTheProcessContract(t *testing.T) {
	p := callableOf(t, globalDoc(
		`  <bpmn:globalUserTask id="g1" name="Approve">
    <bpmn:ioSpecification id="g1.io">
      <bpmn:dataInput id="amount" name="amount"/>
      <bpmn:dataOutput id="approved" name="approved"/>
      <bpmn:inputSet id="g1.is"><bpmn:dataInputRefs>amount</bpmn:dataInputRefs></bpmn:inputSet>
      <bpmn:outputSet id="g1.os"><bpmn:dataOutputRefs>approved</bpmn:dataOutputRefs></bpmn:outputSet>
    </bpmn:ioSpecification>
  </bpmn:globalUserTask>`))

	ios := p.IOSpec()
	if ios == nil {
		t.Fatal("IOSpec() = nil — the callable declares a contract, and a " +
			"caller binds against it by name")
	}

	ins, outs := ios.InputSet(), ios.OutputSet()
	if len(ins) != 1 || len(outs) != 1 {
		t.Fatalf("contract = %d in / %d out, want 1 and 1", len(ins), len(outs))
	}

	if ins[0].Name() != "amount" || outs[0].Name() != "approved" {
		t.Errorf("contract = (%q, %q), want (amount, approved)",
			ins[0].Name(), outs[0].Name())
	}
}

// TestGlobalTaskWithoutAContract is T-15's other half: no <ioSpecification>
// means a contract-LESS callable, which keeps the permissive meaning a
// process without one has always had — not an empty contract that promises
// nothing and demands nothing.
func TestGlobalTaskWithoutAContract(t *testing.T) {
	p := callableOf(t, globalDoc(
		`  <bpmn:globalTask id="g1" name="Reusable"/>`))

	if ios := p.IOSpec(); ios != nil {
		t.Errorf("IOSpec() = %v, want nil — a callable that declares nothing "+
			"is contract-less, and a caller binds against it permissively",
			ios)
	}
}

// TestGlobalTaskDerivedIDCollides is SRD-096 T-16: a document already using a
// derived id is refused for the duplicate.
//
// Silently rewiring would be the worse outcome — the file would import, and
// the node the modeler wrote would have been quietly replaced by scaffolding.
func TestGlobalTaskDerivedIDCollides(t *testing.T) {
	doc := fmt.Sprintf(`<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="%s">
  <bpmn:globalTask id="g1" name="Reusable"/>
  <bpmn:process id="P" name="P">
    <bpmn:startEvent id="g1.start"/>
    <bpmn:endEvent id="e1"/>
    <bpmn:sequenceFlow id="f1" sourceRef="g1.start" targetRef="e1"/>
  </bpmn:process>
</bpmn:definitions>`, nsBPMN)

	_, err := importer{}.ImportDocument(
		context.Background(), strings.NewReader(doc))
	if err == nil {
		t.Fatal("a document using a derived id must be refused, not rewired")
	}

	if !strings.Contains(err.Error(), "g1.start") {
		t.Errorf("error = %v, want it to name the colliding id", err)
	}
}

// TestGlobalTaskNeedsAnID covers the id that is also the key: without one the
// callable could never be named by a callActivity.
func TestGlobalTaskNeedsAnID(t *testing.T) {
	_, err := importer{}.ImportDocument(context.Background(),
		strings.NewReader(globalDoc(`  <bpmn:globalTask name="Reusable"/>`)))
	if err == nil {
		t.Fatal("a global task with no id must be refused")
	}

	if !strings.Contains(err.Error(), "id") {
		t.Errorf("error = %v, want it to name the missing id", err)
	}
}

// TestCallActivityNamesAGlobalTask closes the loop the family exists for: the
// call activity's calledElement is the global task's id, and nothing about
// the call needs to know a global task was involved.
func TestCallActivityNamesAGlobalTask(t *testing.T) {
	res, err := importer{}.ImportDocument(context.Background(),
		strings.NewReader(globalDoc(
			`  <bpmn:globalTask id="g1" name="Reusable"/>`)))
	if err != nil {
		t.Fatalf("ImportDocument: %v", err)
	}

	var caller *process.Process

	for _, p := range res.Processes {
		if p.ID() == "P" {
			caller = p
		}
	}

	if caller == nil {
		t.Fatal("no process P")
	}

	for _, n := range caller.Nodes() {
		if ca, ok := n.(*activities.CallActivity); ok {
			if ca.CalledKey() != "g1" {
				t.Errorf("CalledKey() = %q, want the global task's id",
					ca.CalledKey())
			}

			return
		}
	}

	t.Fatal("no call activity in P")
}

// TestGlobalTaskIDJoinsTheLedger: the callable's own id is a document id like
// any other, so a second element claiming it is the ordinary duplicate
// refusal — and it must be, because that id is the registry key two
// callables would then share.
func TestGlobalTaskIDJoinsTheLedger(t *testing.T) {
	doc := fmt.Sprintf(`<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="%s">
  <bpmn:globalTask id="g1" name="First"/>
  <bpmn:globalUserTask id="g1" name="Second"/>
  <bpmn:process id="P" name="P">
    <bpmn:startEvent id="s1"/>
    <bpmn:endEvent id="e1"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s1" targetRef="e1"/>
  </bpmn:process>
</bpmn:definitions>`, nsBPMN)

	_, err := importer{}.ImportDocument(
		context.Background(), strings.NewReader(doc))
	if err == nil {
		t.Fatal("two callables cannot share an id: it is the registry key " +
			"a callActivity names, and one of them would be unreachable")
	}
}

// TestCallablesOnlyDocumentHasNoProcessToImport is SRD-096 FR-9's other half.
//
// Import answers with THE process of a document. A file of callables has
// none — they are invoked by a process, not run on their own — so the refusal
// says that rather than counting them as candidates and reporting an
// ambiguity that would misdescribe the file.
func TestCallablesOnlyDocumentHasNoProcessToImport(t *testing.T) {
	doc := fmt.Sprintf(`<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="%s">
  <bpmn:globalTask id="g1" name="One"/>
  <bpmn:globalUserTask id="g2" name="Two"/>
</bpmn:definitions>`, nsBPMN)

	_, err := importer{}.Import(context.Background(), strings.NewReader(doc))
	if err == nil {
		t.Fatal("a callables-only document has no process to return")
	}

	if !strings.Contains(err.Error(), "ImportDocument") {
		t.Errorf("error = %v, want it to point at the call that reads them",
			err)
	}

	// They do import — through the document-level call.
	res, derr := importer{}.ImportDocument(
		context.Background(), strings.NewReader(doc))
	if derr != nil {
		t.Fatalf("ImportDocument: %v — the callables are readable", derr)
	}

	if len(res.Processes) != 2 {
		t.Errorf("Processes = %d, want the 2 callables", len(res.Processes))
	}
}

// TestGlobalTaskWithABadBodyRefusesInTheTaskSWords: the body reader is the
// in-process one, so a malformed body is refused in the same words the
// in-process form uses — naming the derived task id, which is what tells a
// reader which callable carried it.
func TestGlobalTaskWithABadBodyRefusesInTheTaskSWords(t *testing.T) {
	_, err := importer{}.ImportDocument(context.Background(),
		strings.NewReader(globalDoc(
			`  <bpmn:globalScriptTask id="g1" name="Compute">
    <bpmn:script>whatever</bpmn:script>
  </bpmn:globalScriptTask>`)))
	if err == nil {
		t.Fatal("a script task with no scriptFormat must be refused, global " +
			"or not")
	}

	for _, want := range []string{"scriptFormat", "g1.task"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v, want it to mention %q", err, want)
		}
	}
}

// TestGlobalTaskInsideAProcessIsRefused is SRD-096 T-18 and FR-10's other
// half.
//
// The family is a definitions-level declaration; inside a <process> it is not
// a flow element, and the context rule refuses it. The refusal carries an
// EMPTY section on purpose — the vendored extract pins no § for the family,
// and inventing one would be worse than omitting it (the same reason
// globalChoreographyTask is pinned to "").
func TestGlobalTaskInsideAProcessIsRefused(t *testing.T) {
	for _, tag := range []string{"globalTask", "globalUserTask"} {
		t.Run(tag, func(t *testing.T) {
			doc := fmt.Sprintf(`<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="%s">
  <bpmn:process id="P" name="P">
    <bpmn:startEvent id="s1"/>
    <bpmn:%s id="g1" name="Inner"/>
    <bpmn:endEvent id="e1"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s1" targetRef="e1"/>
  </bpmn:process>
</bpmn:definitions>`, nsBPMN, tag)

			_, err := importer{}.ImportDocument(
				context.Background(), strings.NewReader(doc))
			if err == nil {
				t.Fatalf("<%s> inside a <process> must be refused: it is a "+
					"declaration, not a flow element", tag)
			}

			var uee *convert.UnsupportedElementError
			if !errors.As(err, &uee) {
				t.Fatalf("error = %v (%T), want an UnsupportedElementError", err, err)
			}

			if uee.Section != "" {
				t.Errorf("Section = %q, want empty — the extract pins no § "+
					"for the family, and an invented one is worse than none",
					uee.Section)
			}
		})
	}
}

// TestImportPicksTheDeclaredProcess is SRD-096 T-19 (a) and (b), and the
// discriminating case for FR-9.
//
// A callable is invoked BY a process, never the document's own executable
// one. Counting it would make the ordinary file — one unmarked <process>
// beside a <globalTask> — newly ambiguous, which is the regression this
// asserts against. The unmarked shape matters: SRD-089.I records that most
// real single-process files never set isExecutable.
func TestImportPicksTheDeclaredProcess(t *testing.T) {
	for name, attr := range map[string]string{
		"unmarked":   "",
		"executable": ` isExecutable="true"`,
	} {
		t.Run(name, func(t *testing.T) {
			doc := fmt.Sprintf(`<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="%s">
  <bpmn:globalTask id="g1" name="Reusable"/>
  <bpmn:process id="P" name="P"%s>
    <bpmn:startEvent id="s1"/>
    <bpmn:callActivity id="ca" name="Reuse" calledElement="g1"/>
    <bpmn:endEvent id="e1"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s1" targetRef="ca"/>
    <bpmn:sequenceFlow id="f2" sourceRef="ca" targetRef="e1"/>
  </bpmn:process>
</bpmn:definitions>`, nsBPMN, attr)

			p, err := importer{}.Import(
				context.Background(), strings.NewReader(doc))
			if err != nil {
				t.Fatalf("Import: %v — the callable is not a candidate, so "+
					"this document has exactly one answer", err)
			}

			if p.ID() != "P" {
				t.Errorf("Import returned %q, want the declared process P",
					p.ID())
			}
		})
	}
}

// TestGlobalTaskDeclaredIDsJoinTheLedger is SRD-096 T-23: the ids the FILE
// writes inside a callable are claimed like any other, so a collision with a
// document id is refused rather than silently shared.
func TestGlobalTaskDeclaredIDsJoinTheLedger(t *testing.T) {
	doc := fmt.Sprintf(`<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="%s">
  <bpmn:globalUserTask id="g1" name="Approve">
    <bpmn:ioSpecification id="shared.io">
      <bpmn:dataInput id="amount" name="amount"/>
      <bpmn:inputSet id="g1.is"><bpmn:dataInputRefs>amount</bpmn:dataInputRefs></bpmn:inputSet>
      <bpmn:outputSet id="g1.os"/>
    </bpmn:ioSpecification>
  </bpmn:globalUserTask>
  <bpmn:process id="P" name="P" isExecutable="true">
    <bpmn:startEvent id="s1"/>
    <bpmn:task id="t1" name="Work">
      <bpmn:ioSpecification id="shared.io">
        <bpmn:dataInput id="din" name="in"/>
        <bpmn:inputSet id="is1"><bpmn:dataInputRefs>din</bpmn:dataInputRefs></bpmn:inputSet>
        <bpmn:outputSet id="os1"/>
      </bpmn:ioSpecification>
    </bpmn:task>
    <bpmn:endEvent id="e1"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s1" targetRef="t1"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t1" targetRef="e1"/>
  </bpmn:process>
</bpmn:definitions>`, nsBPMN)

	_, err := importer{}.ImportDocument(
		context.Background(), strings.NewReader(doc))
	if err == nil {
		t.Fatal("a callable's declared ids share the document's ledger; a " +
			"duplicate must be refused, not quietly shared")
	}

	if !strings.Contains(err.Error(), "shared.io") {
		t.Errorf("error = %v, want it to name the duplicated id", err)
	}
}
