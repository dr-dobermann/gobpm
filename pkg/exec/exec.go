// Package exec holds the public node-execution contracts (ADR-012 v.1): the
// node executor a model element implements, the synchronizing-join variant, and
// the data-binding consumer/producer + Frame surface. The implementations (the
// instance's runtime environment, the data-plane Frame) live in internal/*;
// pkg/model depends only on these interfaces.
package exec

import (
	"context"

	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/renv"
)

// NodeExecutor runs a single node and returns its valid outgoing sequence
// flows on success or an error on failure.
type NodeExecutor interface {
	Exec(
		ctx context.Context,
		re renv.RuntimeEnvironment,
	) ([]*flow.SequenceFlow, error)
}

// SynchronizingJoin is a NodeExecutor that also synchronizes multiple incoming
// flows before it executes (a converging parallel/inclusive gateway).
type SynchronizingJoin interface {
	NodeExecutor

	// Arrive records the arrival of a token on incomingFlowID from
	// arrivingTrackID, reporting whether the join is now complete and which
	// flow ids merged into this completion.
	Arrive(incomingFlowID, arrivingTrackID string) (complete bool, merged []string)
}
