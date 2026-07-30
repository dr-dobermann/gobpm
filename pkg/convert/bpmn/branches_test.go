package bpmn

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/convert"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
)

// wrapDefs renders a <bpmn:definitions> around body. The x: prefix gives every
// case a foreign namespace to exercise the skip policy (SRD-051 §FR-7).
func wrapDefs(body string) string {
	return `<bpmn:definitions` +
		` xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"` +
		` xmlns:x="urn:example:foreign">` + body + `</bpmn:definitions>`
}

// linearProcess renders a start → task → end process, letting a case inject
// extra markup into the task element and extra flow children.
func linearProcess(taskBody, flowBody string) string {
	return `<bpmn:process id="p" isExecutable="true">` +
		`<bpmn:startEvent id="s"/>` +
		`<bpmn:task id="t" name="work">` + taskBody + `</bpmn:task>` +
		`<bpmn:endEvent id="e"/>` +
		`<bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t">` + flowBody +
		`</bpmn:sequenceFlow>` +
		`<bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>` +
		`</bpmn:process>`
}

// runImportCases imports each document and matches the outcome against want:
// an empty want means the import must succeed.
func runImportCases(t *testing.T, cases map[string]struct{ doc, want string }) {
	t.Helper()

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := (importer{}).Import(context.Background(), strings.NewReader(tc.doc))

			switch {
			case tc.want == "" && err != nil:
				t.Fatalf("Import: unexpected error: %v", err)

			case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
				t.Fatalf("Import error = %v, want substring %q", err, tc.want)
			}
		})
	}
}

// TestImportInterfaceCatalogBranches covers the definitions-level
// <interface>/<operation> catalog: identity rules, the duplicate guard, and
// the child-element skip policy (SRD-051 §4.6/§FR-7).
func TestImportInterfaceCatalogBranches(t *testing.T) {
	proc := `<bpmn:process id="p" isExecutable="true">` +
		`<bpmn:startEvent id="s"/><bpmn:endEvent id="e"/>` +
		`<bpmn:sequenceFlow id="f" sourceRef="s" targetRef="e"/>` +
		`</bpmn:process>`

	iface := func(body string) string {
		return wrapDefs(`<bpmn:interface id="i1" name="Iface">` + body + `</bpmn:interface>` + proc)
	}

	op := func(body string) string {
		return iface(`<bpmn:operation id="o1" name="Op">` + body + `</bpmn:operation>`)
	}

	runImportCases(t, map[string]struct{ doc, want string }{
		"interface without id": {
			doc:  wrapDefs(`<bpmn:interface name="Iface"/>` + proc),
			want: "has no id",
		},
		"interface without name falls back to id": {
			doc: wrapDefs(`<bpmn:interface id="i1"/>` + proc),
		},
		"interface skips foreign-namespace child": {
			doc: iface(`<x:meta><x:inner/></x:meta>`),
		},
		"interface skips documentation": {
			doc: iface(`<bpmn:documentation>notes</bpmn:documentation>`),
		},
		"interface rejects unmapped bpmn child": {
			doc:  iface(`<bpmn:callableElement id="c"/>`),
			want: "unsupported element",
		},
		"operation without id": {
			doc:  iface(`<bpmn:operation name="Op"/>`),
			want: "has no id",
		},
		"operation without name falls back to id": {
			doc: iface(`<bpmn:operation id="o1"/>`),
		},
		"duplicate operation id": {
			doc: iface(`<bpmn:operation id="o1"/>` +
				`<bpmn:operation id="o1"/>`),
			want: "duplicate operation id",
		},
		"operation records outMessageRef": {
			doc: op(`<bpmn:outMessageRef>msg-out</bpmn:outMessageRef>`),
		},
		"operation skips foreign-namespace child": {
			doc: op(`<x:binding kind="soap"/>`),
		},
		"operation skips unmapped bpmn child": {
			doc: op(`<bpmn:errorRef>err-1</bpmn:errorRef>`),
		},
	})
}

// TestImportNodeBodyBranches covers the flow-node child policy: incoming and
// outgoing are redundant with sequenceFlow wiring, annotations and foreign
// namespaces are skipped, anything else in the BPMN namespace fails closed.
func TestImportNodeBodyBranches(t *testing.T) {
	runImportCases(t, map[string]struct{ doc, want string }{
		"node skips incoming and outgoing": {
			doc: wrapDefs(linearProcess(
				`<bpmn:incoming>f1</bpmn:incoming><bpmn:outgoing>f2</bpmn:outgoing>`, "")),
		},
		"node skips documentation and extensionElements": {
			doc: wrapDefs(linearProcess(
				`<bpmn:documentation>why</bpmn:documentation>`+
					`<bpmn:extensionElements><x:props/></bpmn:extensionElements>`, "")),
		},
		"node skips foreign-namespace child": {
			doc: wrapDefs(linearProcess(`<x:bounds w="10"/>`, "")),
		},
		"node rejects unmapped bpmn child": {
			doc:  wrapDefs(linearProcess(`<bpmn:ioSpecification id="io"/>`, "")),
			want: "unsupported element",
		},
	})
}

// TestImportSequenceFlowChildBranches covers the conditionExpression reader
// and the sequenceFlow child skip policy (SRD-051 §FR-5/§FR-7).
func TestImportSequenceFlowChildBranches(t *testing.T) {
	runImportCases(t, map[string]struct{ doc, want string }{
		"blank condition is dropped": {
			doc: wrapDefs(linearProcess("",
				`<bpmn:conditionExpression>   </bpmn:conditionExpression>`)),
		},
		"condition skips foreign-namespace child": {
			doc: wrapDefs(linearProcess("",
				`<bpmn:conditionExpression>ok<x:hint/></bpmn:conditionExpression>`)),
		},
		"condition rejects nested bpmn element": {
			doc: wrapDefs(linearProcess("",
				`<bpmn:conditionExpression><bpmn:script>x</bpmn:script></bpmn:conditionExpression>`)),
			want: "unsupported element",
		},
		"flow skips foreign-namespace child": {
			doc: wrapDefs(linearProcess("", `<x:label text="yes"/>`)),
		},
		"flow skips documentation": {
			doc: wrapDefs(linearProcess("", `<bpmn:documentation>edge</bpmn:documentation>`)),
		},
		"flow rejects unmapped bpmn child": {
			doc:  wrapDefs(linearProcess("", `<bpmn:auditing id="a"/>`)),
			want: "unsupported element",
		},
	})
}

// TestImportGatewayBranches covers gatewayDirection validation and the pass-2
// re-resolution of an exclusiveGateway's default attribute (SRD-051 §3.3).
func TestImportGatewayBranches(t *testing.T) {
	branch := func(gwAttrs string) string {
		return wrapDefs(`<bpmn:process id="p" isExecutable="true">` +
			`<bpmn:startEvent id="s"/>` +
			`<bpmn:exclusiveGateway id="g" ` + gwAttrs + `/>` +
			`<bpmn:endEvent id="e1"/><bpmn:endEvent id="e2"/>` +
			`<bpmn:sequenceFlow id="f0" sourceRef="s" targetRef="g"/>` +
			`<bpmn:sequenceFlow id="f1" sourceRef="g" targetRef="e1"/>` +
			`<bpmn:sequenceFlow id="f2" sourceRef="g" targetRef="e2"/>` +
			`</bpmn:process>`)
	}

	runImportCases(t, map[string]struct{ doc, want string }{
		"valid default flow": {
			doc: branch(`default="f2"`),
		},
		"invalid gatewayDirection": {
			doc:  branch(`gatewayDirection="Sideways"`),
			want: "invalid gatewayDirection",
		},
		"default names an unknown flow": {
			doc:  branch(`default="nope"`),
			want: "unknown default flow",
		},
	})
}

// TestImportDefinitionsChildBranches covers the <definitions> child policy:
// only interface and a single process are mapped (SRD-051 §FR-5/§FR-7).
func TestImportDefinitionsChildBranches(t *testing.T) {
	proc := `<bpmn:process id="p" isExecutable="true">` +
		`<bpmn:startEvent id="s"/><bpmn:endEvent id="e"/>` +
		`<bpmn:sequenceFlow id="f" sourceRef="s" targetRef="e"/>` +
		`</bpmn:process>`

	runImportCases(t, map[string]struct{ doc, want string }{
		"definitions rejects unmapped child": {
			doc:  wrapDefs(`<bpmn:collaboration id="c"/>` + proc),
			want: "unsupported element",
		},
		"definitions rejects a second process": {
			doc:  wrapDefs(proc + proc),
			want: "unsupported element",
		},
		"process skips foreign-namespace child": {
			doc: wrapDefs(`<bpmn:process id="p" isExecutable="true">` +
				`<x:laneSet id="l"><x:lane id="l1"/></x:laneSet>` +
				`<bpmn:startEvent id="s"/><bpmn:endEvent id="e"/>` +
				`<bpmn:sequenceFlow id="f" sourceRef="s" targetRef="e"/>` +
				`</bpmn:process>`),
		},
	})
}

// TestImportSequenceFlowIdentityBranches covers the sequenceFlow identity and
// endpoint-role rules: an id is mandatory (ADR-019), both refs are mandatory,
// and the referenced nodes must be able to play their end of the flow.
func TestImportSequenceFlowIdentityBranches(t *testing.T) {
	graph := func(extraFlow string) string {
		return wrapDefs(`<bpmn:process id="p" isExecutable="true">` +
			`<bpmn:startEvent id="s"/>` +
			`<bpmn:task id="t" name="work"/>` +
			`<bpmn:endEvent id="e"/>` +
			`<bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>` +
			`<bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>` +
			extraFlow +
			`</bpmn:process>`)
	}

	runImportCases(t, map[string]struct{ doc, want string }{
		"sequenceFlow without id": {
			doc:  graph(`<bpmn:sequenceFlow sourceRef="s" targetRef="e"/>`),
			want: "has no id",
		},
		"sequenceFlow without targetRef": {
			doc:  graph(`<bpmn:sequenceFlow id="f3" sourceRef="s"/>`),
			want: "needs both sourceRef and targetRef",
		},
		"endEvent cannot be a source": {
			doc:  graph(`<bpmn:sequenceFlow id="f3" sourceRef="e" targetRef="t"/>`),
			want: "is not a sequence source",
		},
		"startEvent cannot be a target": {
			doc:  graph(`<bpmn:sequenceFlow id="f3" sourceRef="t" targetRef="s"/>`),
			want: "is not a sequence target",
		},
	})
}

// TestImportGatewayDefaultNotOutgoing covers the UpdateDefaultFlow failure:
// the default attribute names a real flow that the gateway does not own.
func TestImportGatewayDefaultNotOutgoing(t *testing.T) {
	runImportCases(t, map[string]struct{ doc, want string }{
		"default names an incoming flow": {
			doc: wrapDefs(`<bpmn:process id="p" isExecutable="true">` +
				`<bpmn:startEvent id="s"/>` +
				`<bpmn:exclusiveGateway id="g" default="f0"/>` +
				`<bpmn:endEvent id="e1"/><bpmn:endEvent id="e2"/>` +
				`<bpmn:sequenceFlow id="f0" sourceRef="s" targetRef="g"/>` +
				`<bpmn:sequenceFlow id="f1" sourceRef="g" targetRef="e1"/>` +
				`<bpmn:sequenceFlow id="f2" sourceRef="g" targetRef="e2"/>` +
				`</bpmn:process>`),
			want: "couldn't set default flow",
		},
	})
}

// bodylessCondition is a data.FormalExpression with no Body() accessor — the
// shape a compiled expression engine yields. Export must reject it with a
// classified error rather than drop the condition (SRD-051 open question 2).
type bodylessCondition struct{}

var _ data.FormalExpression = bodylessCondition{}

func (bodylessCondition) ID() string                        { return "compiled-1" }
func (bodylessCondition) Docs() []*foundation.Documentation { return nil }
func (bodylessCondition) Language() string                  { return "urn:compiled" }
func (bodylessCondition) ResultType() string                { return "bool" }
func (bodylessCondition) IsEvaluated() bool                 { return false }

func (bodylessCondition) Evaluate(context.Context, data.Source) (data.Value, error) {
	return nil, errors.New("not evaluable")
}

func (bodylessCondition) Result() (data.Value, error) {
	return nil, errors.New("not evaluated")
}

// buildProcess assembles start → task → end programmatically. flowOpts are
// applied to the start → task link.
func buildProcess(t *testing.T, tasks int, flowOpts ...any) *process.Process {
	t.Helper()

	p, err := process.New("Built", foundation.WithID("built"))
	if err != nil {
		t.Fatalf("process.New: %v", err)
	}

	start, err := events.NewStartEvent("s", foundation.WithID("s"))
	if err != nil {
		t.Fatalf("NewStartEvent: %v", err)
	}

	end, err := events.NewEndEvent("e", foundation.WithID("e"))
	if err != nil {
		t.Fatalf("NewEndEvent: %v", err)
	}

	if err := p.Add(start); err != nil {
		t.Fatalf("Add start: %v", err)
	}

	prev := flow.Node(start)

	for i := range tasks {
		id := fmt.Sprintf("t%d", i)

		task, err := activities.NewManualTask(id,
			foundation.WithID(id), activities.WithoutParams())
		if err != nil {
			t.Fatalf("NewManualTask: %v", err)
		}

		if err := p.Add(task); err != nil {
			t.Fatalf("Add task: %v", err)
		}

		linkNodes(t, prev, task, fmt.Sprintf("f%d", i), i == 0, flowOpts)
		prev = task
	}

	if err := p.Add(end); err != nil {
		t.Fatalf("Add end: %v", err)
	}

	linkNodes(t, prev, end, "f-end", tasks == 0, flowOpts)

	return p
}

// linkNodes links src → trg, applying flowOpts only to the first link.
func linkNodes(t *testing.T, src, trg flow.Node, id string, first bool, flowOpts []any) {
	t.Helper()

	opts := []options.Option{foundation.WithID(id)}

	if first {
		for _, o := range flowOpts {
			opt, ok := o.(options.Option)
			if !ok {
				t.Fatalf("flow option %T is not an options.Option", o)
			}

			opts = append(opts, opt)
		}
	}

	source, ok := src.(flow.SequenceSource)
	if !ok {
		t.Fatalf("%T is not a sequence source", src)
	}

	target, ok := trg.(flow.SequenceTarget)
	if !ok {
		t.Fatalf("%T is not a sequence target", trg)
	}

	if _, err := flow.Link(source, target, opts...); err != nil {
		t.Fatalf("link %s: %v", id, err)
	}
}

// TestExportConditionWithoutSourceText covers the refusal to serialize a
// condition whose source text is unavailable (SRD-051 §FR-5).
func TestExportConditionWithoutSourceText(t *testing.T) {
	p := buildProcess(t, 1, flow.WithCondition(bodylessCondition{}))

	err := (exporter{}).Export(context.Background(), &bytes.Buffer{}, p)
	if err == nil || !strings.Contains(err.Error(), "no source text to export") {
		t.Fatalf("Export = %v, want a no-source-text failure", err)
	}
}

// TestFlowXMLNilFlow covers the nil-endpoint guards that keep a malformed
// model from panicking inside the exporter.
func TestFlowXMLNilFlow(t *testing.T) {
	xf, err := flowXML(nil)
	if xf != nil || err == nil || !strings.Contains(err.Error(), "sequenceFlow is nil") {
		t.Fatalf("flowXML(nil) = %v, %v", xf, err)
	}
}

// failAfterWriter accepts okWrites calls, then fails every later one. It
// separates the header write from the encoder's own writes so both the
// Encode and the Flush failure paths are reachable.
type failAfterWriter struct {
	okWrites int
	calls    int
}

func (w *failAfterWriter) Write(b []byte) (int, error) {
	w.calls++
	if w.calls > w.okWrites {
		return 0, io.ErrShortWrite
	}

	return len(b), nil
}

// TestExportEncodeFailure covers the encoder-side write failure: the header
// goes out, then every write the encoder makes fails.
func TestExportEncodeFailure(t *testing.T) {
	p := buildProcess(t, 1)

	// One successful write lets the XML header through; everything the
	// encoder writes afterwards fails.
	w := &failAfterWriter{okWrites: 1}

	err := (exporter{}).Export(context.Background(), w, p)
	if err == nil || !strings.Contains(err.Error(), "couldn't encode process") {
		t.Fatalf("Export = %v, want an encode failure", err)
	}

	if !errors.Is(err, io.ErrShortWrite) {
		t.Errorf("Export error = %v, want io.ErrShortWrite cause", err)
	}
}

// TestExportUnsupportedNode covers the export-side unsupported-element
// feedback: a node outside the §FR-8 subset yields UnsupportedElementError.
func TestExportUnsupportedNode(t *testing.T) {
	p := buildProcess(t, 1)

	sub, err := activities.NewSubProcess("sub", foundation.WithID("sub"))
	if err != nil {
		t.Fatalf("NewSubProcess: %v", err)
	}

	if err := p.Add(sub); err != nil {
		t.Fatalf("Add subprocess: %v", err)
	}

	err = (exporter{}).Export(context.Background(), &bytes.Buffer{}, p)

	var uee *convert.UnsupportedElementError
	if !errors.As(err, &uee) {
		t.Fatalf("Export = %v, want *convert.UnsupportedElementError", err)
	}
}
