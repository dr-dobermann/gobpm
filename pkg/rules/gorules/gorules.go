// Package gorules provides the batteries-included Business Rule Engine
// (ADR-027 v.1 §2.4): a bounded registry of named in-process Go decisions.
// It grows only by explicit registration, evaluates by registered name, and
// fails loud on an unknown reference — never a silent default. Any external
// rules service (DMN or otherwise) replaces it wholesale through the
// rules.Engine seam.
package gorules

import (
	"context"
	"sync"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/rules"
)

const (
	errorClass = "GORULES"

	// GoRulesType is the implementation mechanism of the in-core decision
	// registry (the "##"-hint convention).
	GoRulesType = "##GoRules"
)

// Registry is the in-core rules.Engine: a static map of named decisions
// populated by explicit registration. Register write-locks (setup time);
// Evaluate read-locks, so concurrent tracks evaluate freely.
type Registry struct {
	decisions map[string]rules.DecisionFunc
	mu        sync.RWMutex
}

// interface check
var _ rules.Engine = (*Registry)(nil)

// New creates an empty decision registry.
func New() *Registry {
	return &Registry{
		decisions: map[string]rules.DecisionFunc{},
	}
}

// Register adds the named decision. The name must be non-empty and unique;
// the decision body must be non-nil.
func (reg *Registry) Register(name string, d rules.DecisionFunc) error {
	if name == "" {
		return errs.New(
			errs.M("Register: an empty decision name isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if d == nil {
		return errs.New(
			errs.M("Register: a nil DecisionFunc isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed),
			errs.D("decision_name", name))
	}

	reg.mu.Lock()
	defer reg.mu.Unlock()

	if _, ok := reg.decisions[name]; ok {
		return errs.New(
			errs.M("Register: decision is already registered"),
			errs.C(errorClass, errs.DuplicateObject),
			errs.D("decision_name", name))
	}

	reg.decisions[name] = d

	return nil
}

// MustRegister is the panic-on-error Register twin for fixture and example
// building; it returns the registry for chaining.
func (reg *Registry) MustRegister(
	name string,
	d rules.DecisionFunc,
) *Registry {
	if err := reg.Register(name, d); err != nil {
		errs.Panic(err)

		return nil
	}

	return reg
}

// Type returns the registry's implementation kind.
func (reg *Registry) Type() string {
	return GoRulesType
}

// Evaluate runs the decision registered under decisionRef against the data
// reader. A function registry is a single implicit hit: a non-nil row wraps
// as a one-row result, a nil row as an empty one. An unregistered reference
// is a classified error.
func (reg *Registry) Evaluate(
	ctx context.Context,
	decisionRef string,
	r service.DataReader,
) ([]rules.Row, error) {
	if decisionRef == "" {
		return nil, errs.New(
			errs.M("Evaluate: an empty decision reference isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if r == nil {
		return nil, errs.New(
			errs.M("Evaluate: a nil DataReader isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed),
			errs.D("decision_ref", decisionRef))
	}

	reg.mu.RLock()
	d, ok := reg.decisions[decisionRef]
	reg.mu.RUnlock()

	if !ok {
		return nil, errs.New(
			errs.M("Evaluate: decision isn't registered"),
			errs.C(errorClass, errs.ObjectNotFound),
			errs.D("decision_ref", decisionRef))
	}

	row, err := d(ctx, r)
	if err != nil {
		return nil, errs.New(
			errs.M("decision evaluation failed"),
			errs.C(errorClass, errs.OperationFailed),
			errs.E(err),
			errs.D("decision_ref", decisionRef))
	}

	if row == nil {
		return nil, nil
	}

	return []rules.Row{row}, nil
}
