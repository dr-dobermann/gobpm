package flow

import (
	"reflect"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

type sflowConfig struct {
	name string
	cond data.FormalExpression
	src  SequenceSource
	trg  SequenceTarget

	// if set then flows added into the same container where src is.
	putInSrcContainer bool

	baseOpts []options.Option
}

type sflowOption func(cfg *sflowConfig) error

// ---------------- options.Option interface -----------------------------------
func (so sflowOption) Apply(cfg options.Configurator) error {
	if fc, ok := cfg.(*sflowConfig); ok {
		return so(fc)
	}

	return errs.New(
		errs.M("config doesn't suppurt figurator"),
		errs.C(errorClass, errs.TypeCastingError),
		errs.D("config_type", reflect.TypeOf(cfg).String()))
}

// --------------- options.Configureator interface -----------------------------

// Validate checks SequenceFlow configureation errors.
func (fc *sflowConfig) Validate() error {
	// for the moment there is no valuable validation for SequenceFlow config.
	return nil
}

// --------------- option.NameConfigureator interface --------------------------

// SetName sets name of the SequenceFlow.
func (fc *sflowConfig) SetName(name string) error {
	fc.name = name

	return nil
}

// -----------------------------------------------------------------------------

// newSequenceFlow creates a new SequenceFlow from the configuration.
func (fc *sflowConfig) newSequenceFlow() (*SequenceFlow, error) {
	fe, err := NewFlowElement(fc.name, fc.baseOpts...)
	if err != nil {
		return nil, err
	}

	f := SequenceFlow{
		FlowElement:         *fe,
		source:              fc.src,
		target:              fc.trg,
		conditionExpression: fc.cond,
	}

	return &f, nil
}

// WithCondition sets SequenceFlow condition.
func WithCondition(cond data.FormalExpression) options.Option {
	f := func(fc *sflowConfig) error {
		if cond == nil {
			return errs.New(
				errs.M("condition couldn't be empty"),
				errs.C(errorClass, errs.EmptyNotAllowed))
		}

		fc.cond = cond

		return nil
	}

	return sflowOption(f)
}

// WithoutContainer select mode of SequenceFlow creation when it
// wouldn't add to the same container as sequence source.
// func WithoutContainer() options.Option {
// 	f := func(fc *sflowConfig) error {
// 		fc.putInSrcContainer = false
//
// 		return nil
// 	}
//
// 	return sflowOption(f)
// }
