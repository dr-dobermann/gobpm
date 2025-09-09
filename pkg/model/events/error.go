package events

import (
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

// ErrorEventDefinition represents an error event definition.
type ErrorEventDefinition struct {
	definition

	// If the trigger is an Error, then an Error payload MAY be provided.
	err *bpmncommon.Error
}

// NewErrorEventDefinition creates a new ErrorEventDefinition and returns
// its pointer.
func NewErrorEventDefinition(
	cErr *bpmncommon.Error,
	baseOpts ...options.Option,
) (*ErrorEventDefinition, error) {
	if cErr == nil {
		return nil,
			errs.New(
				errs.M("empty error object isn't allowed"),
				errs.C(errorClass, errs.BulidingFailed))
	}

	d, err := newDefinition(baseOpts...)
	if err != nil {
		return nil, err
	}

	return &ErrorEventDefinition{
		definition: *d,
		err:        cErr,
	}, nil
}

// Error returns the ErrorEventDefinition error structure.
func (eed *ErrorEventDefinition) Error() *bpmncommon.Error {
	return eed.err
}

// ---------------- flow.EventDefinition interface -----------------------------

// Type implements the Definition interface.
func (*ErrorEventDefinition) Type() flow.EventTrigger {
	return flow.TriggerError
}

// CheckItemDefinition check if definition is related with
// data.ItemDefinition with iDefId Id.
func (eed *ErrorEventDefinition) CheckItemDefinition(iDefID string) bool {
	if eed.err.Structure() == nil {
		return false
	}

	return eed.err.Structure().ID() == iDefID
}

// GetItemsList returns a list of data.ItemDefinition the EventDefinition
// is based on.
// If EventDefiniton isn't based on any data.ItemDefiniton, empty list
// wil be returned.
func (eed *ErrorEventDefinition) GetItemsList() []*data.ItemDefinition {
	if eed.err.Structure() == nil {
		return []*data.ItemDefinition{}
	}

	return []*data.ItemDefinition{eed.err.Structure()}
}

// CloneEvent clones EventDefinition with dedicated data.ItemDefinition
// list.
func (eed *ErrorEventDefinition) CloneEvent(
	evtData []data.Data,
) (flow.EventDefinition, error) {
	var iDef *data.ItemDefinition

	if len(evtData) != 0 {
		d := evtData[0]

		if d.ItemDefinition().ID() != eed.err.Structure().ID() {
			return nil,
				errs.New(
					errs.M(
						"error itemDefinition and data itemDefinition "+
							"have different ids(%s, %s)",
						eed.err.Structure().ID(),
						d.ItemDefinition().ID()))
		}

		iDef = d.ItemDefinition()
	}

	e, err := bpmncommon.NewError(
		eed.err.Name(),
		eed.err.ErrorCode(),
		iDef,
		foundation.WithID(eed.err.ID()))
	if err != nil {
		return nil,
			errs.New(
				errs.M("couldn't clone Error %q[%s]",
					eed.err.Name(), eed.err.ID()),
				errs.C(errorClass, errs.BulidingFailed),
				errs.E(err))
	}

	need, err := NewErrorEventDefinition(
		e, foundation.WithID(eed.ID()))
	if err != nil {
		return nil,
			errs.New(
				errs.M("cloning failed for ErrorEventDefinition #%s",
					eed.ID()),
				errs.C(errorClass, errs.BulidingFailed),
				errs.E(err))
	}

	return need, nil
}

// -----------------------------------------------------------------------------
