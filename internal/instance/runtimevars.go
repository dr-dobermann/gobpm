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
	return []string{StartedAt, CurrState, TracksCount}
}
