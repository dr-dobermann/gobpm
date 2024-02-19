package data

import "github.com/dr-dobermann/gobpm/pkg/model/foundation"

type (
	// ItemOption interface used to declare ItemDefinition object option.
	ItemOption interface {
		apply(*itemConfig) error
	}

	// used as functor interface for ItemDefinition option definition.
	itemOption func(*itemConfig) error

	// itemConfig consist optional parameters for ItemDefinition build.
	itemConfig struct {
		id         string
		docs       []*foundation.Documentation
		kind       ItemKind
		imp        *foundation.Import
		str        Value
		collection bool
	}
)

// apply implements ItemOption interface for itemOption functor.
func (o itemOption) apply(cfg *itemConfig) error {

	return o(cfg)
}

// itemDefBuild builds ItemDefinition object from the itemConfig.
func (ic *itemConfig) itemDef() *ItemDefinition {

	return &ItemDefinition{
		BaseElement:  *foundation.NewBaseElement(ic.id, ic.docs...),
		Kind:         ic.kind,
		Import:       ic.imp,
		Structure:    ic.str,
		isCollection: ic.collection,
	}
}

// SetId sets an ItemDefintion Id.
func SetId(id string) ItemOption {
	f := func(cfg *itemConfig) error {
		cfg.id = id

		return nil
	}

	return itemOption(f)
}

// SetDocumentation adds documentation to an ItemDefintion.
func SetDocumentation(docs ...*foundation.Documentation) ItemOption {
	f := func(cfg *itemConfig) error {
		cfg.docs = append(cfg.docs, docs...)

		return nil
	}

	return itemOption(f)
}

// SetKind sets kind of an ItemDefintion.
func SetKind(kind ItemKind) ItemOption {
	f := func(cfg *itemConfig) error {
		cfg.kind = kind

		return nil
	}

	return itemOption(f)
}

// SetImport sets import of an ItemDefintion.
func SetImport(imp *foundation.Import) ItemOption {
	f := func(cfg *itemConfig) error {
		cfg.imp = imp

		return nil
	}

	return itemOption(f)
}
