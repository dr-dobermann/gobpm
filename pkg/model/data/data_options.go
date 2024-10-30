package data

import (
	"fmt"
	"reflect"
	"slices"

	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

// ============================================================================
//                              propConfig
// ============================================================================

type (
	// propConfig is used to create a single Property
	propConfig struct {
		name string

		iae *ItemAwareElement
	}
)

// newProperty creates a new property from propertyConfiguration.
func (cfg *propConfig) newProperty() (*Property, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("property configuration validation failed: %w", err)
	}

	p := Property{
		ItemAwareElement: *cfg.iae,
		name:             cfg.name,
	}

	return &p, nil
}

// --------------------- options.Configurator interface -----------------------

// Validate checks property configuration consistency.
func (cfg *propConfig) Validate() error {
	if cfg.name == "" {
		return fmt.Errorf("property should have a name")
	}

	if cfg.iae == nil {
		return fmt.Errorf("ItemAwarElement isn't set")
	}

	return nil
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

// ============================================================================
//                               asscConfig
// ============================================================================

type (
	asscConfig struct {
		trans FormalExpression
		trg   *ItemAwareElement
		src   []*ItemAwareElement

		baseOptions []options.Option
	}

	asscOption func(cfg *asscConfig) error
)

// newAssociation creates a new Association from asscConfig.
func (aCfg *asscConfig) newAssociation() (*Association, error) {
	if err := aCfg.Validate(); err != nil {
		return nil,
			fmt.Errorf("association configuration validation failed: %w", err)
	}

	be, err := foundation.NewBaseElement(aCfg.baseOptions...)
	if err != nil {
		return nil,
			fmt.Errorf("baseElement building failed: %w", err)
	}

	a := Association{
		BaseElement:    *be,
		transformation: aCfg.trans,
		sources:        map[string]*ItemAwareElement{},
		target:         aCfg.trg,
	}

	for _, iae := range aCfg.src {
		a.sources[iae.ItemDefinition().Id()] = iae
	}

	return &a, nil
}

// WithTransformation set transformation for the Association.
func WithTransformation(fe FormalExpression) options.Option {
	f := func(cfg *asscConfig) error {
		if fe == nil {
			return fmt.Errorf("no formal expression for transformation")
		}

		if cfg.trans != nil {
			return fmt.Errorf("transformation is already set")
		}

		cfg.trans = fe

		return nil
	}

	return asscOption(f)
}

// WithSource adds new source to Association.
// Source should be an ItemAwareElement.
// WithSource checks for ItemDefintion duplication.
func WithSource(iae *ItemAwareElement) options.Option {
	f := func(cfg *asscConfig) error {
		if iae == nil {
			return fmt.Errorf("no ItemAwareElemnt for source")
		}

		if slices.ContainsFunc(
			cfg.src,
			func(src *ItemAwareElement) bool {
				return src.ItemDefinition().Id() == iae.ItemDefinition().Id()
			}) {
			return fmt.Errorf("duplicate source ItemDefinition id: %q",
				iae.ItemDefinition().Id())
		}

		cfg.src = append(cfg.src, iae)

		return nil
	}

	return asscOption(f)
}

// -------------------- options.Option interface ------------------------------

func (ao asscOption) Apply(cfg options.Configurator) error {
	if aCfg, ok := cfg.(*asscConfig); ok {
		return ao(aCfg)
	}

	return fmt.Errorf("not a asscConfig (%s)", reflect.TypeOf(cfg).String())
}

// --------------------- options.Configurator interface -----------------------

func (aCfg *asscConfig) Validate() error {
	if aCfg.trans == nil && len(aCfg.src) != 1 {
		return fmt.Errorf("association could have only 1 source " +
			"without transformation")
	}

	if aCfg.trg == nil {
		return fmt.Errorf("association target isn't defined")
	}

	return nil
}

// ----------------------------------------------------------------------------
