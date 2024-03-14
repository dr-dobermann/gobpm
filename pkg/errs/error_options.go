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
		msg     string
		classes []string
		details map[string]string
	}

	errOption interface {
		apply(cfg *errConfig) error
	}

	errFunc func(cfg *errConfig) error
)

// ------------------ errOption interface --------------------------------------
//
// apply implements errOption interface for errFunc.
func (ef errFunc) apply(cfg *errConfig) error {
	if cfg == nil {
		return fmt.Errorf("empty error configureation")
	}

	return ef(cfg)
}

// newError creates a new ApplicationError from the errConfig.
func (cfg *errConfig) newError() *ApplicationError {
	return &ApplicationError{
		Err:     cfg.err,
		Message: cfg.msg,
		Classes: cfg.classes,
		Details: cfg.details}
}

// E adds error into errConfig.
func E(err error) errOption {
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
func M(format string, values ...any) errOption {
	f := func(cfg *errConfig) error {
		format := strings.Trim(format, " ")
		if format == "" {
			return fmt.Errorf("empty error message format")
		}

		cfg.msg = fmt.Sprintf(format, values...)

		return nil
	}

	return errFunc(f)
}

// C adds error classes into the errConfig.
func C(classes ...string) errOption {
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

// D adds the errConfig details.
func D(k, v string) errOption {
	f := func(cfg *errConfig) error {
		k := strings.Trim(k, " ")
		v := strings.Trim(v, " ")
		if k != "" && v != "" {
			cfg.details[k] = v
		}

		return nil
	}

	return errFunc(f)
}
