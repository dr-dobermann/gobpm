package data

import (
	"fmt"
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

// Property represents a BPMN property element. Properties, like Data Objects, are item-aware elements. But, unlike Data
// Objects, they are not visually displayed on a Process diagram. Certain flow
// elements MAY contain properties, in particular only Processes, Activities,
// and Events MAY contain Properties.
type Property struct {
	name string
	ItemAwareElement
}

// NewProperty creates a new Property object and returns its pointer.
func NewProperty(
	name string,
	item *ItemDefinition,
	state *SrcState,
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

	if err := CheckName(name, errorClass); err != nil {
		return nil, err
	}

	iae, err := NewItemAwareElement(item, state, baseOpts...)
	if err != nil {
		return nil, err
	}

	p := &Property{
		ItemAwareElement: *iae,
		name:             name,
	}

	if err := rejectValueless(p); err != nil {
		return nil, err
	}

	return p, nil
}

// rejectValueless reports an error if p has no value — its item has no structure,
// so gobpm can never fill it (ADR-010: a property's structure IS its value,
// immutable). A value-less property is rejected at construction (FIX-018): the
// fail-fast, single source of truth for "every property is usable". The "declare
// empty, fill later" intent uses a typed-zero value (NewVariable(0) / "").
func rejectValueless(p *Property) error {
	if p.Value() == nil {
		return errs.New(
			errs.M("property %q has no value: its item has no structure and "+
				"can never be filled; declare it with a typed value", p.name),
			errs.C(errorClass, errs.InvalidObject))
	}

	return nil
}

// MustProperty creates a new Property and returns its pointer on success or
// panics on failure.
func MustProperty(
	name string,
	item *ItemDefinition,
	state *SrcState,
	_ ...options.Option,
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

	if err := iaeOpt(&cfg); err != nil {
		return nil,
			fmt.Errorf("property option applying error: %w", err)
	}

	return cfg.newProperty()
}

// MustProp creates and returns property with name and IAEAdderOption.
// If error occurs it panics.
func MustProp(name string, iaeOpt IAEAdderOption) *Property {
	p, err := NewProp(name, iaeOpt)
	if err != nil {
		errs.Panic(err)

		return nil
	}

	return p
}

// Name returns the Property name.
func (p *Property) Name() string {
	return p.name
}

// Clone returns a deep copy of the Property — a distinct ItemAwareElement (its
// own value and state) under the same name. It lets a Snapshot give each
// registered version and each instance private property objects instead of
// sharing one mutable object across them (FIX-016). It errors when the embedded
// ItemAwareElement can't be cloned (a nil value).
func (p *Property) Clone() (*Property, error) {
	iae, err := p.ItemAwareElement.Clone()
	if err != nil {
		return nil, err
	}

	return &Property{
		name:             p.name,
		ItemAwareElement: *iae,
	}, nil
}

// CloneProperties returns deep copies of props so a node or snapshot clone owns
// private Property objects instead of sharing the source's — a later edit to the
// source (removing or re-valuing a property) can't leak into a registered
// snapshot or a running instance (FIX-017). It returns an error if any property
// is value-less: an ItemAwareElement with no structure is unclonable (its value
// is nil, Clone rejects it) and a process declaring one can't be executed, so it
// is rejected here at registration. A nil slice clones to nil.
func CloneProperties(props []*Property) ([]*Property, error) {
	if props == nil {
		return nil, nil
	}

	cloned := make([]*Property, len(props))
	for i, p := range props {
		c, err := p.Clone()
		if err != nil {
			return nil, errs.New(
				errs.M("couldn't clone property %q", p.Name()),
				errs.C(errorClass, errs.OperationFailed),
				errs.E(err))
		}

		cloned[i] = c
	}

	return cloned, nil
}

// ----------------------------------------------------------------------------
// Test interfaces for Property.
var _ Data = (*Property)(nil)
