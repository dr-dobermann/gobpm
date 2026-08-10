package bpmn

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/convert"
)

// TestDispositionForEveryContext pins the disposition of every element
// name the converter classifies, in every context that can carry it —
// the before/after equivalence for SRD-089.A M1, written from the
// behaviour of the six switches the dispatch table replaced.
func TestDispositionForEveryContext(t *testing.T) {
	tests := map[string]struct {
		local string
		ctx   parseCtx
		want  dispositionKind
	}{
		"documentation under definitions":     {tagDocumentation, ctxDefinitions, skipped},
		"extensionElements under definitions": {tagExtensionElems, ctxDefinitions, skipped},
		"documentation under process":         {tagDocumentation, ctxProcess, skipped},
		"extensionElements under process":     {tagExtensionElems, ctxProcess, skipped},
		"documentation under node":            {tagDocumentation, ctxNode, skipped},
		"extensionElements under node":        {tagExtensionElems, ctxNode, skipped},
		"documentation under sequenceFlow":    {tagDocumentation, ctxSequenceFlow, skipped},
		"documentation under interface":       {tagDocumentation, ctxInterface, skipped},
		"documentation under operation":       {tagDocumentation, ctxOperation, skipped},

		"incoming under node": {tagIncoming, ctxNode, skipped},
		"outgoing under node": {tagOutgoing, ctxNode, skipped},

		"errorRef under operation": {tagErrorRef, ctxOperation, skipped},

		// The refusals: an unmapped element is refused in every context,
		// and the zero value of dispositionKind is what makes absence
		// from the tables mean refusal rather than silent acceptance.
		"subProcess under process":        {"subProcess", ctxProcess, refused},
		"boundaryEvent under process":     {"boundaryEvent", ctxProcess, refused},
		"collaboration under definitions": {"collaboration", ctxDefinitions, refused},
		"timerEventDefinition under node": {"timerEventDefinition", ctxNode, refused},
		"invented under operation":        {"inventedChild", ctxOperation, refused},

		// Context matters: incoming is skipped inside a node and refused
		// anywhere else, so the key really is the pair.
		"incoming under process": {tagIncoming, ctxProcess, refused},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			if got := dispositionFor(tc.ctx, tc.local); got != tc.want {
				t.Errorf("dispositionFor(%d, %q) = %d, want %d",
					tc.ctx, tc.local, got, tc.want)
			}
		})
	}
}

// TestOperationChildIsDeclaredNotLenient covers the one behaviour change
// M1 makes deliberately. The old parseOperationChild skipped ANY unknown
// in-namespace child; the standard gives <operation> exactly three
// (inMessageRef, outMessageRef, errorRef), so all three are declared and
// anything else is now refused rather than swallowed.
func TestOperationChildIsDeclaredNotLenient(t *testing.T) {
	doc := func(child string) string {
		return fmt.Sprintf(`<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="%s">
  <bpmn:interface id="i1" name="Svc">
    <bpmn:operation id="op1" name="charge">%s</bpmn:operation>
  </bpmn:interface>
  <bpmn:process id="P" name="P">
    <bpmn:startEvent id="s"/>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f" sourceRef="s" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`, nsBPMN, child)
	}

	t.Run("errorRef is skipped by declaration", func(t *testing.T) {
		_, err := importer{}.Import(context.Background(),
			strings.NewReader(doc(`<bpmn:errorRef>err1</bpmn:errorRef>`)))
		if err != nil {
			t.Fatalf("Import with errorRef: %v", err)
		}
	})

	t.Run("an undeclared child is refused", func(t *testing.T) {
		_, err := importer{}.Import(context.Background(),
			strings.NewReader(doc(`<bpmn:inventedChild id="x"/>`)))

		var uee *convert.UnsupportedElementError
		if !errors.As(err, &uee) {
			t.Fatalf("Import with an undeclared operation child = %v, want UnsupportedElementError", err)
		}

		if uee.Tag != "inventedChild" {
			t.Errorf("refused tag = %q, want %q", uee.Tag, "inventedChild")
		}
	})

	t.Run("a foreign-namespace child stays silent", func(t *testing.T) {
		_, err := importer{}.Import(context.Background(),
			strings.NewReader(doc(`<x:anything xmlns:x="urn:x"/>`)))
		if err != nil {
			t.Fatalf("Import with a foreign operation child: %v", err)
		}
	})
}
