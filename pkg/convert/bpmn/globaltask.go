package bpmn

import (
	"encoding/xml"
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/errs"
)

// globalTaskTags maps each member of the GlobalTask family onto the
// in-process tag whose reading it shares.
//
// The rewrite IS the mechanism (ADR-024 v.7 §2.13). A global task's body is
// the same body its in-process counterpart has — the same <script>, the same
// <documentation>, the same dialect attributes — so pass 2 must build it with
// the same builder, and the way to get that is to hand pass 2 the tag it
// already knows. Anything else would be a second reading of every construct,
// drifting from the first the moment either changes.
var globalTaskTags = map[string]string{
	tagGlobalTask:             tagTask,
	tagGlobalManualTask:       tagManualTask,
	tagGlobalUserTask:         tagUserTask,
	tagGlobalScriptTask:       tagScriptTask,
	tagGlobalBusinessRuleTask: tagBusinessRuleTask,
}

// Suffixes of the elements synthesized inside a global task's process.
//
// They are DERIVED from the global task's id rather than generated, because a
// re-import must produce the same ids: the process is registered under the
// global task's id, a second registration of the same key is a new VERSION of
// it (ADR-019), and versions of one definition whose inner ids drifted are not
// versions of one definition. Every one is claimed in the document's id
// ledger, so a file already using one is refused for the duplicate rather than
// silently rewired.
const (
	globalStartSuffix = ".start"
	globalTaskSuffix  = ".task"
	globalEndSuffix   = ".end"
	globalInSuffix    = ".in"
	globalOutSuffix   = ".out"
)

// parseGlobalTaskElem parses one definitions-level GlobalTask into a process
// of its own — the callable the registry serves (ADR-023 v.5 §2.7).
//
// It RECORDS specs and builds nothing, which is what the importer's two-pass
// shape requires: the item definitions an <ioSpecification> names may be
// declared after this element, and they are not resolvable until pass 2 has
// built the document's vocabulary. So this returns an assembly in exactly the
// state a <process> leaves behind, and pass 2 builds it by the same path,
// unaware that no <process> element produced it.
func parseGlobalTaskElem(p *parser, se xml.StartElement) (*assembly, error) {
	id := attrValue(se, "id")
	if id == "" {
		return nil, errs.New(
			errs.M("bpmn: <%s> has no id (ids are never auto-generated, "+
				"ADR-019) — and here the id is also the key a callActivity "+
				"names it by", se.Name.Local),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if err := p.claimID(id, se.Name.Local); err != nil {
		return nil, err
	}

	// definitionsParsers routes exactly the keys of this map — it is built
	// FROM it — so the lookup cannot miss, and there is no unreachable
	// branch here pretending otherwise.
	inner := globalTaskTags[se.Name.Local]

	// The same body reader every in-process node uses, so <script>,
	// <documentation>, <ioSpecification> and the dialect attributes are read
	// once in this codebase and not twice.
	body, err := p.parseNodeBody(se)
	if err != nil {
		return nil, err
	}

	// The callable's ioSpecification serves twice: as the PROCESS's declared
	// contract and as the TASK's own parameters. It is one element in the
	// standard — the ioSpecification belongs to the CallableElement, and for
	// a global task the callable IS the task — so a declared output is one
	// the task can actually produce, and the contract is not a promise
	// nothing keeps.
	spec := procSpec{
		id:          id,
		name:        fallbackName(id, strings.TrimSpace(attrValue(se, "name"))),
		docs:        body.docs,
		io:          body.io,
		synthesized: true,
	}

	asm := p.newAssembly(spec)

	if err := p.addGlobalTaskGraph(asm, se, inner, id, spec.name, body); err != nil {
		return nil, err
	}

	return asm, nil
}

// addGlobalTaskGraph records the None Start, the task and the None End that
// make the callable a runnable process, plus the two flows joining them.
//
// §13.3.4 gives a call the semantics of the called Process, and a Process is
// entered by its None Start Event — so a callable with no flow of its own
// needs exactly this shape to be callable at all.
func (p *parser) addGlobalTaskGraph(
	asm *assembly, se xml.StartElement, innerTag, id, name string,
	body nodeBody,
) error {
	startID, taskID, endID := id+globalStartSuffix, id+globalTaskSuffix,
		id+globalEndSuffix
	inID, outID := id+globalInSuffix, id+globalOutSuffix

	for _, claim := range []struct{ id, kind string }{
		{startID, tagStartEvent},
		{taskID, innerTag},
		{endID, tagEndEvent},
		{inID, tagSequenceFlow},
		{outID, tagSequenceFlow},
	} {
		if err := p.claimID(claim.id, claim.kind); err != nil {
			return err
		}
	}

	// The task keeps the ELEMENT the file wrote — its attributes are the
	// dialect's and the model's to read — with only its tag rewritten, so
	// pass 2 dispatches on the in-process builder.
	inner := se
	inner.Name.Local = innerTag

	asm.specs = append(asm.specs,
		nodeSpec{se: synthElement(tagStartEvent), id: startID, name: startID},
		nodeSpec{se: inner, id: taskID, name: name, body: body},
		nodeSpec{se: synthElement(tagEndEvent), id: endID, name: endID})

	asm.flows = append(asm.flows,
		flowSpec{id: inID, srcRef: startID, trgRef: taskID},
		flowSpec{id: outID, srcRef: taskID, trgRef: endID})

	return nil
}

// synthElement builds the start element for a node the file did not write.
//
// It carries no attributes on purpose: a synthesized event is a NONE event,
// and anything the global task declared belongs to the task, not to the
// scaffolding around it.
func synthElement(tag string) xml.StartElement {
	return xml.StartElement{Name: xml.Name{Space: nsBPMN, Local: tag}}
}
