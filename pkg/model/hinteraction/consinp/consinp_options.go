package consinp

import (
	"fmt"
	"io"
	"reflect"
	"slices"
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

type (
	consInpConfig struct {
		inputs   []input
		source   io.Reader
		baseOpts []options.Option
	}

	ciOption func(cfg *consInpConfig) error
)

// newCRenderer creates a new CRenderer from the ciCfg.
func (ciCfg *consInpConfig) newCRenderer(
	be *foundation.BaseElement,
) (*CRenderer, error) {
	if err := ciCfg.Validate(); err != nil {
		return nil,
			fmt.Errorf("invalid Console Renderer configuration: %w", err)
	}

	return &CRenderer{
		BaseElement: *be,
		inputs:      ciCfg.inputs,
		src:         ciCfg.source,
	}, nil
}

// WithSource sets non-empty console input source for CRenderer.
// src is used as an input source for rendering form.
// Values in src should be divided by '\n'
func WithSource(src io.Reader) options.Option {
	f := func(ciCfg *consInpConfig) error {
		if src == nil {
			return fmt.Errorf("no source")
		}

		ciCfg.source = src

		return nil
	}

	return ciOption(f)
}

// WithIntInput adds input for integer value.
//
// Parameters:
//   - name defines the name of the data output
//   - prompt describes the input (could be empty)
func WithIntInput(name, prompt string) options.Option {
	f := func(ciCfg *consInpConfig) error {
		prompt = strings.TrimSpace(prompt)
		name = strings.TrimSpace(name)
		if name == "" {
			return fmt.Errorf("empty name isn't allowed")
		}

		if prompt == "" {
			prompt = name
		}

		if slices.ContainsFunc(ciCfg.inputs,
			func(i input) bool {
				return i.name() == name
			}) {
			return errs.New(
				errs.M("duplicate input name"),
				errs.C(errorClass, errs.DuplicateObject),
				errs.D("input_name", name))
		}

		ciCfg.inputs = append(ciCfg.inputs, &intInput{
			inputBase: inputBase{
				inputName: name,
				prompt:    prompt,
			},
		})

		return nil
	}

	return ciOption(f)
}

// WithStringInput adds input for string value.
//
// Parameters:
//   - name defines the name of output data of the render
//   - prompt describes the input (could be empty)
func WithStringInput(name, prompt string) options.Option {
	f := func(ciCfg *consInpConfig) error {
		prompt = strings.TrimSpace(prompt)
		name = strings.TrimSpace(name)
		if name == "" {
			return fmt.Errorf("empty name isn't allowed")
		}

		if prompt == "" {
			prompt = name
		}

		if slices.ContainsFunc(ciCfg.inputs,
			func(i input) bool {
				return i.name() == name
			}) {
			return errs.New(
				errs.M("duplicate input name"),
				errs.C(errorClass, errs.DuplicateObject),
				errs.D("input_name", name))
		}

		ciCfg.inputs = append(ciCfg.inputs, &stringInput{
			inputBase: inputBase{
				inputName: name,
				prompt:    prompt,
			},
		})

		return nil
	}

	return ciOption(f)
}

// WithMessager adds a new message to the render form.
// This field doesn't provide any output data.
//
// Parameter:
//   - name could be empty
//   - prompt defines the message which will be shown on render form.
func WithMessager(name, prompt string) options.Option {
	f := func(ciCfg *consInpConfig) error {
		prompt = strings.TrimSpace(prompt)
		name = strings.TrimSpace(name)
		if name == "" {
			name = "messager"
		}

		if prompt == "" {
			return fmt.Errorf("no message in messager")
		}

		ciCfg.inputs = append(ciCfg.inputs, &messager{
			inputBase: inputBase{
				inputName: name,
				prompt:    prompt,
			},
		})

		return nil
	}

	return ciOption(f)
}

// -------------- options.Configurator interface ------------------------------

// Validate checks consistency of the conInpConfig.
func (ciCfg *consInpConfig) Validate() error {
	if len(ciCfg.inputs) == 0 {
		return fmt.Errorf("no inputs")
	}

	if ciCfg.source == nil {
		return fmt.Errorf("no input source")
	}

	return nil
}

// --------------- options.Option interface -----------------------------------

// Apply adds option to the configuration cfg.
func (cio ciOption) Apply(cfg options.Configurator) error {
	if ciCfg, ok := cfg.(*consInpConfig); ok {
		return cio(ciCfg)
	}

	return errs.New(
		errs.M("not consInpConfig"),
		errs.C(errorClass, errs.TypeCastingError),
		errs.D("config_type", reflect.TypeOf(cfg).String()))
}

// -----------------------------------------------------------------------------
