// Package rules defines the Business Rule Engine seam (ADR-027 v.1 §2.1):
// a pluggable engine service that evaluates a named decision against the
// process-data read surface. The engine is infrastructure, not a spec-modeled
// artifact — the standard leaves the rule-engine binding open — so the seam
// lives beside the other engine services (the WorkerDispatcher neighborhood),
// not under pkg/model. The batteries-included default is the gorules
// decision registry (subpackage gorules); a DMN or any external rules
// service plugs in behind the same interface.
package rules

import (
	"context"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
)

// Engine evaluates business decisions for the Business Rule Task. An
// implementation resolves the decision reference itself (a registered name
// for the in-core default, a DMN decision id/key for an external engine).
type Engine interface {
	// Type names the engine kind in the standard's "##"-hint convention
	// ("##GoRules", "##DMN", ...) — reported as the task's implementation
	// attribute and in the startup-config printout.
	Type() string

	// Evaluate resolves decisionRef and evaluates it against the read-only
	// process-data surface, returning one structured result item (nil when
	// the decision produces no committable result). An unknown decisionRef
	// is an error, never a silent no-op.
	Evaluate(
		ctx context.Context,
		decisionRef string,
		r service.DataReader,
	) (*data.ItemDefinition, error)
}

// DecisionFunc is an in-process decision body for registry-style engines:
// it reads through the per-execution data reader and returns its result item
// (the gooper.OpFunctor idiom without the message binding). The result
// item's ID names the process variable the outcome commits to; a nil result
// with a nil error means the decision concluded without a committable value.
type DecisionFunc func(
	ctx context.Context,
	r service.DataReader,
) (*data.ItemDefinition, error)
