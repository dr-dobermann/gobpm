package data

import (
	"fmt"
	"reflect"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

// ============================================================================
// ItemDefinition options
// ============================================================================

type (
	// used as functor interface for ItemDefinition option definition.
	itemOption func(*itemConfig) error

	// itemConfig consist optional parameters for ItemDefinition build.
	itemConfig struct {
		baseOptions []options.Option
		kind        ItemKind
		imp         *foundation.Import
		str         Value
		collection  bool
	}
)

// itemDefBuild builds ItemDefinition object from the itemConfig.
func (ic *itemConfig) itemDef() (*ItemDefinition, error) {
	be, err := foundation.NewBaseElement(ic.baseOptions...)
	if err != nil {
		return nil, err
	}

	return &ItemDefinition{
		BaseElement:  *be,
		kind:         ic.kind,
		importRef:    ic.imp,
		structure:    ic.str,
		isCollection: ic.collection,
	}, nil
}

// WithKind sets kind of an ItemDefintion.
func WithKind(kind ItemKind) options.Option {
	f := func(cfg *itemConfig) error {
		if err := kind.Validate(); err != nil {
			return err
		}

		cfg.kind = kind

		return nil
	}

	return itemOption(f)
}

// WithImport sets import of an ItemDefintion.
func WithImport(imp *foundation.Import) options.Option {
	f := func(cfg *itemConfig) error {
		cfg.imp = imp

		return nil
	}

	return itemOption(f)
}

// --------------------- options.Option interface -----------------------------
// Apply implements ItemOption interface for itemOption functor.
func (o itemOption) Apply(cfg options.Configurator) error {
	if ic, ok := cfg.(*itemConfig); ok {
		return o(ic)
	}

	return errs.New(
		errs.M("not itemConfig: %s", reflect.TypeOf(cfg).String()),
		errs.C(errorClass, errs.TypeCastingError))
}

// ------------------- options.Configurator interface -------------------------
func (ic *itemConfig) Validate() error {
	return nil
}

// ----------------------------------------------------------------------------

// ============================================================================
// ItemAwareElement options
// ============================================================================

type (
	iaeConfig struct {
		state *SrcState
		iDef  *ItemDefinition

		baseOpts []options.Option
	}

	// IAEOption is a function type for configuring ItemAwareElement
	IAEOption func(cfg *iaeConfig) error
)

// newIAE creates a new IAE from the iaeConfig.
func (iaeC *iaeConfig) newIAE() (*ItemAwareElement, error) {
	if err := iaeC.Validate(); err != nil {
		return nil,
			fmt.Errorf("ItemAwareElement building failed: %w", err)
	}

	return NewItemAwareElement(
		iaeC.iDef,
		iaeC.state,
		iaeC.baseOpts...)
}

// WithState sets current state of the IAE.
func WithState(ds *SrcState) IAEOption {
	f := func(cfg *iaeConfig) error {
		if ds == nil {
			return fmt.Errorf("empty data state")
		}

		cfg.state = ds

		return nil
	}

	return IAEOption(f)
}

// WithIDefinition creqtes a new ItemDefinition for IAE.
func WithIDefinition(value Value, opts ...options.Option) IAEOption {
	f := func(cfg *iaeConfig) error {
		iDef, err := NewItemDefinition(value, opts...)
		if err != nil {
			return fmt.Errorf("couldn't created ItemDefinition: %w", err)
		}

		cfg.iDef = iDef

		return nil
	}

	return IAEOption(f)
}

// WithIDef sets actual ItemDefinition of IAE.
func WithIDef(iDef *ItemDefinition) IAEOption {
	f := func(cfg *iaeConfig) error {
		if iDef == nil {
			return fmt.Errorf("no ItemDefinition")
		}

		cfg.iDef = iDef

		return nil
	}

	return IAEOption(f)
}

// ------------------- options.Option interface -------------------------------

// Apply runs IAEOption on given cfg if its cast to iaeConfig.
func (iaeO IAEOption) Apply(cfg options.Configurator) error {
	if iaeC, ok := cfg.(*iaeConfig); ok {
		return iaeO(iaeC)
	}

	return fmt.Errorf("not IEA config (%s)", reflect.TypeOf(cfg).String())
}

// ------------------ options.Configurator interface --------------------------

// Validate checks iaeC consistency.
func (iaeC *iaeConfig) Validate() error {
	if iaeC.iDef == nil {
		return fmt.Errorf("no ItemDefinition")
	}

	if iaeC.iDef.Structure() == nil && iaeC.state != UndefinedSrcState {
		return fmt.Errorf("invalid data state %q with empty ItemDefinition",
			iaeC.state.name)
	}

	return nil
}

// ============================================================================
// IAEAdder and IAEAdderOption provides an functionality to add
// option-like configuration for adding ItemAwareItem to Property or Parameter
// ============================================================================

type (
	// IAEAdder represents an interface for adding ItemAwareElements.
	IAEAdder interface {
		options.Configurator

		AddIAE(iae *ItemAwareElement) error
	}

	// IAEAdderOption is a function type for adding ItemAwareElement
	IAEAdderOption func(cfg IAEAdder) error
)

// WithIAE adds ItemAwareElement to the cfg which implements IAEAdder interface
//
// Available options:
//   - data.IDef
//   - data.IDefinition
//   - data.WithState
//   - foundation.WithId
//   - foundation.WithDoc
func WithIAE(opts ...options.Option) IAEAdderOption {
	f := func(cfg IAEAdder) error {
		iae, err := NewIAE(opts...)
		if err != nil {
			return fmt.Errorf("ItemAwareElement building failed: %w", err)
		}

		if err := cfg.AddIAE(iae); err != nil {
			return fmt.Errorf("ItemAwareElement adding failed: %w", err)
		}

		return nil
	}

	return IAEAdderOption(f)
}

// ---------------------- options.Option interface ----------------------------

// Apply applies the IAEAdderOption to the provided configurator.
func (iaeO IAEAdderOption) Apply(cfg options.Configurator) error {
	if iaeC, ok := cfg.(IAEAdder); ok {
		return iaeO(iaeC)
	}

	return errs.New(
		errs.M("invlaid configuration type"),
		errs.C(errorClass, errs.TypeCastingError),
		errs.D("config_type", reflect.TypeOf(cfg).String()))
}

// ----------------------------------------------------------------------------
