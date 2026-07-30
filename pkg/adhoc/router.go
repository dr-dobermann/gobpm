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

// State is what a routing decision may rest on: how the container has
// progressed so far, and the data visible to it.
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

	// Last is the inner activity whose completion triggered this call. It is
	// empty on the first call, when the container's scope has just opened.
	Last string
}
