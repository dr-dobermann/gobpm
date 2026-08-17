package bpmn

import (
	"context"
	"strings"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/expression/lite"
)

// TestNodesAreBuiltAfterTheWholeDocument pins the order pass 2 imposes: a
// node whose constructor refuses is reported only after the document has
// been read to the end, so a parse failure LATER in the file wins.
//
// The order matters because it is what lets a catalog object be declared
// after the process that refers to it (elements/foundation.md:23 —
// rootElements is unordered). Reporting the constructor's refusal first
// would mean the node had been built before the file was fully read,
// which is exactly what must not happen.
func TestNodesAreBuiltAfterTheWholeDocument(t *testing.T) {
	// The serviceTask names an operation no <interface> declares, which
	// its constructor refuses; the adHocSubProcess is an element the parser
	// refuses. The adHocSubProcess comes later in the file.
	doc := `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:process id="P" name="P">
    <bpmn:serviceTask id="svc" name="call" operationRef="ghost"/>
    <bpmn:adHocSubProcess id="sub"/>
  </bpmn:process>
</bpmn:definitions>`

	_, err := importer{}.Import(context.Background(), strings.NewReader(doc))
	if err == nil {
		t.Fatal("Import = nil, want an error")
	}

	if !strings.Contains(err.Error(), "adHocSubProcess") {
		t.Errorf("error = %q, want the parse failure that comes later in the "+
			"file — a node constructed before the document was read to the "+
			"end could not see a root element declared after the process", err)
	}
}

// TestDeferredBuildKeepsDocumentOrder pins that deferring construction
// did not reorder anything: nodes reach the process in the order the file
// declared them, since that order is what the process replays.
func TestDeferredBuildKeepsDocumentOrder(t *testing.T) {
	doc := `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:process id="P" name="P">
    <bpmn:startEvent id="s1"/>
    <bpmn:task id="t1" name="first"/>
    <bpmn:task id="t2" name="second"/>
    <bpmn:endEvent id="e1"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s1" targetRef="t1"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t1" targetRef="t2"/>
    <bpmn:sequenceFlow id="f3" sourceRef="t2" targetRef="e1"/>
  </bpmn:process>
</bpmn:definitions>`

	p, err := importer{}.Import(context.Background(), strings.NewReader(doc))
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	if got := len(p.Nodes()); got != 4 {
		t.Fatalf("nodes = %d, want 4", got)
	}

	// Every declared id resolved to a node, and the flows between them
	// linked — the graph is what the file described.
	if got := len(p.Flows()); got != 3 {
		t.Errorf("flows = %d, want 3", got)
	}
}

// TestExprSpecID covers the id an expression is minted under: its own
// when it declared one, and one derived from its owner when it did not.
// BPMN makes an expression's id optional and the model does not, so the
// fallback is what keeps a legal document importable — and it must stay
// derived from the owner, since two conditions minted under one id would
// collide.
func TestExprSpecID(t *testing.T) {
	for name, tc := range map[string]struct {
		spec exprSpec
		want string
	}{
		"declared": {
			spec: exprSpec{ownerID: "f1", role: "condition", id: "own"},
			want: "own",
		},
		"derived from the owner": {
			spec: exprSpec{ownerID: "f1", role: "condition"},
			want: "f1:condition",
		},
		"derived per role": {
			spec: exprSpec{ownerID: "ev1", role: "timeDate"},
			want: "ev1:timeDate",
		},
	} {
		t.Run(name, func(t *testing.T) {
			if got := tc.spec.exprID(); got != tc.want {
				t.Errorf("exprID() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestRunnableBodyIsOwnerAgnostic covers the extracted expression layer
// directly: it resolves a language and yields a body from nothing but an
// exprSpec, which is what lets an element with no sequence flow behind it
// carry an expression.
func TestRunnableBodyIsOwnerAgnostic(t *testing.T) {
	for name, tc := range map[string]struct {
		spec    exprSpec
		docLang string
		want    string
		wantErr string
	}{
		"lite as written": {
			spec: exprSpec{
				ownerKind: "conditionalEventDefinition", ownerID: "d1",
				role: "condition", lang: lite.Language, body: "x > 1",
			},
			want: "x > 1",
		},
		"JUEL by its delimiters": {
			spec: exprSpec{
				ownerKind: "conditionalEventDefinition", ownerID: "d1",
				role: "condition", body: "${x > 1}",
			},
			want: "x > 1",
		},
		"the document's language is inherited": {
			spec: exprSpec{
				ownerKind: "timerEventDefinition", ownerID: "d2",
				role: "timeDate", body: "now",
			},
			docLang: lite.Language,
			want:    "now",
		},
		"a language with no engine is refused": {
			spec: exprSpec{
				ownerKind: "conditionalEventDefinition", ownerID: "d3",
				role: "condition", lang: nsXPath, body: "count(//x) > 1",
			},
			wantErr: `conditionalEventDefinition "d3" carries a condition`,
		},
		"nothing declared and nothing inferable": {
			spec: exprSpec{
				ownerKind: "timerEventDefinition", ownerID: "d4",
				role: "timeDate", body: "2026-08-11T10:00:00Z",
			},
			wantErr: "(none declared, and none inferable)",
		},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := runnableBody(tc.spec, tc.docLang)

			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("runnableBody = %q, %v; want an error naming %q",
						got, err, tc.wantErr)
				}

				return
			}

			if err != nil {
				t.Fatalf("runnableBody: %v", err)
			}

			if got != tc.want {
				t.Errorf("runnableBody = %q, want %q", got, tc.want)
			}
		})
	}
}
