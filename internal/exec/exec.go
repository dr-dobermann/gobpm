package exec

import (
	"context"

	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/renv"
)

// NodeExecutor is an interface for executing single flowNode.
type NodeExecutor interface {
	// Exec runs single node and returns its valid
	// output sequence flows on success or error on failure.
	Exec(
		ctx context.Context,
		re renv.RuntimeEnvironment,
	) ([]*flow.SequenceFlow, error)
}

// SynchronizingJoin is implemented by gateway nodes whose converging side
// synchronizes its incoming flows (the Parallel AND-join; later Inclusive).
// The node owns its per-instance arrival state and serializes concurrent
// arrivals itself (ADR-005 §2.4). A node that does not implement this interface
// is a non-synchronizing merge: each arriving token passes straight through.
type SynchronizingJoin interface {
	NodeExecutor

	// Arrive records that the track arrivingTrackID reached the join on
	// incomingFlowID and reports whether the join is now complete. It is
	// atomic — safe for concurrent track callers.
	//
	// A non-completing arrival is recorded and returns (false, nil). The
	// completing arrival returns (true, merged) where merged is the ids of the
	// tracks absorbed into the join (every prior arrival — the completing
	// arrival itself is the survivor and is not listed), and the node resets its
	// arrival state for reuse. Ids keep the contract in the model layer: the
	// node never references the runtime track type.
	Arrive(incomingFlowID, arrivingTrackID string) (complete bool, merged []string)
}
