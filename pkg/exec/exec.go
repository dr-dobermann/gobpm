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

// FlowChecker answers reachability for a synchronizing join. It is implemented
// by the instance (which owns the static node graph and the live track
// positions) and is consulted only from the instance loop, so the live-token
// set it reads is consistent (ADR-005 v.2 §2.10, SRD-022 §6).
type FlowChecker interface {
	// CheckFlows returns the subset of flows still reachable for node — those
	// with a live token somewhere on a backward path from the flow's source to
	// the start. Reachability is structural (condition-ignoring) and
	// cycle-guarded.
	CheckFlows(node flow.Node, flows []*flow.SequenceFlow) ([]*flow.SequenceFlow, error)
}

// ReachabilityJoin is a SynchronizingJoin whose completion is non-local: it
// fires only when no live token can still reach an un-marked incoming flow. The
// owning loop supplies reachability through a FlowChecker and re-checks the join
// when a token parks at it and on every token death (ADR-005 v.2 §2.10).
type ReachabilityJoin interface {
	SynchronizingJoin

	// Recheck re-prunes the join's now-unreachable incoming flows via fc and
	// reports completion without a new arrival. On completion it returns the
	// promoted survivor track id and the absorbed (merged) track ids.
	Recheck(fc FlowChecker) (complete bool, survivor string, merged []string)
}
