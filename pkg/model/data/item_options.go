package data

import (
	"reflect"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

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

func (ic *itemConfig) Validate() error {
	return nil
}

// apply implements ItemOption interface for itemOption functor.
func (o itemOption) Apply(cfg options.Configurator) error {
	if ic, ok := cfg.(*itemConfig); ok {
		return o(ic)
	}

	return errs.New(
		errs.M("not itemConfig: %s", reflect.TypeOf(cfg).String()),
		errs.C(errorClass, errs.TypeCastingError))
}

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

// SetKind sets kind of an ItemDefintion.
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

// SetImport sets import of an ItemDefintion.
func WithImport(imp *foundation.Import) options.Option {
	f := func(cfg *itemConfig) error {
		cfg.imp = imp

		return nil
	}

	return itemOption(f)
}
