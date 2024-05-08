package events

import (
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/common"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

type ErrorEventDefinition struct {
	definition

	// If the trigger is an Error, then an Error payload MAY be provided.
	err *common.Error
}

// NewErrorEventDefinition creates a new ErrorEventDefinition and returns
// its pointer.
func NewErrorEventDefinition(
	cErr *common.Error,
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
func (eed *ErrorEventDefinition) Error() *common.Error {
	return eed.err
}

// ---------------- flow.EventDefinition interface -----------------------------

// Type implements the Definition interface.
func (*ErrorEventDefinition) Type() flow.EventTrigger {

	return flow.TriggerError
}

// CheckItemDefinition check if definition is related with
// data.ItemDefinition with iDefId Id.
func (eed *ErrorEventDefinition) CheckItemDefinition(iDefId string) bool {
	if eed.err.Structure() == nil {
		return false
	}

	return eed.err.Structure().Id() == iDefId
}

// GetItemList returns a list of data.ItemDefinition the EventDefinition
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

		if d.ItemDefinition().Id() != eed.err.Structure().Id() {
			return nil,
				errs.New(
					errs.M(
						"error itemDefinition and data itemDefinition "+
							"have different ids(%s, %s)",
						eed.err.Structure().Id(),
						d.ItemDefinition().Id()))
		}

		iDef = d.ItemDefinition()
	}

	e, err := common.NewError(
		eed.err.Name(),
		eed.err.ErrorCode(),
		iDef,
		foundation.WithId(eed.err.Id()))
	if err != nil {
		return nil,
			errs.New(
				errs.M("couldn't clone Error %q[%s]",
					eed.err.Name(), eed.err.Id()),
				errs.C(errorClass, errs.BulidingFailed),
				errs.E(err))
	}

	need, err := NewErrorEventDefinition(
		e, foundation.WithId(eed.Id()))
	if err != nil {
		return nil,
			errs.New(
				errs.M("cloning failed for ErrorEventDefinition #%s",
					eed.Id()),
				errs.C(errorClass, errs.BulidingFailed),
				errs.E(err))
	}

	return need, nil
}

// -----------------------------------------------------------------------------
