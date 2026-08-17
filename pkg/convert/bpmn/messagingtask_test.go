package bpmn

import (
	"strings"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
)

// messagingDoc puts one messaging task in a linear process.
func messagingDoc(tag, attrs string) string {
	return `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">` +
		catalogRoots + `
  <bpmn:process id="P" name="P">
    <bpmn:startEvent id="s1"/>
    <bpmn:` + tag + ` id="t1" name="exchange"` + attrs + `/>
    <bpmn:endEvent id="e1"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s1" targetRef="t1"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t1" targetRef="e1"/>
  </bpmn:process>
</bpmn:definitions>`
}

// TestMessagingTasksImport covers SRD-089.D §6 T-12 (FR-6): both tasks
// resolve messageRef against the catalog. .C refused them for exactly one
// reason — the catalog did not exist — and that reason is now gone.
func TestMessagingTasksImport(t *testing.T) {
	for _, tag := range []string{tagSendTask, tagReceiveTask} {
		t.Run(tag, func(t *testing.T) {
			r, err := importEventDoc(t, messagingDoc(tag, ` messageRef="m1"`))
			if err != nil {
				t.Fatalf("Import of a %s: %v", tag, err)
			}

			n := nodeByID(t, r, "t1")

			if got := n.Name(); got != "exchange" {
				t.Errorf("name = %q, want %q", got, "exchange")
			}

			m, ok := n.(interface{ Message() *bpmncommon.Message })
			if !ok {
				t.Fatalf("%T exposes no Message()", n)
			}

			if got := m.Message(); got == nil || got.ID() != "m1" {
				t.Errorf("Message() = %v, want the catalog's m1 — the task must "+
					"carry the message the file named, not merely construct", got)
			}
		})
	}
}

// TestReceiveTaskInstantiate pins BPMN's instantiate attribute, which
// marks a receive task that STARTS an instance rather than waiting inside
// one. Its default is false, and only a non-default value produces an
// option.
func TestReceiveTaskInstantiate(t *testing.T) {
	for name, tc := range map[string]struct {
		attrs string
		want  bool
	}{
		"default":           {attrs: ` messageRef="m1"`, want: false},
		"instantiate=true":  {attrs: ` messageRef="m1" instantiate="true"`, want: true},
		"instantiate=false": {attrs: ` messageRef="m1" instantiate="false"`, want: false},
	} {
		t.Run(name, func(t *testing.T) {
			r, err := importEventDoc(t, messagingDoc(tagReceiveTask, tc.attrs))
			if err != nil {
				t.Fatalf("Import: %v", err)
			}

			n := nodeByID(t, r, "t1")

			i, ok := n.(interface{ Instantiate() bool })
			if !ok {
				t.Fatalf("%T exposes no Instantiate()", n)
			}

			if got := i.Instantiate(); got != tc.want {
				t.Errorf("Instantiate() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestMessagingTaskMessageRef covers FR-6 and FR-7: the message is
// required, and a reference that does not resolve says whether the id was
// missing or of the wrong kind.
func TestMessagingTaskMessageRef(t *testing.T) {
	for name, tc := range map[string]struct{ attrs, want string }{
		"missing": {
			attrs: ``,
			want:  "names no messageRef",
		},
		"unknown id": {
			attrs: ` messageRef="ghost"`,
			want:  `references message "ghost" in "messageRef"`,
		},
		"a signal is not a message": {
			attrs: ` messageRef="sig1"`,
			want:  `"sig1" is a signal`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := importEventDoc(t, messagingDoc(tagSendTask, tc.attrs))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Import = %v, want an error containing %q", err, tc.want)
			}
		})
	}
}

// TestMessagingTaskUnmappedAttrsAreReported pins that the two attributes
// the model cannot hold are reported rather than dropped. A SendTask's
// implementation field has no setter, so reading the attribute back would
// report a mechanism the engine never used; operationRef binds the task
// to a service operation neither constructor takes.
func TestMessagingTaskUnmappedAttrsAreReported(t *testing.T) {
	r, err := importEventDoc(t, messagingDoc(tagSendTask,
		` messageRef="m1" implementation="##WebService" operationRef="op1"`))
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	got := map[string]bool{}
	for _, d := range r.Dropped {
		if d.Element == "t1" {
			got[d.Construct] = true
		}
	}

	for _, attr := range []string{"implementation", "operationRef"} {
		if !got[attr] {
			t.Errorf("dropped = %v, want an entry for %q — an attribute the "+
				"model cannot hold is reported, not discarded", r.Dropped, attr)
		}
	}
}
