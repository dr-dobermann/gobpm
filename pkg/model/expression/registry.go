package expression

import (
	"context"
	"github.com/dr-dobermann/gobpm/pkg/observability"
	"sort"
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
)

const (
	errorClass = "EXPRESSION"

	// NoneType is the empty Registry's kind: no engine is registered and
	// every evaluation fails loud (the WithoutDefaultExpressionEngines
	// posture, ADR-032 §2.1).
	NoneType = "##None"
)

// Registry is the core expression-engine router (ADR-032 §2.1):
// registered engines' language claims fold into a language→engine map at
// construction, and the Registry is itself an Engine — every runtime
// consumer (conditions, timers, MI, correlation, the dispatcher binder)
// talks to one interface however many engines are wired. Immutable after
// construction: built before the engine runs, read concurrently without
// locks.
type Registry struct {
	byLanguage map[string]Engine
	kinds      []string
	languages  []string
}

// interface check
var _ Engine = (*Registry)(nil)

// NewRegistry folds the engines' language claims into a routing map. A
// nil engine, an engine with no claims, a blank claim, or a duplicate
// claim (two engines answering the same language) reject construction —
// routing stays deterministic and operator-visible, never silently
// shadowed.
func NewRegistry(engines ...Engine) (*Registry, error) {
	reg := &Registry{byLanguage: map[string]Engine{}}

	for i, e := range engines {
		if e == nil {
			return nil, errs.New(
				errs.M("NewRegistry: a nil Engine isn't allowed (engine %d)",
					i),
				errs.C(errorClass, errs.EmptyNotAllowed))
		}

		ll := e.Languages()
		if len(ll) == 0 {
			return nil, errs.New(
				errs.M("NewRegistry: engine claims no languages"),
				errs.C(errorClass, errs.EmptyNotAllowed),
				errs.D("engine", e.Type()))
		}

		for _, l := range ll {
			nl := normalize(l)
			if nl == "" {
				return nil, errs.New(
					errs.M("NewRegistry: a blank language claim isn't allowed"),
					errs.C(errorClass, errs.EmptyNotAllowed),
					errs.D("engine", e.Type()))
			}

			if prev, ok := reg.byLanguage[nl]; ok {
				return nil, errs.New(
					errs.M("NewRegistry: language is claimed twice"),
					errs.C(errorClass, errs.DuplicateObject),
					errs.D("language", nl),
					errs.D("engines", prev.Type()+", "+e.Type()))
			}

			reg.byLanguage[nl] = e
			reg.languages = append(reg.languages, nl)
		}

		reg.kinds = append(reg.kinds, e.Type())
	}

	sort.Strings(reg.languages)

	return reg, nil
}

// normalize canonicalizes an expression-language URI for routing.
func normalize(language string) string {
	return strings.ToLower(strings.TrimSpace(language))
}

// Type returns the aggregated kind: "##None" for the empty registry, else
// the registered engine kinds joined in registration order.
func (reg *Registry) Type() string {
	if len(reg.kinds) == 0 {
		return NoneType
	}

	return strings.Join(reg.kinds, "+")
}

// Languages returns the sorted, normalized language claims.
func (reg *Registry) Languages() []string {
	return append([]string{}, reg.languages...)
}

// EngineFor resolves the engine answering language (normalized) — the
// startup routing table prints through it.
func (reg *Registry) EngineFor(language string) (Engine, bool) {
	e, ok := reg.byLanguage[normalize(language)]

	return e, ok
}

// Evaluate routes expr to the engine claiming its Language(). A nil or
// language-less expression, the empty registry and an unclaimed language
// are classified errors — the latter lists the languages actually
// registered, so the failure explains itself (ADR-032 §2.1: the empty
// language is loud; the Definitions-level default-language inheritance
// rides interchange).
func (reg *Registry) Evaluate(
	ctx context.Context,
	expr data.FormalExpression,
	src data.Source,
) (data.Value, error) {
	if expr == nil {
		return nil, errs.New(
			errs.M("Evaluate: a nil FormalExpression isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	lang := normalize(expr.Language())
	if lang == "" {
		return nil, errs.New(
			errs.M("Evaluate: the expression carries no language "+
				"(programmatic expressions always name one)"),
			errs.C(errorClass, errs.EmptyNotAllowed),
			errs.D(observability.AttrExpressionID, expr.ID()))
	}

	if len(reg.byLanguage) == 0 {
		return nil, errs.New(
			errs.M("Evaluate: no expression engine is registered — wire "+
				"one with thresher.WithExpressionEngine"),
			errs.C(errorClass, errs.InvalidState),
			errs.D("language", lang))
	}

	e, ok := reg.byLanguage[lang]
	if !ok {
		return nil, errs.New(
			errs.M("Evaluate: no registered engine claims the "+
				"expression language"),
			errs.C(errorClass, errs.ObjectNotFound),
			errs.D("language", lang),
			errs.D("registered_languages",
				strings.Join(reg.languages, ", ")))
	}

	return e.Evaluate(ctx, expr, src)
}
