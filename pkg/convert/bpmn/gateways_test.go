package bpmn

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/gateways"
)

// gatewayDoc is a process whose single gateway is the element under test,
// with two outgoing flows so a default has something to name.
func gatewayDoc(kind, attrs string) string {
	return fmt.Sprintf(`<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="%s">
  <bpmn:process id="P" name="P">
    <bpmn:startEvent id="s1"/>
    <bpmn:%s id="g1" %s/>
    <bpmn:endEvent id="e1"/>
    <bpmn:endEvent id="e2"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s1" targetRef="g1"/>
    <bpmn:sequenceFlow id="f2" sourceRef="g1" targetRef="e1"/>
    <bpmn:sequenceFlow id="f3" sourceRef="g1" targetRef="e2"/>
  </bpmn:process>
</bpmn:definitions>`, nsBPMN, kind, attrs)
}

// TestImportGatewayKinds covers SRD-089.C §FR-3: four of the five gateways
// import, each as its own model type.
func TestImportGatewayKinds(t *testing.T) {
	tests := map[string]struct {
		kind string
		want string
	}{
		"exclusive":  {tagExclusiveGateway, "*gateways.ExclusiveGateway"},
		"parallel":   {tagParallelGateway, "*gateways.ParallelGateway"},
		"inclusive":  {tagInclusiveGateway, "*gateways.InclusiveGateway"},
		"eventBased": {tagEventBasedGtw, "*gateways.EventBasedGateway"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			p, err := importer{}.Import(context.Background(),
				strings.NewReader(gatewayDoc(tc.kind, `gatewayDirection="Diverging"`)))
			if err != nil {
				t.Fatalf("Import of <%s>: %v", tc.kind, err)
			}

			for _, n := range p.Nodes() {
				if n.ID() != "g1" {
					continue
				}

				if got := fmt.Sprintf("%T", n); got != tc.want {
					t.Errorf("<%s> imported as %s, want %s", tc.kind, got, tc.want)
				}

				return
			}

			t.Fatalf("<%s> produced no node g1", tc.kind)
		})
	}
}

// TestGatewayDefaultOnlyWhereTheStandardGivesOne pins which kinds may carry
// a default.
//
// A parallel gateway takes every outgoing flow and an event-based one is
// decided by its events, so neither has a condition to fall through —
// BPMN gives them no default attribute. The model, though, keeps
// UpdateDefaultFlow on the shared base, so nothing in the type system
// stops a converter from setting one. This is that stop.
func TestGatewayDefaultOnlyWhereTheStandardGivesOne(t *testing.T) {
	withDefault := map[string]bool{
		tagExclusiveGateway: true,
		tagInclusiveGateway: true,
		tagParallelGateway:  false,
		tagEventBasedGtw:    false,
	}

	for kind, takes := range withDefault {
		t.Run(kind, func(t *testing.T) {
			p, err := importer{}.Import(context.Background(),
				strings.NewReader(gatewayDoc(kind, `default="f3"`)))
			if err != nil {
				t.Fatalf("Import of <%s default=…>: %v", kind, err)
			}

			// Read the default back through the concrete kinds.
			var got string

			for _, n := range p.Nodes() {
				switch v := n.(type) {
				case *gateways.ExclusiveGateway:
					if d := v.DefaultFlow(); d != nil {
						got = d.ID()
					}
				case *gateways.InclusiveGateway:
					if d := v.DefaultFlow(); d != nil {
						got = d.ID()
					}
				case *gateways.ParallelGateway:
					if d := v.DefaultFlow(); d != nil {
						got = d.ID()
					}
				case *gateways.EventBasedGateway:
					if d := v.DefaultFlow(); d != nil {
						got = d.ID()
					}
				}
			}

			if takes && got != "f3" {
				t.Errorf("<%s> default = %q, want f3", kind, got)
			}

			if !takes && got != "" {
				t.Errorf("<%s> acquired default %q, which BPMN does not give it",
					kind, got)
			}
		})
	}
}

// TestGatewayConstructorGuards covers the two guards the dispatch tables
// make unreachable from a document — each testable directly, and each
// stating something true about the design rather than padding coverage.
func TestGatewayConstructorGuards(t *testing.T) {
	t.Run("the complex gateway has no constructor here", func(t *testing.T) {
		// Deliberate, not missing: SRD-089.C §4.1. BPMN carries activation
		// as an expression; the model wants per-gate token counts, and one
		// cannot be recovered from the other.
		_, err := newGatewayOfKind(tagComplexGateway, nil)
		if err == nil {
			t.Fatal("newGatewayOfKind(complexGateway) succeeded; §4.1 says it must not")
		}

		if !strings.Contains(err.Error(), tagComplexGateway) {
			t.Errorf("error %q does not name the kind", err)
		}
	})

	t.Run("a node that cannot hold a default is refused", func(t *testing.T) {
		// Every gateway the model has today embeds the shared Gateway, so
		// this cannot arise from a document — but nothing in the type
		// system says a future kind must, and a gateway that silently kept
		// no default would route by a rule the file never described.
		start, err := newStartEventNode(t)
		if err != nil {
			t.Fatalf("start event: %v", err)
		}

		asm := &assembly{}

		if err := deferDefaultFlow(asm, "someGateway", "g9", "f1", start); err == nil {
			t.Fatal("deferDefaultFlow accepted a node with no UpdateDefaultFlow")
		}

		if len(asm.refs) != 0 {
			t.Error("a refused default still recorded a pending reference")
		}
	})
}

// newStartEventNode builds a node that is deliberately not a gateway.
func newStartEventNode(t *testing.T) (flow.Node, error) {
	t.Helper()

	return events.NewStartEvent("s", foundation.WithID("s"))
}
