package bpmn

import (
	"encoding/xml"
	"strconv"
	"strings"
)

// The Collaboration family, consumed definitionally per ADR-024 §2.15:
// participants for which process each names, message flows for the
// report. No § pins — the extract carries none for the family (#334).
const (
	tagCollaboration = "collaboration"
	tagParticipant   = "participant"
	tagMessageFlow   = "messageFlow"
	attrProcessRef   = "processRef"
)

// participantSpec is one <participant> as read: its identity and the
// process it names — "" is a black-box pool (SRD-089.I FR-3).
type participantSpec struct {
	id, name   string
	processRef string
}

// messageFlowSpec is one <messageFlow>: reported under its own identity,
// its messageRef consumed against the catalog (§4.4).
type messageFlowSpec struct {
	id, name             string
	sourceRef, targetRef string
	messageRef           string
}

// collabSpec is one <collaboration> as read. Nothing here is built.
type collabSpec struct {
	id, name     string
	participants []participantSpec
	flows        []messageFlowSpec
}

// parseCollaborationElem reads one definitions-level <collaboration>
// into the parser's collab list, validated once the whole document is
// read — its processes may be declared after it.
func parseCollaborationElem(
	p *parser, _ *assembly, se xml.StartElement,
) (*assembly, error) {
	spec := collabSpec{
		id:   strings.TrimSpace(attrValue(se, "id")),
		name: strings.TrimSpace(attrValue(se, "name")),
	}

	if spec.id != "" {
		if err := p.claimID(spec.id, tagCollaboration); err != nil {
			return nil, err
		}
	}

	outer := p.owner
	if spec.id != "" {
		p.owner = spec.id
	}

	err := p.parseCollabBody(&spec, se)

	p.owner = outer

	if err != nil {
		return nil, err
	}

	p.reportUnmappedAttrs(se, p.owner, nil)
	p.collabs = append(p.collabs, spec)

	return nil, nil
}

// parseCollabBody walks a collaboration's children.
func (p *parser) parseCollabBody(spec *collabSpec, se xml.StartElement) error {
	for {
		tok, err := p.token()
		if err != nil {
			return err
		}

		switch t := tok.(type) {
		case xml.StartElement:
			if err := p.parseCollabChild(spec, t); err != nil {
				return err
			}

		case xml.EndElement:
			if t.Name == se.Name {
				return nil
			}
		}
	}
}

// parseCollabChild handles one child of <collaboration>: a participant,
// a message flow, documentation (skipped by declaration — there is no
// model element to attach it to), or a stranger settled through the
// disposition tables.
func (p *parser) parseCollabChild(spec *collabSpec, se xml.StartElement) error {
	if se.Name.Space != nsBPMN {
		return p.skipElement()
	}

	switch se.Name.Local {
	case tagParticipant:
		return p.parseParticipant(spec, se)

	case tagMessageFlow:
		return p.parseMessageFlow(spec, se)

	case tagDocumentation:
		return p.skipElement()
	}

	return p.settle(ctxCollab, se)
}

// parseParticipant reads one <participant>.
func (p *parser) parseParticipant(spec *collabSpec, se xml.StartElement) error {
	ps := participantSpec{
		id:         strings.TrimSpace(attrValue(se, "id")),
		name:       strings.TrimSpace(attrValue(se, "name")),
		processRef: strings.TrimSpace(attrValue(se, attrProcessRef)),
	}

	if ps.id != "" {
		if err := p.claimID(ps.id, tagParticipant); err != nil {
			return err
		}
	}

	spec.participants = append(spec.participants, ps)

	return p.skipElement()
}

// parseMessageFlow reads one <messageFlow>.
func (p *parser) parseMessageFlow(spec *collabSpec, se xml.StartElement) error {
	fs := messageFlowSpec{
		id:         strings.TrimSpace(attrValue(se, "id")),
		name:       strings.TrimSpace(attrValue(se, "name")),
		sourceRef:  strings.TrimSpace(attrValue(se, "sourceRef")),
		targetRef:  strings.TrimSpace(attrValue(se, "targetRef")),
		messageRef: strings.TrimSpace(attrValue(se, attrMessageRef)),
	}

	if fs.id != "" {
		if err := p.claimID(fs.id, tagMessageFlow); err != nil {
			return err
		}
	}

	spec.flows = append(spec.flows, fs)

	return p.skipElement()
}

// messageFlowLoss is why a <messageFlow> cannot survive the import: it
// is the DRAWING of an exchange whose execution the engine performs
// through message events and correlation keys (ADR-024 §2.15) — the
// graph the engine runs is unchanged by its presence.
const messageFlowLoss = "draws a message exchange between pools; the " +
	"engine performs the exchange itself — a throw or send on one side, " +
	"a message event or receive on the other, matched by message name " +
	"and correlation key — so the drawing has nothing to add to the " +
	"graph the engine runs"

// resolveCollaborations validates and consumes the document's
// collaborations once the whole document is read (a collaboration may
// precede the processes it names): a present participant processRef
// must name a declared process; a message flow's messageRef must name a
// declared message; each flow is reported (SRD-089.I FR-3, FR-4).
func resolveCollaborations(p *parser) error {
	for i := range p.collabs {
		c := &p.collabs[i]

		for _, ps := range c.participants {
			if ps.processRef == "" {
				// A black-box pool: its process is someone else's system,
				// definitional by nature (FR-3).
				continue
			}

			site := refSite{
				from:   "<" + tagParticipant + "> " + strconv.Quote(ps.id),
				attr:   attrProcessRef,
				target: ps.processRef,
			}

			kind, declared := p.ids[ps.processRef]
			if !declared {
				return site.notFound(tagProcess)
			}

			if kind != tagProcess {
				return site.wrongKind(tagProcess, kind)
			}
		}

		for _, fs := range c.flows {
			if fs.messageRef != "" {
				site := refSite{
					from:   "<" + tagMessageFlow + "> " + strconv.Quote(fs.id),
					attr:   attrMessageRef,
					target: fs.messageRef,
				}

				kind, declared := p.ids[fs.messageRef]
				if !declared {
					return site.notFound(tagMessage)
				}

				if kind != tagMessage {
					return site.wrongKind(tagMessage, kind)
				}
			}

			// sourceRef/targetRef stay unresolved by design: they name
			// interaction nodes across pools — participants included —
			// and validating them would model the half of Collaboration
			// §2.15 declines (§4.4).
			p.report(fallbackName(fs.id, fs.name), tagMessageFlow, messageFlowLoss)
		}
	}

	return nil
}
