package data

import (
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

// ReadyParameter wraps item as a Ready parameter named name — the datum
// shape every runtime commit path builds (FIX-026: one error-returning
// helper instead of per-site Must* chains; a bad name or nil item fails
// with a classified error, never a panic).
func ReadyParameter(name string, item *ItemDefinition) (*Parameter, error) {
	iae, err := NewItemAwareElement(item, ReadyDataState)
	if err != nil {
		return nil, err
	}

	return NewParameter(name, iae)
}

// ReadyValueParameter builds an ItemDefinition from value (with optional
// item options, e.g. foundation.WithID) and wraps it as a Ready parameter
// named name — the value-first twin of ReadyParameter.
func ReadyValueParameter(
	name string,
	value Value,
	itemOpts ...options.Option,
) (*Parameter, error) {
	item, err := NewItemDefinition(value, itemOpts...)
	if err != nil {
		return nil, err
	}

	return ReadyParameter(name, item)
}
