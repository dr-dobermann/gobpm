package bpmn

import (
	"encoding/xml"
	"strconv"
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/artifacts"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

// The §8.4.1 artifacts are model-only carriers (ADR-039): parsed and
// preserved into the container's artifact collection, never executed. The
// parsers here are registered in processParsers, so an artifact inside a
// <subProcess> works with no second registration — the dataElements
// arrangement (SRD-092 FR-7).

// annotationSpec is a <textAnnotation> as read, buffered for pass 2 like
// every other spec: its container may not be constructible until then.
type annotationSpec struct {
	id, text, textFormat string
	// container is the id of the declaring container, "" for the process —
	// the same convention as nodeSpec (SRD-089.E §4.1).
	container string
}

// groupSpec is a <group> as read; categoryValueRef resolves in pass 2
// through the document's category-value lookup (SRD-092 FR-8).
type groupSpec struct {
	id, categoryValueRef, container string
}

// parseTextAnnotationElem records one <textAnnotation> for pass 2: its
// optional id (claimed into the document ledger when declared), the
// textFormat attribute, and the <text> child's character data.
func parseTextAnnotationElem(
	p *parser, asm *assembly, se xml.StartElement,
) error {
	id := strings.TrimSpace(attrValue(se, "id"))
	if id != "" {
		if err := p.claimID(id, se.Name.Local); err != nil {
			return err
		}
	}

	spec := annotationSpec{
		id:         id,
		textFormat: strings.TrimSpace(attrValue(se, "textFormat")),
		container:  p.container,
	}

	for {
		tok, err := p.token()
		if err != nil {
			return err
		}

		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Space == nsBPMN && t.Name.Local == tagText {
				text, err := p.readText(t)
				if err != nil {
					return err
				}

				spec.text = strings.TrimSpace(text)

				continue
			}

			if err := p.skipElement(); err != nil {
				return err
			}

		case xml.EndElement:
			if t.Name == se.Name {
				asm.annots = append(asm.annots, spec)

				return nil
			}
		}
	}
}

// parseGroupElem records one <group> for pass 2: its optional id (claimed
// when declared) and the categoryValueRef naming the value it represents.
func parseGroupElem(p *parser, asm *assembly, se xml.StartElement) error {
	id := strings.TrimSpace(attrValue(se, "id"))
	if id != "" {
		if err := p.claimID(id, se.Name.Local); err != nil {
			return err
		}
	}

	asm.groups = append(asm.groups, groupSpec{
		id:               id,
		categoryValueRef: strings.TrimSpace(attrValue(se, "categoryValueRef")),
		container:        p.container,
	})

	return p.skipElement()
}

// parseCategoryElem reads one definitions-level <category> as resolution
// input (ADR-039 §2.3): each <categoryValue>'s value joins the document
// lookup a group's categoryValueRef resolves through. No model element is
// created — the value a group represents is embedded in the group itself.
func parseCategoryElem(p *parser, se xml.StartElement) (*assembly, error) {
	id := strings.TrimSpace(attrValue(se, "id"))
	if id != "" {
		if err := p.claimID(id, se.Name.Local); err != nil {
			return nil, err
		}
	}

	for {
		tok, err := p.token()
		if err != nil {
			return nil, err
		}

		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Space == nsBPMN && t.Name.Local == tagCategoryValue {
				if err := p.recordCategoryValue(t); err != nil {
					return nil, err
				}

				continue
			}

			if err := p.skipElement(); err != nil {
				return nil, err
			}

		case xml.EndElement:
			if t.Name == se.Name {
				return nil, nil
			}
		}
	}
}

// recordCategoryValue claims one <categoryValue>'s id and records its value
// in the document lookup. A value without an id is unreferencable — nothing
// can resolve to it — so it is read and left unrecorded.
func (p *parser) recordCategoryValue(se xml.StartElement) error {
	id := strings.TrimSpace(attrValue(se, "id"))
	if id != "" {
		if err := p.claimID(id, se.Name.Local); err != nil {
			return err
		}

		p.categoryValues[id] = strings.TrimSpace(attrValue(se, "value"))
	}

	return p.skipElement()
}

// artifactHolder is what an artifact attaches to: the process, or the
// sub-process container declaring it.
type artifactHolder interface {
	AddArtifacts(...artifacts.Artifact) error
}

// artifactHolderFor resolves a spec's container id to the collection owner,
// the process for "".
func artifactHolderFor(asm *assembly, id string) (artifactHolder, error) {
	if id == "" {
		return asm.proc, nil
	}

	// Both guards are unreachable from any document — the containerFor
	// argument: a container id is set only for <subProcess>/<transaction>,
	// which the node pass builds into a *activities.SubProcess.
	n, ok := asm.byID[id]
	if !ok {
		return nil, errs.Invariant(
			"container %q holds artifacts but was never built", id)
	}

	h, ok := n.(artifactHolder)
	if !ok {
		return nil, errs.Invariant(
			"container %q is a %T, which holds no artifacts", id, n)
	}

	return h, nil
}

// withDeclaredID returns the base options for an artifact: its document id
// when it declared one, nothing otherwise (the model generates one).
func withDeclaredID(id string) []options.Option {
	if id == "" {
		return nil
	}

	return []options.Option{foundation.WithID(id)}
}

// buildCarriedArtifacts materializes the document's carrier artifacts:
// every textAnnotation, then every group with its categoryValueRef resolved
// to the value it embeds (SRD-092 FR-7/FR-8). A group whose ref resolves to
// nothing the document declared is dropped with a report (FR-10) — the file
// survives, and the host is told which reference failed.
func buildCarriedArtifacts(p *parser, asm *assembly) error {
	for _, s := range asm.annots {
		ta, err := artifacts.NewTextAnnotation(s.text, s.textFormat,
			withDeclaredID(s.id)...)
		if err != nil {
			return err
		}

		if err := attachArtifact(asm, s.container, ta); err != nil {
			return err
		}

		if s.id != "" {
			asm.artsByID[s.id] = ta
		}
	}

	for _, s := range asm.groups {
		value := ""

		if s.categoryValueRef != "" {
			v, ok := p.categoryValues[s.categoryValueRef]
			if !ok {
				p.report(s.id, tagGroup, groupRefLoss(s.categoryValueRef))

				continue
			}

			value = v
		}

		g, err := artifacts.NewGroup(value, withDeclaredID(s.id)...)
		if err != nil {
			return err
		}

		if err := attachArtifact(asm, s.container, g); err != nil {
			return err
		}

		if s.id != "" {
			asm.artsByID[s.id] = g
		}
	}

	return nil
}

// attachArtifact puts one built artifact on its declaring container.
func attachArtifact(
	asm *assembly, containerID string, a artifacts.Artifact,
) error {
	h, err := artifactHolderFor(asm, containerID)
	if err != nil {
		return err
	}

	return h.AddArtifacts(a)
}

// groupRefLoss words the report for a group whose categoryValueRef names
// no <categoryValue> the document declares.
func groupRefLoss(ref string) string {
	return "its categoryValueRef " + strconv.Quote(ref) +
		" names no <categoryValue> declared in this document, and the value " +
		"is the group's meaning (ADR-039 §2.3) — the group is dropped, the " +
		"rest of the file imports"
}
