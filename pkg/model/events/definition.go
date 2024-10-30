package events

import (
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

// definition is the base class for define an Event.
type definition struct {
	foundation.BaseElement
}

// newDefinition creates a new Event Definition and returns its pointer.
func newDefinition(baseOpts ...options.Option) (*definition, error) {
	be, err := foundation.NewBaseElement(baseOpts...)
	if err != nil {
		return nil, err
	}

	return &definition{
		BaseElement: *be,
	}, nil
}

// --------------- flow.EventDefinition interface ------------------------------

// GetItemList returns a list of data.ItemDefinition the EventDefinition
// is based on.
// If EventDefiniton isn't based on any data.ItemDefiniton, empty list
// wil be returned.
func (d *definition) GetItemsList() []*data.ItemDefinition {
	// by default an empty list of items returned
	return []*data.ItemDefinition{}
}

// -----------------------------------------------------------------------------
