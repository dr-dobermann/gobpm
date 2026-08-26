package bpmn

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/convert"
	"github.com/dr-dobermann/gobpm/pkg/model/gateways"
)

// TestVisualArtifactsAreCarried — once SRD-089.A T-6's skip disposition,
// re-decided by ADR-039: the annotation and the group are CARRIED into the
// model-only artifact tier, the category is consumed as their resolution
// input, and none of them becomes a node.
func TestVisualArtifactsAreCarried(t *testing.T) {
	doc := `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:category id="c1" name="Ops">
    <bpmn:categoryValue id="cv1" value="urgent"/>
  </bpmn:category>
  <bpmn:relationship id="r1" type="x"/>
  <bpmn:import importType="http://www.w3.org/2001/XMLSchema" location="types.xsd" namespace="urn:t"/>
  <bpmn:process id="P" name="P">
    <bpmn:startEvent id="s1"/>
    <bpmn:task id="t1" name="work"/>
    <bpmn:endEvent id="e1"/>
    <bpmn:textAnnotation id="a1"><bpmn:text>a note</bpmn:text></bpmn:textAnnotation>
    <bpmn:group id="g1" categoryValueRef="cv1"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s1" targetRef="t1"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t1" targetRef="e1"/>
  </bpmn:process>
</bpmn:definitions>`

	p, err := importer{}.Import(context.Background(), strings.NewReader(doc))
	if err != nil {
		t.Fatalf("Import of a file carrying annotations: %v", err)
	}

	// The flow graph is exactly what it would be without them.
	if got := len(p.Nodes()); got != 3 {
		t.Errorf("nodes = %d, want 3 — an annotation must not become a node", got)
	}

	if got := len(p.Flows()); got != 2 {
		t.Errorf("flows = %d, want 2", got)
	}

	// ...and the artifacts are held, not dropped (ADR-039).
	arts := p.Artifacts()
	if len(arts) != 2 {
		t.Fatalf("artifacts = %d, want the annotation and the group", len(arts))
	}

	if arts[0].ID() != "a1" || arts[1].ID() != "g1" {
		t.Errorf("artifacts = %q/%q, want a1/g1", arts[0].ID(), arts[1].ID())
	}
}

// TestAssociationIsNotAnAnnotation guards the one artifact that must NOT
// join the skip list. The extract keeps Association in scope precisely
// because it carries compensation semantics, so silently dropping it
// would drop the link between a compensating activity and its handler.
func TestAssociationIsNotAnAnnotation(t *testing.T) {
	doc := `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:process id="P" name="P">
    <bpmn:startEvent id="s1"/>
    <bpmn:endEvent id="e1"/>
    <bpmn:association id="a1" sourceRef="s1" targetRef="e1"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s1" targetRef="e1"/>
  </bpmn:process>
</bpmn:definitions>`

	_, err := importer{}.Import(context.Background(), strings.NewReader(doc))

	var uee *convert.UnsupportedElementError
	if !errors.As(err, &uee) {
		t.Fatalf("Import with an association = %v, want it refused, not skipped", err)
	}

	if uee.Tag != tagAssociation {
		t.Errorf("refused tag = %q, want %q", uee.Tag, tagAssociation)
	}
}

// TestChoreographyAndConversationAreRefused covers SRD-089.A §6 T-7
// (FR-8): they belong to separate conformance sub-classes, and a
// Choreography is not a Process — skipping one would import a different
// diagram than the modeler drew.
func TestChoreographyAndConversationAreRefused(t *testing.T) {
	tags := map[string]string{
		"choreography":           "",
		"globalChoreographyTask": "",
		"conversation":           "§9.5.1",
		"callConversation":       "§9.5.1",
		"globalConversation":     "§9.5.1",
	}

	for tag, section := range tags {
		t.Run(tag, func(t *testing.T) {
			doc := fmt.Sprintf(`<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="%s">
  <bpmn:%s id="x"/>
  <bpmn:process id="P" name="P">
    <bpmn:startEvent id="s1"/>
    <bpmn:endEvent id="e1"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s1" targetRef="e1"/>
  </bpmn:process>
</bpmn:definitions>`, nsBPMN, tag)

			_, err := importer{}.Import(context.Background(), strings.NewReader(doc))

			var uee *convert.UnsupportedElementError
			if !errors.As(err, &uee) {
				t.Fatalf("Import of <%s> = %v, want UnsupportedElementError", tag, err)
			}

			if uee.Section != section {
				t.Errorf("<%s> refused with section %q, want %q — a section "+
					"asserted from memory is worse feedback than none",
					tag, uee.Section, section)
			}
		})
	}
}

// TestParallelGatewayDefaultIsIgnoredOnImport pins what import does with
// an attribute BPMN does not define on the element.
//
// The export side refuses to write `default` on a parallel gateway
// (§13.4.1 defines none), and its test reaches that state programmatically
// because the importer cannot produce it — which left the import side's
// behaviour unpinned. It is IGNORED, deliberately and consistently: the
// importer silently ignores every attribute it does not map (isExecutable,
// startQuantity, isImmediate and the rest), and singling this one out for
// a refusal would be inconsistent with the other twenty. What must not
// happen is the gateway acquiring a default flow from an attribute the
// standard does not give it.
func TestParallelGatewayDefaultIsIgnoredOnImport(t *testing.T) {
	doc := `<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <bpmn:process id="P" name="P">
    <bpmn:startEvent id="s1"/>
    <bpmn:parallelGateway id="g1" default="f2"/>
    <bpmn:endEvent id="e1"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s1" targetRef="g1"/>
    <bpmn:sequenceFlow id="f2" sourceRef="g1" targetRef="e1"/>
  </bpmn:process>
</bpmn:definitions>`

	p, err := importer{}.Import(context.Background(), strings.NewReader(doc))
	if err != nil {
		t.Fatalf("Import: %v", err)
	}

	var pg *gateways.ParallelGateway

	for _, n := range p.Nodes() {
		if g, ok := n.(*gateways.ParallelGateway); ok {
			pg = g
		}
	}

	if pg == nil {
		t.Fatal("parallel gateway missing after import")
	}

	if df := pg.DefaultFlow(); df != nil {
		t.Errorf("parallel gateway acquired default flow %q from an attribute "+
			"BPMN §13.4.1 does not define on it", df.ID())
	}

	// And it must not reappear on the way out.
	if out := exportOnce(t, doc); strings.Contains(out, "default=") {
		t.Errorf("export re-emitted a default on the parallel gateway:\n%s", out)
	}
}
