package script

import (
	"context"
	"sort"
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
)

const (
	errorClass = "SCRIPT"

	// NoneType is the empty Registry's kind: no engine is registered and
	// every execution fails loud (ADR-031 §2.2).
	NoneType = "##None"
)

// Registry is the core script-engine router (ADR-031 §2.1): registered
// engines' format claims fold into a format→engine map at construction,
// and the Registry itself is an Engine, so the Script Task talks to one
// interface regardless of how many interpreters are wired. It is immutable
// after construction — built before the engine runs, read concurrently
// without locks.
type Registry struct {
	byFormat map[string]Engine
	kinds    []string
	formats  []string
}

// interface check
var _ Engine = (*Registry)(nil)

// NewRegistry folds the engines' format claims into a routing map. A nil
// engine, an engine with no format claims, a blank claim, or a duplicate
// claim (two engines answering the same format) reject construction —
// routing stays deterministic and operator-visible, never silently
// shadowed.
func NewRegistry(engines ...Engine) (*Registry, error) {
	reg := &Registry{byFormat: map[string]Engine{}}

	for i, e := range engines {
		if e == nil {
			return nil, errs.New(
				errs.M("NewRegistry: a nil Engine isn't allowed (engine %d)",
					i),
				errs.C(errorClass, errs.EmptyNotAllowed))
		}

		ff := e.Formats()
		if len(ff) == 0 {
			return nil, errs.New(
				errs.M("NewRegistry: engine claims no formats"),
				errs.C(errorClass, errs.EmptyNotAllowed),
				errs.D("engine", e.Type()))
		}

		for _, f := range ff {
			nf := normalize(f)
			if nf == "" {
				return nil, errs.New(
					errs.M("NewRegistry: a blank format claim isn't allowed"),
					errs.C(errorClass, errs.EmptyNotAllowed),
					errs.D("engine", e.Type()))
			}

			if prev, ok := reg.byFormat[nf]; ok {
				return nil, errs.New(
					errs.M("NewRegistry: format is claimed twice"),
					errs.C(errorClass, errs.DuplicateObject),
					errs.D("format", nf),
					errs.D("engines", prev.Type()+", "+e.Type()))
			}

			reg.byFormat[nf] = e
			reg.formats = append(reg.formats, nf)
		}

		reg.kinds = append(reg.kinds, e.Type())
	}

	sort.Strings(reg.formats)

	return reg, nil
}

// normalize canonicalizes a scriptFormat MIME hint for routing.
func normalize(format string) string {
	return strings.ToLower(strings.TrimSpace(format))
}

// Type returns the aggregated kind: "##None" for the empty registry, else
// the registered engine kinds joined in registration order.
func (reg *Registry) Type() string {
	if len(reg.kinds) == 0 {
		return NoneType
	}

	return strings.Join(reg.kinds, "+")
}

// Formats returns the sorted, normalized format claims.
func (reg *Registry) Formats() []string {
	return append([]string{}, reg.formats...)
}

// EngineFor resolves the engine answering format (normalized) — the Script
// facts name the routed engine's kind through it.
func (reg *Registry) EngineFor(format string) (Engine, bool) {
	e, ok := reg.byFormat[normalize(format)]

	return e, ok
}

// Execute routes the script to the engine claiming format. An empty
// registry and an unclaimed format are classified errors — the latter
// lists the formats actually registered, so the failure explains itself.
func (reg *Registry) Execute(
	ctx context.Context,
	format, script string,
	r service.DataReader,
) (Outputs, error) {
	if normalize(format) == "" {
		return nil, errs.New(
			errs.M("Execute: an empty script format isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if r == nil {
		return nil, errs.New(
			errs.M("Execute: a nil DataReader isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed),
			errs.D("script_format", format))
	}

	if len(reg.byFormat) == 0 {
		return nil, errs.New(
			errs.M("Execute: no script engine is registered — wire one "+
				"(e.g. adapters/lua) with thresher.WithScriptEngine"),
			errs.C(errorClass, errs.InvalidState),
			errs.D("script_format", format))
	}

	e, ok := reg.EngineFor(format)
	if !ok {
		return nil, errs.New(
			errs.M("Execute: no registered engine claims the script format"),
			errs.C(errorClass, errs.ObjectNotFound),
			errs.D("script_format", normalize(format)),
			errs.D("registered_formats", strings.Join(reg.formats, ", ")))
	}

	return e.Execute(ctx, format, script, r)
}
