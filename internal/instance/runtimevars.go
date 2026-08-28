package instance

import (
	"fmt"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
)

const (
	// StartedAt represents the started time variable name.
	StartedAt = "STARTED_AT"
	// CurrState represents the current state variable name.
	CurrState = "STATE"
	// TracksCount represents the tracks count variable name.
	TracksCount = "TRACKS_CNT"
	// CompletedBy names the performer register: a map of node name → the user who
	// completed that human task (ADR-020 v.2 §2.4.2). Served here, under the
	// reserved read-only RUNTIME subtree, because the record is engine-published —
	// a process reads who performed a task but must not be able to overwrite the
	// answer, nor collide with it by naming a variable the same way.
	//
	// One map rather than one variable per task, so this name set stays closed: an
	// open per-task namespace would force prefix matching here and make
	// RuntimeVarNames grow with every completion.
	CompletedBy = "COMPLETED_BY"

	// Iterations names the iteration register: a map of activity id → what
	// that activity's iteration did (kind, total, completed, terminated).
	// Served here, keyed by activity id, for two reasons that both matter —
	// the RUNTIME name set stays closed, and a key disambiguates two iterated
	// activities running at once, which a flat name could not (ADR-025
	// §2.9.2). It outlives the activity: the counts at the activity's own
	// scope end with the activation, and this is what answers "how many did
	// we process?" one node later.
	Iterations = data.IterationsName

	// IterationOwners names the completion account: a map of activity id →
	// (ordinal → the actor who completed that instance).
	//
	// COMPLETED_BY cannot answer this. It keys by NODE, so an iterated
	// activity has one entry however many instances ran and whoever did them
	// — the last completion wins and the rest are lost. Three approvals are
	// three pieces of work by three people, and which of them approved item 2
	// stays answerable after the activity has gone (ADR-025 §2.15).
	IterationOwners = data.IterationOwnersName
)

// DataReader returns the instance's read-only root data reader — process
// properties plus the runtime variables (StartedAt/CurrState/TracksCount). For
// host observation (SRD-018): the returned value exposes only the read-only
// service.DataReader surface, never a mutating method. Built once in New (an
// empty frame at the process-root scope), so this getter cannot fail.
func (inst *Instance) DataReader() service.DataReader {
	return inst.sc.reader
}

// RuntimeVar implements scope.RuntimeVarsSupplier: the data plane delegates
// reads under the reserved RUNTIME subtree here, so every read observes the
// live engine state (SRD-007 FR-9).
func (inst *Instance) RuntimeVar(name string) (data.Data, error) {
	var d data.Value

	switch name {
	case StartedAt:
		d = values.NewVariable(inst.startTime)

	case CurrState:
		d = values.NewVariable(inst.State())

	case TracksCount:
		tc := int(inst.trackCount.Load())
		d = values.NewVariable(tc)

	case CompletedBy:
		m, err := values.NewMap(inst.performers.snapshot())
		if err != nil {
			return nil,
				fmt.Errorf("couldn't build the %q runtime variable: %w", name, err)
		}

		d = m

	case Iterations:
		m, err := values.NewMap(inst.iterations.snapshot())
		if err != nil {
			return nil,
				fmt.Errorf("couldn't build the %q runtime variable: %w", name, err)
		}

		d = m

	case IterationOwners:
		m, err := values.NewMap(inst.iterationOwners.snapshot())
		if err != nil {
			return nil,
				fmt.Errorf("couldn't build the %q runtime variable: %w", name, err)
		}

		d = m

	default:
		return nil,
			fmt.Errorf("invalid runtime variable name %q", name)
	}

	id, err := data.NewItemDefinition(d)
	if err != nil {
		return nil,
			fmt.Errorf(
				"couldn't create an ItemDefinition for runtime variable %q: %w",
				name, err)
	}

	iae, err := data.NewItemAwareElement(id, data.ReadyDataState)
	if err != nil {
		return nil,
			fmt.Errorf(
				"couldn't create an ItemAwareElement for runtime variable %q: %w",
				name, err)
	}

	p, err := data.NewParameter(name, iae)
	if err != nil {
		return nil,
			fmt.Errorf(
				"couldn't create an ItemDefinition for runtime variable %q: %w",
				name, err)
	}

	return p, nil
}

// RuntimeVarNames implements scope.RuntimeVarsSupplier: it lists the runtime
// variables the instance exposes under the RUNTIME source.
func (inst *Instance) RuntimeVarNames() []string {
	return []string{
		StartedAt, CurrState, TracksCount, CompletedBy, Iterations,
		IterationOwners,
	}
}
