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

// apply implements ItemOption interface for itemOption functor.
func (o itemOption) Apply(cfg any) error {
	if ic, ok := cfg.(*itemConfig); ok {
		return o(ic)
	}

	return &errs.ApplicationError{
		Message: "not itemConfig",
		Classes: []string{
			errorClass,
			errs.TypeCastingError,
		},
		Details: map[string]string{
			"cast_from": reflect.TypeOf(cfg).String(),
		},
	}
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
		if kind != Information && kind != Physical {
			return &errs.ApplicationError{
				Message: "kind could be ony Information or Physical",
				Classes: []string{
					errorClass,
					errs.InvalidParameter,
				},
				Details: map[string]string{
					"kind": string(kind),
				},
			}
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
