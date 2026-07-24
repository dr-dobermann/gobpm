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

// Row is one decision result record — output name → value — the
// DMN-universal result element. A full decision result is a list of Rows:
// single-hit policies yield one, multi-hit policies (Rule Order, Collect)
// yield many.
type Row map[string]data.Value

// Engine evaluates business decisions for the Business Rule Task. An
// implementation resolves the decision reference itself (a registered name
// for the in-core default, a DMN decision id/key for an external engine).
type Engine interface {
	// Type names the engine kind in the standard's "##"-hint convention
	// ("##GoRules", "##DMN", ...) — reported as the task's implementation
	// attribute and in the startup-config printout.
	Type() string

	// Evaluate resolves decisionRef and evaluates it against the read-only
	// process-data surface, returning the decision result rows (nil or
	// empty when the decision produces no committable result). An unknown
	// decisionRef is an error, never a silent no-op.
	Evaluate(
		ctx context.Context,
		decisionRef string,
		r service.DataReader,
	) ([]Row, error)
}

// Deployer is the ingestion half of the minimal two-operation rule-engine
// component contract (deploy + evaluate — ADR-027 v.1 §2.1): it validates an
// external decision definition and caches its executable form for later
// Evaluate calls. Engines that ingest external artifacts (a DMN adapter, the
// table-driven engine) implement it beside the Engine seam; the Business
// Rule Task itself never deploys — deployment is the embedder's platform
// operation.
type Deployer interface {
	Deploy(ctx context.Context, definition []byte) error
}

// DecisionFunc is an in-process decision body for registry-style engines:
// it reads through the per-execution data reader and returns its result row
// (the gooper.OpFunctor idiom without the message binding). A function
// registry is a single implicit hit — a decision yields at most one Row; a
// nil Row with a nil error means the decision concluded without a
// committable result.
type DecisionFunc func(
	ctx context.Context,
	r service.DataReader,
) (Row, error)
