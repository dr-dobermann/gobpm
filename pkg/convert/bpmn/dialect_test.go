package bpmn

import (
	"context"
	"strings"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/convert"
)

// camundaFile is the stock Camunda 7 export from the coverage audit —
// the file that imported "successfully" and arrived with its assignee,
// its candidate groups, its external-task topic and its I/O mapping gone,
// with no error, no warning and no log line.
const camundaFile = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:camunda="http://camunda.org/schema/1.0/bpmn"
                  xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI"
                  id="Definitions_1" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="Proc" name="Order" camunda:versionTag="1.4" camunda:historyTimeToLive="30">
    <bpmn:startEvent id="s1" name="Go"/>
    <bpmn:userTask id="u1" name="Approve"
                   camunda:assignee="john"
                   camunda:candidateUsers="ann, bob"
                   camunda:candidateGroups="mgr,ops"
                   camunda:formKey="embedded:app:forms/a.html"
                   camunda:asyncBefore="true"
                   camunda:jobPriority="10">
      <bpmn:extensionElements>
        <camunda:formData>
          <camunda:formField id="amount" label="Amount" type="long"/>
        </camunda:formData>
        <camunda:taskListener event="create" class="com.acme.L"/>
      </bpmn:extensionElements>
    </bpmn:userTask>
    <bpmn:serviceTask id="v1" name="Charge"
                      camunda:type="external" camunda:topic="charge"
                      camunda:failedJobRetryTimeCycle="R3/PT5M">
      <bpmn:extensionElements>
        <camunda:inputOutput>
          <camunda:inputParameter name="amount">x</camunda:inputParameter>
        </camunda:inputOutput>
      </bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="e1"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s1" targetRef="u1"/>
    <bpmn:sequenceFlow id="f2" sourceRef="u1" targetRef="v1"/>
    <bpmn:sequenceFlow id="f3" sourceRef="v1" targetRef="e1"/>
  </bpmn:process>
  <bpmndi:BPMNDiagram id="d1"><bpmndi:BPMNPlane id="p1" bpmnElement="Proc"/></bpmndi:BPMNDiagram>
</bpmn:definitions>`

// reportOf imports doc through the document capability and indexes the
// report by construct.
func reportOf(t *testing.T, doc string) (*convert.Result, map[string]convert.Dropped) {
	t.Helper()

	res, err := importer{}.ImportDocument(
		context.Background(), strings.NewReader(doc))
	if err != nil {
		t.Fatalf("ImportDocument: %v", err)
	}

	byConstruct := make(map[string]convert.Dropped, len(res.Dropped))
	for _, d := range res.Dropped {
		byConstruct[d.Construct] = d
	}

	return res, byConstruct
}

// TestCamundaDialectIsMappedAndReported is the audit's finding, closed.
func TestCamundaDialectIsMappedAndReported(t *testing.T) {
	res, report := reportOf(t, camundaFile)

	if len(res.Processes) != 1 {
		t.Fatalf("Processes = %d, want 1", len(res.Processes))
	}

	t.Run("what the model can hold is mapped", func(t *testing.T) {
		// The mapped values are not readable back through a getter — the
		// model keeps assignment private — so the evidence that they were
		// consumed is that they are NOT in the report. A construct is
		// either mapped or reported; nothing is both, and nothing is
		// neither.
		for _, mapped := range []string{
			"camunda:assignee",
			"camunda:candidateUsers",
			"camunda:candidateGroups",
			"camunda:type",
			"camunda:topic",
		} {
			if d, reported := report[mapped]; reported {
				t.Errorf("%s was reported as unmapped (%q) — it has a model home",
					mapped, d.Reason)
			}
		}
	})

	t.Run("what it cannot hold is reported, with a reason", func(t *testing.T) {
		for _, want := range []string{
			"camunda:formKey",
			"camunda:asyncBefore",
			"camunda:jobPriority",
			"camunda:versionTag",
			"camunda:historyTimeToLive",
			"camunda:failedJobRetryTimeCycle",
			"camunda:formData",
			"camunda:taskListener",
			"camunda:inputOutput",
		} {
			d, ok := report[want]
			if !ok {
				t.Errorf("%s vanished without a report", want)

				continue
			}

			if d.Reason == "" {
				t.Errorf("%s reported with no reason", want)
			}

			if d.Element == "" {
				t.Errorf("%s reported without naming the element it was on", want)
			}
		}
	})

	t.Run("a report names the element a reader can find", func(t *testing.T) {
		if got := report["camunda:formData"].Element; got != "u1" {
			t.Errorf("formData reported on %q, want the user task u1", got)
		}

		if got := report["camunda:inputOutput"].Element; got != "v1" {
			t.Errorf("inputOutput reported on %q, want the service task v1", got)
		}

		if got := report["camunda:versionTag"].Element; got != "Proc" {
			t.Errorf("versionTag reported on %q, want the process", got)
		}
	})

	t.Run("one report per construct, not per leaf", func(t *testing.T) {
		// formData carries a formField; the file did not map ONE thing,
		// not two.
		if _, leaked := report["camunda:formField"]; leaked {
			t.Error("a nested formField was reported separately from its formData")
		}

		if _, leaked := report["camunda:inputParameter"]; leaked {
			t.Error("a nested inputParameter was reported separately")
		}
	})
}

// TestUnrecognizedNamespacesStaySilent covers the other half of the rule.
// A converter cannot report on a vocabulary it does not know, and treating
// every foreign annotation as a finding would bury the ones that matter.
func TestUnrecognizedNamespacesStaySilent(t *testing.T) {
	doc := `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <bpmn:process id="P" name="P" zeebe:versionTag="1">
    <bpmn:startEvent id="s1"/>
    <bpmn:serviceTask id="v1" name="Charge">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="charge" retries="3"/>
      </bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="e1"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s1" targetRef="v1"/>
    <bpmn:sequenceFlow id="f2" sourceRef="v1" targetRef="e1"/>
  </bpmn:process>
</bpmn:definitions>`

	res, _ := reportOf(t, doc)

	if len(res.Dropped) != 0 {
		t.Errorf("Dropped = %#v, want nothing — an unrecognized namespace is "+
			"not something this converter can speak about", res.Dropped)
	}
}

// TestImportIsUnchangedByReporting pins NFR-2: a caller using the original
// entry point sees exactly what it always did.
func TestImportIsUnchangedByReporting(t *testing.T) {
	p, err := importer{}.Import(context.Background(),
		strings.NewReader(camundaFile))
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	if p.ID() != "Proc" || len(p.Nodes()) != 4 {
		t.Errorf("Import returned %q with %d nodes, want Proc with 4",
			p.ID(), len(p.Nodes()))
	}
}

// TestSplitList covers the comma-separated id lists the dialect uses,
// including the spacing a modeler actually types.
func TestSplitList(t *testing.T) {
	tests := map[string][]string{
		"ann,bob":     {"ann", "bob"},
		"ann, bob":    {"ann", "bob"},
		" ann , bob ": {"ann", "bob"},
		"solo":        {"solo"},
		"a,,b":        {"a", "b"},
		"":            {},
	}

	for in, want := range tests {
		t.Run(in, func(t *testing.T) {
			got := splitList(in)
			if len(got) != len(want) {
				t.Fatalf("splitList(%q) = %v, want %v", in, got, want)
			}

			for i := range got {
				if got[i] != want[i] {
					t.Errorf("splitList(%q)[%d] = %q, want %q", in, i, got[i], want[i])
				}
			}
		})
	}
}

// TestAnUnknownDialectConstructIsStillReported is the guarantee that makes
// this mechanism worth having.
//
// The reporter walks the element's ACTUAL attributes and children rather
// than looking for names it knows, so a construct added to Camunda after
// this code was written — or one nobody here has heard of — is reported
// with a truthful generic reason instead of vanishing. A list-driven
// reporter would silently drop exactly the constructs it most needs to
// mention.
func TestAnUnknownDialectConstructIsStillReported(t *testing.T) {
	doc := `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:camunda="http://camunda.org/schema/1.0/bpmn">
  <bpmn:process id="P" name="P">
    <bpmn:startEvent id="s1"/>
    <bpmn:userTask id="u1" name="Approve" camunda:inventedInVersion99="yes">
      <bpmn:extensionElements>
        <camunda:somethingNobodyHasSeen id="x"/>
      </bpmn:extensionElements>
    </bpmn:userTask>
    <bpmn:endEvent id="e1"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s1" targetRef="u1"/>
    <bpmn:sequenceFlow id="f2" sourceRef="u1" targetRef="e1"/>
  </bpmn:process>
</bpmn:definitions>`

	_, report := reportOf(t, doc)

	for _, want := range []string{
		"camunda:inventedInVersion99",
		"camunda:somethingNobodyHasSeen",
	} {
		d, ok := report[want]
		if !ok {
			t.Errorf("%s vanished — an unknown construct is exactly what "+
				"reporting exists for", want)

			continue
		}

		if d.Reason == "" {
			t.Errorf("%s reported with no reason", want)
		}
	}
}

// TestImportDocumentPropagatesParseErrors pins that the capability fails
// the same way Import does. A report is not a reason to soften a refusal:
// a file that cannot be imported is not a file imported with notes.
func TestImportDocumentPropagatesParseErrors(t *testing.T) {
	res, err := importer{}.ImportDocument(context.Background(),
		strings.NewReader(`<?xml version="1.0"?><bpmn:definitions `+
			`xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">`+
			`<bpmn:process id="P" name="P"><bpmn:subProcess id="sub"/>`+
			`</bpmn:process></bpmn:definitions>`))

	if err == nil {
		t.Fatalf("ImportDocument = %v, want the unsupported-element refusal", res)
	}

	if res != nil {
		t.Errorf("ImportDocument returned %v alongside an error", res)
	}
}

// TestReportingSurvivesATruncatedExtension covers the stream-error path of
// the reporting walk: a document that ends mid-extension must fail, not
// return a half report.
func TestReportingSurvivesATruncatedExtension(t *testing.T) {
	_, err := importer{}.ImportDocument(context.Background(),
		strings.NewReader(`<?xml version="1.0"?><bpmn:definitions `+
			`xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" `+
			`xmlns:camunda="http://camunda.org/schema/1.0/bpmn">`+
			`<bpmn:process id="P" name="P"><bpmn:userTask id="u1" name="A">`+
			`<bpmn:extensionElements><camunda:formData>`))

	if err == nil {
		t.Fatal("a truncated extension subtree must fail the import")
	}
}

// TestCamundaTopicMatchesTheDialect guards the one risk in reading a
// Camunda attribute name out of the observability vocabulary.
//
// The importer uses observability.AttrTopic as the attribute name because
// the repo enforces the constant wherever its spelling appears
// (internal/lintcfg TestNoLiteralAttrKeys). But the two are different
// things that merely spell the same: Camunda fixes `camunda:topic`, while
// a log key may be renamed at any time. Without this pin such a rename
// would make the parser look for an attribute no file carries, and every
// external-task topic would go back to being silently dropped — with all
// the other tests still green.
func TestCamundaTopicMatchesTheDialect(t *testing.T) {
	const dialectAttr = "topic"

	if camundaTopic != dialectAttr {
		t.Fatalf("camundaTopic = %q, but Camunda names the attribute %q; the "+
			"importer reads it through the observability constant, so they must "+
			"agree — give the dialect its own constant if the vocabulary moves",
			camundaTopic, dialectAttr)
	}
}
