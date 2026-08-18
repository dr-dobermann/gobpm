package bpmn

import (
	"strings"
	"testing"
)

// The independent-review sweep's regression pins (SRD-089.H/.I
// pre-merge review): each test fails with its fix reverted.

// TestComplexMultipleDefsRefused: a <complexBehaviorDefinition> event
// carrying several event definitions refuses instead of silently
// keeping the first.
func TestComplexMultipleDefsRefused(t *testing.T) {
	_, err := importEventDoc(t, propDoc(
		`  <bpmn:signal id="sig1" name="S"/>`,
		`    <bpmn:task id="t1" name="Handle">
      <bpmn:multiInstanceLoopCharacteristics id="mi1" behavior="Complex">
        <bpmn:loopCardinality language="gobpm:lite">2</bpmn:loopCardinality>
        <bpmn:complexBehaviorDefinition id="cb1">
          <bpmn:condition language="gobpm:lite">loopCounter &gt; 0</bpmn:condition>
          <bpmn:event id="ev1">
            <bpmn:signalEventDefinition id="sd1" signalRef="sig1"/>
            <bpmn:signalEventDefinition id="sd2" signalRef="sig1"/>
          </bpmn:event>
        </bpmn:complexBehaviorDefinition>
      </bpmn:multiInstanceLoopCharacteristics>
    </bpmn:task>
    <bpmn:sequenceFlow id="f2" sourceRef="s1" targetRef="t1"/>
    <bpmn:sequenceFlow id="f3" sourceRef="t1" targetRef="e1"/>`))
	if err == nil || !strings.Contains(err.Error(), "2 event definitions") {
		t.Fatalf("error = %v, want the exactly-one refusal naming the count",
			err)
	}
}

// TestOrphanInputItemRefused: an <inputDataItem> beside a cardinality —
// no loopDataInputRef — refuses like every other half-pair instead of
// being silently dropped.
func TestOrphanInputItemRefused(t *testing.T) {
	_, err := importEventDoc(t, miDoc(
		`      <bpmn:multiInstanceLoopCharacteristics id="mi1">
        <bpmn:loopCardinality language="gobpm:lite">2</bpmn:loopCardinality>
        <bpmn:inputDataItem id="item1" name="order"/>
      </bpmn:multiInstanceLoopCharacteristics>`))
	if err == nil || !strings.Contains(err.Error(), "pair it with a loopDataInputRef") {
		t.Fatalf("error = %v, want the orphaned-item refusal", err)
	}
}

// TestUnnameableItemsRefused: an item element with neither name nor id
// is named as PRESENT but unnameable — not claimed absent.
func TestUnnameableItemsRefused(t *testing.T) {
	tests := map[string]struct {
		marker string
		want   string
	}{
		"input item": {
			marker: `      <bpmn:multiInstanceLoopCharacteristics id="mi1">
        <bpmn:loopDataInputRef>do1</bpmn:loopDataInputRef>
        <bpmn:inputDataItem/>
      </bpmn:multiInstanceLoopCharacteristics>`,
			want: "<inputDataItem> with neither a name nor an id",
		},
		"output item": {
			marker: `      <bpmn:multiInstanceLoopCharacteristics id="mi1">
        <bpmn:loopCardinality language="gobpm:lite">2</bpmn:loopCardinality>
        <bpmn:loopDataOutputRef>do1</bpmn:loopDataOutputRef>
        <bpmn:outputDataItem/>
      </bpmn:multiInstanceLoopCharacteristics>`,
			want: "<outputDataItem> with neither a name nor an id",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := importEventDoc(t, miDoc(tc.marker))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

// TestCollabDialectAttrNamesTheCollab: an unmapped dialect attribute on
// <collaboration> is reported against the collaboration's own identity,
// not an empty owner.
func TestCollabDialectAttrNamesTheCollab(t *testing.T) {
	res, err := importEventDoc(t, `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:camunda="http://camunda.org/schema/1.0/bpmn">
  <bpmn:collaboration id="c1" camunda:historyTimeToLive="30">
    <bpmn:participant id="pa1" processRef="P"/>
  </bpmn:collaboration>
  <bpmn:process id="P" name="only">
    <bpmn:startEvent id="s1"/>
  </bpmn:process>
</bpmn:definitions>`)
	if err != nil {
		t.Fatalf("ImportDocument: %v", err)
	}

	found := false

	for _, d := range res.Dropped {
		if d.Construct == "camunda:historyTimeToLive" {
			found = true

			if d.Element != "c1" {
				t.Errorf("Element = %q, want the collaboration's id", d.Element)
			}
		}
	}

	if !found {
		t.Fatal("the dialect attribute on <collaboration> was not reported")
	}
}
