package data

import (
	"fmt"
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

// Properties, like Data Objects, are item-aware elements. But, unlike Data
// Objects, they are not visually displayed on a Process diagram. Certain flow
// elements MAY contain properties, in particular only Processes, Activities,
// and Events MAY contain Properties.
type Property struct {
	ItemAwareElement

	// Defines the name of the Property.
	name string
}

// NewProperty creates a new Property object and returns its pointer.
func NewProperty(
	name string,
	item *ItemDefinition,
	state *DataState,
	baseOpts ...options.Option,
) (*Property, error) {
	name = strings.TrimSpace(name)
	if err := errs.CheckStr(
		name,
		"property should has non-empty name",
		errorClass,
	); err != nil {
		return nil, err
	}

	iae, err := NewItemAwareElement(item, state, baseOpts...)
	if err != nil {
		return nil, err
	}

	return &Property{
			ItemAwareElement: *iae,
			name:             name,
		},
		nil
}

// MustProperty creates a new Property and returns its pointer on success or
// panics on failure.
func MustProperty(
	name string,
	item *ItemDefinition,
	state *DataState,
	baseOpts ...options.Option,
) *Property {
	p, err := NewProperty(name, item, state)
	if err != nil {
		errs.Panic(err)
	}

	return p
}

// NewProp creates a new Property with a name and the ItemAwareElement.
// IAE is set up by WithIAE option.
func NewProp(name string, iaeOpt IAEAdderOption) (*Property, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("property should have non-empty name")
	}

	if iaeOpt == nil {
		return nil, fmt.Errorf("no IAEAdder")
	}

	cfg := propConfig{
		name: name,
	}

	if err := iaeOpt.Apply(&cfg); err != nil {
		return nil,
			fmt.Errorf("property option applying error: %w", err)
	}

	return cfg.newProperty()
}

// Name returns the Property name.
func (p *Property) Name() string {
	return p.name
}

// --------------------- IAEAdder interface -----------------------------------

func (cfg *propConfig) AddIAE(iae *ItemAwareElement) error {
	if iae == nil {
		return fmt.Errorf("no ItemAwareElement")
	}

	cfg.iae = iae

	return nil
}

// ----------------------------------------------------------------------------
// Test interfaces for Property.
var _ Data = (*Property)(nil)
