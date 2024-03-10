package foundation

import (
	"reflect"
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/google/uuid"
)

type (
	// baseConfig holds configuration for BaseElement building
	baseConfig struct {
		id   string
		docs []Documentation
	}

	// BaseOptions sets one BaseConfig fields
	BaseOption func(*baseConfig) error
)

// Validate implements options.Configurator interface.
func (bc *baseConfig) Validate() error {
	return nil
}

// Apply implements options.Option interface for BaseOption
func (bo BaseOption) Apply(cfg options.Configurator) error {
	if bc, ok := cfg.(*baseConfig); ok {
		return bo(bc)
	}

	return &errs.ApplicationError{
		Message: "not BaseConfig",
		Classes: []string{
			errorClass,
			errs.TypeCastingError,
		},
		Details: map[string]string{
			"cast_from": reflect.TypeOf(cfg).String(),
		},
	}
}

// WithId updates id field in BaseConfig.
func WithId(id string) options.Option {
	id = strings.Trim(id, " ")

	f := func(bc *baseConfig) error {
		if id == "" {
			return &errs.ApplicationError{
				Message: "empty id isn't allowed",
				Classes: []string{
					errorClass,
					errs.InvalidParameter,
				},
			}
		}

		bc.id = id

		return nil
	}

	return BaseOption(f)
}

// WithDocs updates docs element of BaseConfig.
func WithDocs(docs ...*Documentation) options.Option {
	f := func(bc *baseConfig) error {
		if bc.docs == nil {
			bc.docs = []Documentation{}
		}

		for _, d := range docs {
			bc.docs = append(bc.docs, *d)
		}

		return nil
	}

	return BaseOption(f)
}

// baseElement creates a new BaseElement from BaseConfig.
func (bc *baseConfig) baseElement() *BaseElement {
	if bc.id == "" {
		bc.id = uuid.Must(uuid.NewRandom()).String()
	}

	if bc.docs == nil {
		bc.docs = []Documentation{}
	}

	return &BaseElement{
		id:   bc.id,
		docs: append([]Documentation{}, bc.docs...),
	}
}
