// Package adhoc carries the routing contract of the Ad-Hoc Sub-Process
// (BPMN §13.3.5, ADR-035): the host-supplied decision that replaces
// sequence-flow succession inside an ad-hoc container.
//
// An ad-hoc container holds activities whose order is not fixed by the model.
// Which of them may run — and when the container is finished — is answered by
// a Router the modeler attaches to the Sub-Process. The engine consults it
// when the container's scope opens and again after each inner activity
// settles; an empty answer ends the ad-hoc work.
package adhoc

import (
	"context"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
)

// Router answers "what may run next" inside an Ad-Hoc Sub-Process, returning
// the ids of the inner activities to run. An empty result ends the asking
// track, and when the container's last track ends the Ad-Hoc Sub-Process
// completes — routing and completion are the same decision seen at two levels.
//
// Implementations must be prompt, read-only and free of blocking I/O. A Router
// runs while an instance is executing, so waiting inside Next stalls work that
// could otherwise proceed; waiting for a human to choose is expressed by manual
// selection (the engine offers the candidates and resumes when one is
// activated), never by a Router that blocks. A decision needing remote data
// reads it from the scope, where an earlier activity put it.
type Router interface {
	Next(ctx context.Context, s State) ([]string, error)
}

// Evaluator evaluates a BPMN expression against the container's scope. A Router
// receives one because a DataReader cannot evaluate: the engine routes an
// expression to the engine of its language (ADR-032), and a Router calling
// FormalExpression.Evaluate itself would bypass that routing.
type Evaluator interface {
	Evaluate(
		ctx context.Context, expr data.FormalExpression,
	) (data.Value, error)
}

// State is what a routing decision may rest on: the container's activities,
// how far they have progressed, and the data visible to them.
//
// Every activity here is named by its **id** — Activities, both counters and
// Last all speak ids, because ids are unique where names are not. Next may
// answer with a name (it resolves either way), but a Router that correlates its
// answer with the state should use ids throughout, or the lookup silently
// misses. Give ad-hoc activities explicit, readable ids
// (foundation.WithID("gather-logs")) and both halves read the same.
type State struct {
	// Completed counts settled executions per inner activity id. An activity
	// that has never run is absent, so len(Completed) is the number of distinct
	// activities that have run at least once.
	Completed map[string]int

	// Running counts live instances per inner activity id. Under parallel
	// ordering one activity may hold several; under sequential ordering the
	// whole map holds at most one instance.
	Running map[string]int

	// Data reads the Ad-Hoc container's scope, with the enclosing process's
	// data visible through the ordinary walk-up. It is a consistent snapshot
	// taken for this decision: values cannot shift midway through Next.
	//
	// Reading is the Router's only access to data. A decision that needs to
	// record something returns the activity that writes it, so every mutation
	// travels the ordinary commit path and appears in the change stream.
	Data service.DataReader

	// Eval evaluates an expression against the same scope Data reads, through
	// the engine's language-routed expression seam. The engine always supplies
	// it; a Router that decides without expressions ignores it.
	Eval Evaluator

	// Last is the inner activity whose completion triggered this call. It is
	// empty on the first call, when the container's scope has just opened.
	Last string

	// Activities is the container's inner activity roster — read it first when
	// deciding, whatever its position here (it trails so the struct's
	// pointer-scannable prefix stays minimal).
	//
	// It is a SET, not an order: which of them may run is the Router's answer,
	// and routing is never inferred from the order elements were added
	// (ADR-035 §2.9). The roster is how a Router names an activity before
	// anything has run — at scope open both counters above are empty.
	Activities []string
}
