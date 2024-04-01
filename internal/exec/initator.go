package exec

import (
	"context"
	"errors"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
)

// Process Initator holds the prepared process Snapshot which is used to
// create an process Instance.
// Process Initator also holds a process Initiation Events List and receive
// all event definition from list to start a new process Instance.
type Initiator struct {
	foundation.BaseElement

	Sshot *Snapshot

	// InitEvents indexed by Definition's Id.
	InitEvents  map[string]flow.EventNode
	EvtProducer EventProducer
	Runner      ProcessRunner
}

// NewInitiator creates a new Initiator and returns its pointer on success
// or error on failure.
func NewInitiator(
	p *process.Process,
	ep EventProducer,
	r ProcessRunner,
	baseOpts ...options.Option,
) (*Initiator, error) {
	if ep == nil {
		return nil,
			errs.New(
				errs.M("empty event producer"),
				errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if r == nil {
		return nil,
			errs.New(
				errs.M("empty runner"),
				errs.C(errorClass, errs.EmptyNotAllowed))
	}

	be, err := foundation.NewBaseElement(baseOpts...)
	if err != nil {
		return nil, err
	}

	s, err := NewSnapshot(p)
	if err != nil {
		return nil, err
	}

	ini := Initiator{
		BaseElement: *be,
		Sshot:       s,
		InitEvents:  map[string]flow.EventNode{},
		EvtProducer: ep,
		Runner:      r,
	}

	if err = ini.registerEvents(s, ep); err != nil {
		return nil, err
	}

	return &ini, nil
}

// registerEvents registers all eventDefinition of the _initiation_ events of
// the process.
func (ini *Initiator) registerEvents(s *Snapshot, ep EventProducer) error {
	ee := []error{}

	for _, n := range s.Nodes {
		e, ok := n.(flow.EventNode)
		if !ok {
			continue
		}

		//TODO: add RecieveTask Message as initial EventDefinition

		// initiate event should be throw event and has no incoming flows
		// or any bounded tasks
		for _, d := range e.Definitions() {
			if len(n.Incoming()) == 0 &&
				flow.StartEventClass == e.EventClass() {
				if err := ep.RegisterEvents(ini, d); err != nil {
					ee = append(ee, err)

					continue
				}

				ini.InitEvents[d.Id()] = e
			}
		}
	}

	if len(ee) != 0 {
		return errors.Join(ee...)
	}

	return nil
}

// ------------------ EventProcessor interface ---------------------------------

// Process processes event definition and on success creates a new process
// instance and add send it to run queue.
//
// Initiator registers only initating events, so every event processed by
// it should initiate (run) a new process instance.
func (ini *Initiator) ProcessEvent(
	ctx context.Context,
	eDef flow.EventDefinition,
) error {
	if eDef == nil {
		if len(ini.InitEvents) != 0 {
			return errs.New(
				errs.M("empty event definition is not expected"),
				errs.C(errorClass, errs.EmptyNotAllowed))
		}

		return ini.Runner.RunProcess(ini.Sshot, nil, nil)
	}

	e, ok := ini.InitEvents[eDef.Id()]
	if !ok {
		return errs.New(
			errs.M("event definition %s isn't registered as initial event",
				eDef.Id()),
			errs.C(errorClass, errs.ObjectNotFound))
	}

	return ini.Runner.RunProcess(ini.Sshot, e, eDef)
}

// -----------------------------------------------------------------------------
