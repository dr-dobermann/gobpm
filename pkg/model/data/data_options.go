package data

import (
	"fmt"
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
