package bpmn

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/convert"
	"github.com/dr-dobermann/gobpm/pkg/errs"
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
		"extensionElements under process":     {tagExtensionElems, ctxProcess, skipped},
		"extensionElements under node":        {tagExtensionElems, ctxNode, skipped},
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

// TestDocumentationIsClaimedWhereTheModelHoldsIt pins which side of the
// dispatch answers for <documentation>. Under a flow node and a sequence
// flow a parser table claims it, so it never reaches dispositionFor; under
// <process> parseProcess intercepts it, because the process is built
// lazily and its documentation must arrive as a construction option. The
// contexts with no model element to attach it to keep skipping it.
func TestDocumentationIsClaimedWhereTheModelHoldsIt(t *testing.T) {
	if _, ok := nodeChildParsers[tagDocumentation]; !ok {
		t.Error("a flow node's documentation is not claimed by nodeChildParsers")
	}

	if _, ok := sequenceFlowParsers[tagDocumentation]; !ok {
		t.Error("a sequence flow's documentation is not claimed by sequenceFlowParsers")
	}

	for _, ctx := range []parseCtx{ctxDefinitions, ctxInterface, ctxOperation} {
		if got := dispositionFor(ctx, tagDocumentation); got != skipped {
			t.Errorf("dispositionFor(%d, documentation) = %d, want skipped — "+
				"there is no model element in that context to carry it", ctx, got)
		}
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

// TestDeferredRefDiagnostics covers SRD-089.A §6 T-8/T-9: a forward
// reference that cannot be resolved names both ends and the attribute,
// and a reference whose target exists but is the WRONG KIND says so
// rather than claiming the id is missing.
func TestDeferredRefDiagnostics(t *testing.T) {
	doc := func(def string) string {
		return fmt.Sprintf(`<?xml version="1.0"?>
<bpmn:definitions xmlns:bpmn="%s">
  <bpmn:process id="P" name="P">
    <bpmn:startEvent id="s"/>
    <bpmn:exclusiveGateway id="g" default="%s"/>
    <bpmn:endEvent id="e1"/>
    <bpmn:endEvent id="e2"/>
    <bpmn:sequenceFlow id="f0" sourceRef="s" targetRef="g"/>
    <bpmn:sequenceFlow id="f1" sourceRef="g" targetRef="e1"/>
    <bpmn:sequenceFlow id="f2" sourceRef="g" targetRef="e2"/>
  </bpmn:process>
</bpmn:definitions>`, nsBPMN, def)
	}

	t.Run("an undeclared target names both ends", func(t *testing.T) {
		_, err := importer{}.Import(context.Background(), strings.NewReader(doc("ghost")))
		if err == nil {
			t.Fatal("Import with an undeclared default: want an error")
		}

		for _, want := range []string{"exclusiveGateway g", `"default"`, `"ghost"`, "no such element is declared"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q does not mention %q", err, want)
			}
		}

		assertClass(t, err, errs.ObjectNotFound)
	})

	t.Run("a target of the wrong kind is not reported as missing", func(t *testing.T) {
		// "s" IS declared — as a start event. Telling the author it does
		// not exist would send them hunting a typo that is not there.
		_, err := importer{}.Import(context.Background(), strings.NewReader(doc("s")))
		if err == nil {
			t.Fatal("Import with a default naming a node: want an error")
		}

		if strings.Contains(err.Error(), "no such element is declared") {
			t.Errorf("error %q reports a declared id as missing", err)
		}

		if !strings.Contains(err.Error(), "is a flow node") {
			t.Errorf("error %q does not say the target is a flow node", err)
		}

		assertClass(t, err, errs.TypeCastingError)
	})
}

// assertClass fails unless err is a converter ApplicationError carrying
// class — the class is what a host branches on, so an error whose text
// improved but whose class drifted is still a break.
func assertClass(t *testing.T, err error, class string) {
	t.Helper()

	var ae *errs.ApplicationError
	if !errors.As(err, &ae) {
		t.Fatalf("error is %T, want *errs.ApplicationError", err)
	}

	if !ae.HasClass(errorClass) || !ae.HasClass(class) {
		t.Errorf("error classes = %v, want %s and %s", ae, errorClass, class)
	}
}
