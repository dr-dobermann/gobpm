package errs

import (
	"fmt"
	"strings"
)

const (
	defaultMessage = "unspecified error"
)

type (
	errConfig struct {
		err     error
		details map[string]string
		msg     string
		classes []string
	}

	// Option configures the ApplicationError that New builds. The options are
	// E, M, C and D; the interface is sealed, so no other package implements it.
	Option interface {
		apply(cfg *errConfig) error
	}

	errFunc func(cfg *errConfig) error
)

// ------------------ Option interface --------------------------------------
//
// apply implements the Option interface for errFunc.
func (ef errFunc) apply(cfg *errConfig) error {
	if cfg == nil {
		return fmt.Errorf("empty error configuration")
	}

	return ef(cfg)
}

// newError creates a new ApplicationError from the errConfig.
func (cfg *errConfig) newError() *ApplicationError {
	return &ApplicationError{
		Err:     cfg.err,
		Message: cfg.msg,
		Classes: cfg.classes,
		Details: cfg.details,
	}
}

// E adds error into errConfig.
func E(err error) Option {
	f := func(cfg *errConfig) error {
		if err == nil {
			return fmt.Errorf("empty error")
		}

		cfg.err = err

		return nil
	}

	return errFunc(f)
}

// M fills the message in errConfig.
func M(format string, values ...any) Option {
	f := func(cfg *errConfig) error {
		format = strings.Trim(format, " ")
		if format == "" {
			return fmt.Errorf("empty error message format")
		}

		cfg.msg = fmt.Sprintf(format, values...)

		return nil
	}

	return errFunc(f)
}

// C adds error classes into the errConfig.
func C(classes ...string) Option {
	f := func(cfg *errConfig) error {
		for _, c := range classes {
			c = strings.Trim(c, " ")
			if c != "" {
				cfg.classes = append(cfg.classes, c)
			}
		}

		return nil
	}

	return errFunc(f)
}

// D adds a key/value diagnostic detail to the errConfig. The value is a
// pre-stringified string: errs stores and renders details without reflection
// or boxing, keeping error construction cheap on hot paths (FIX-019). Callers
// stringify non-string values explicitly (strconv.Itoa, string(namedType),
// x.ID()) — do not reintroduce `any` here.
func D(k, v string) Option {
	f := func(cfg *errConfig) error {
		k = strings.Trim(k, " ")
		if k != "" && v != "" {
			cfg.details[k] = v
		}

		return nil
	}

	return errFunc(f)
}
