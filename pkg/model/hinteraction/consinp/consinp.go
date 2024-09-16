/*
consinp is a package which implement Rendered interface for
user input from console.
*/

package consinp

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"reflect"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"

	hi "github.com/dr-dobermann/gobpm/pkg/model/hinteraction"
)

const (
	errorClass = "CONSOLE_RENDERER_ERRORS"

	ConsInpRender = "##consInputRender"
)

type (
	input interface {
		name() string
		read(io.Reader) (data.Data, error)
	}

	CRenderer struct {
		foundation.BaseElement

		src io.Reader

		inputs []input
	}
)

// ============================================================================

// createData creates a new data.Data objects from the input inp and value v.
func createData(name string, v data.Value) (data.Data, error) {
	idef, err := data.NewItemDefinition(v)
	if err != nil {
		return nil, fmt.Errorf("couldn't create an itemDefinition: %w", err)
	}

	iae, err := data.NewItemAwareElement(idef, data.ReadyDataState)
	if err != nil {
		return nil, fmt.Errorf("couldn't create an ItemAwareElement: %w", err)
	}

	return data.NewParameter(name, iae)
}

// ============================================================================

// NewRenderer creates a new Console Renderer with inputs.
// It fails on inputs duplication by name or value and empty inputs list.
//
// Available options:
//   - consinp.WithInput
//   - foundation.WithId
//   - foundation.WithDoc
func NewRenderer(
	opts ...options.Option,
) (*CRenderer, error) {
	cfg := consInpConfig{
		inputs: []input{},
		source: bufio.NewReader(os.Stdin),
	}

	for _, opt := range opts {
		switch o := opt.(type) {
		case ciOption:
			err := o.Apply(&cfg)
			if err != nil {
				return nil,
					errs.New(
						errs.M("option applying failed"),
						errs.C(errorClass, errs.BulidingFailed),
						errs.E(err))
			}

		case foundation.BaseOption:
			cfg.baseOpts = append(cfg.baseOpts, opt)

		default:
			return nil,
				errs.New(
					errs.M("invalid option"),
					errs.C(errorClass, errs.BulidingFailed),
					errs.D("option_type", reflect.TypeOf(o).String()))
		}
	}

	be, err := foundation.NewBaseElement(cfg.baseOpts...)
	if err != nil {
		return nil,
			errs.New(
				errs.M("couldn't create BaseElement"),
				errs.C(errorClass, errs.BulidingFailed),
				errs.E(err))
	}

	return cfg.newCRenderer(be)
}

func (cr *CRenderer) Implementation() string {
	return ConsInpRender
}

// ------------------- human_interaction.Renderer interface -------------------

// Render presents the CRenderer's prompts and gather user inputs.
func (cr *CRenderer) Render(_ data.Source) ([]data.Data, error) {
	if len(cr.inputs) == 0 {
		return nil,
			errs.New(
				errs.M("no inputs defined"),
				errs.C(errorClass, errs.InvalidObject),
				errs.D("renderer_id", cr.Id()))
	}

	results := []data.Data{}

	for _, inp := range cr.inputs {
		d, err := inp.read(cr.src)
		if err != nil {
			return nil,
				errs.New(
					errs.M("couldn't read input"),
					errs.C(errorClass, errs.OperationFailed),
					errs.D("input_name", inp.name()),
					errs.E(err))
		}

		if d != nil {
			results = append(results, d)
		}
	}

	return results, nil
}

// ----------------------------------------------------------------------------

// interface check

var _ hi.Renderer = (*CRenderer)(nil)
