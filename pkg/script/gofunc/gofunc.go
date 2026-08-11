// Package gofunc is the dependency-free Script Task engine: the BPMN script
// text names a Go function the host registered, and the engine calls it.
//
// It exists because every other extension point has an in-core implementation
// that costs no third-party dependency, and the Script Task did not. Lua
// (adapters/lua, SRD-065) is a real script engine and the right choice when a
// model carries interpreted source — but it brings an interpreter with it, and
// the core holds to stdlib + uuid (SAD-001 G2). So the in-core option executes
// the one language the core already has: Go.
//
// It is the same move gooper makes for Service Tasks. The host writes Go,
// registers it under a name, and the model refers to the name:
//
//	th, err := thresher.New("engine",
//		thresher.WithScriptEngine(gofunc.New(
//			gofunc.WithScript("total", func(
//				ctx context.Context, r service.DataReader,
//			) (script.Outputs, error) {
//				// … read from r, compute, return named outputs
//			}))))
//
// The limit, stated so nobody mistakes this for more than it is: a .bpmn file
// authored elsewhere carries inline source, not a name, so this engine does not
// make such a model run. It makes a gobpm-authored model with a Script Task run
// with no third-party dependency, and gives a migration a named seam to bind to.
package gofunc

import (
	"context"
	"sort"
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/script"
)

const (
	errorClass = "GOFUNC_ERRORS"

	// GoFuncType is the engine kind, following the "##"-hint convention
	// adapters/lua uses for "##Lua".
	GoFuncType = "##GoFunc"
)

// formats are the scriptFormat hints this engine claims. A model names one of
// these in its Script Task's scriptFormat so the registry routes to it.
var formats = []string{"application/x-gobpm-gofunc", "gofunc"}

// ScriptFunc is a script body written in Go. It receives the per-execution
// read-only data reader — the same one every other engine is given — and
// returns the named outputs the Script Task commits to its scope.
type ScriptFunc func(
	ctx context.Context,
	r service.DataReader,
) (script.Outputs, error)

// Engine is a registry of named Go functions, satisfying script.Engine.
type Engine struct {
	scripts map[string]ScriptFunc
}

// Option configures the engine at construction.
type Option func(*Engine) error

// WithScript registers f under name. The name is what a model's Script Task
// carries as its script text.
//
// A duplicate or empty name fails at construction rather than at execution: a
// model that will not run should say so when the engine is built, not when a
// token reaches the task.
func WithScript(name string, f ScriptFunc) Option {
	return func(e *Engine) error {
		// Trim HERE, not only in the guard. Execute trims before looking up,
		// so a name stored with surrounding whitespace would be registered
		// successfully and then be permanently unreachable.
		name = strings.TrimSpace(name)

		if name == "" {
			return errs.New(
				errs.M("WithScript: an empty script name isn't allowed"),
				errs.C(errorClass, errs.EmptyNotAllowed))
		}

		if f == nil {
			return errs.New(
				errs.M("WithScript: a nil ScriptFunc isn't allowed for %q", name),
				errs.C(errorClass, errs.EmptyNotAllowed))
		}

		if _, dup := e.scripts[name]; dup {
			return errs.New(
				errs.M("WithScript: %q is already registered", name),
				errs.C(errorClass, errs.InvalidParameter))
		}

		e.scripts[name] = f

		return nil
	}
}

// New builds the engine over the registered scripts.
func New(opts ...Option) (*Engine, error) {
	e := &Engine{scripts: map[string]ScriptFunc{}}

	for _, o := range opts {
		if err := o(e); err != nil {
			return nil, err
		}
	}

	return e, nil
}

// Type returns the engine kind.
func (e *Engine) Type() string { return GoFuncType }

// Formats returns the claimed scriptFormat hints.
func (e *Engine) Formats() []string {
	return append([]string{}, formats...)
}

// Execute resolves name in the registry and runs it.
//
// An unregistered name reports what IS registered, because that is the failure
// a modeler actually hits — a message naming only what is missing leaves them
// guessing at the spelling.
func (e *Engine) Execute(
	ctx context.Context,
	_, name string,
	r service.DataReader,
) (script.Outputs, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errs.New(
			errs.M("Execute: an empty script name isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if r == nil {
		return nil, errs.New(
			errs.M("Execute: a nil DataReader isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	f, ok := e.scripts[name]
	if !ok {
		return nil, errs.New(
			errs.M("Execute: no script %q is registered (have: %s)",
				name, strings.Join(e.names(), ", ")),
			errs.C(errorClass, errs.ObjectNotFound))
	}

	return f(ctx, r)
}

// names lists the registered script names, sorted so an error message is
// stable between runs.
func (e *Engine) names() []string {
	nn := make([]string, 0, len(e.scripts))
	for n := range e.scripts {
		nn = append(nn, n)
	}

	sort.Strings(nn)

	return nn
}
