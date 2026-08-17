package bpmn

import (
	"strings"
	"testing"
)

// TestOneIDLedger pins claimID's reason to exist: the parser's per-kind
// tables — flow elements, catalog objects, item definitions, stores,
// operations, sequence flows — hold ONE id vocabulary, because every
// reference attribute resolves by id across them. Before the ledger a
// cross-table duplicate imported silently, and whichever element the
// resolver's probe order reached first won every reference.
func TestOneIDLedger(t *testing.T) {
	tests := map[string]struct {
		decls, body string
	}{
		"dataStore reuses a node id": {
			decls: `  <bpmn:dataStore id="s1"/>`,
		},
		"dataObject reuses an itemDefinition id": {
			decls: `  <bpmn:itemDefinition id="x1"/>`,
			body:  `    <bpmn:dataObject id="x1" name="o"/>`,
		},
		"task reuses a message id": {
			decls: `  <bpmn:message id="m1" name="M"/>`,
			body:  `    <bpmn:task id="m1" name="T"/>`,
		},
		"node reuses a sequenceFlow id": {
			body: `    <bpmn:task id="f1" name="T"/>`,
		},
		"two sequenceFlows share an id": {
			body: `    <bpmn:task id="t1" name="T"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s1" targetRef="t1"/>`,
		},
		"two interfaces share an id": {
			decls: `  <bpmn:interface id="i1" name="A"/>
  <bpmn:interface id="i1" name="B"/>`,
		},
		"operation reuses an interface id": {
			decls: `  <bpmn:interface id="i1" name="A">
    <bpmn:operation id="i1" name="op"/>
  </bpmn:interface>`,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := importEventDoc(t, dataDoc(tc.decls, tc.body))
			if err == nil || !strings.Contains(err.Error(), "duplicate id") {
				t.Fatalf("error = %v, want the ledger's duplicate-id refusal", err)
			}
		})
	}
}

// TestRootElementAfterTheProcessJoinsTheLedger: BPMN orders root elements
// freely, so the guard must hold whichever side of the <process> the
// second declaration lands on.
func TestRootElementAfterTheProcessJoinsTheLedger(t *testing.T) {
	_, err := importEventDoc(t, `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:process id="P" name="P">
    <bpmn:startEvent id="s1"/>
  </bpmn:process>
  <bpmn:itemDefinition id="s1"/>
</bpmn:definitions>`)
	if err == nil || !strings.Contains(err.Error(), "duplicate id") {
		t.Fatalf("error = %v, want the ledger's duplicate-id refusal", err)
	}
}
