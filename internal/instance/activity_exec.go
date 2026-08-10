package instance

import (
	"context"

	"github.com/dr-dobermann/gobpm/pkg/model/flow"
)

// awaitKind classifies what an activity instance is currently waiting on.
// The distinction is load-bearing twice over (ADR-025 v.3 §2.13): only an
// awaitEvent instance holds a subscription the hub must know about, and only
// an awaitEvent instance contributes to the instance's residency. An
// instance awaiting a child scope's drain or a child instance is NOT doing
// work — treating it as if it were would pin its process instance resident
// for as long as the child waits, which for an iterated Sub-Process holding
// parked User Tasks is the week-long approval case ADR-007 v.2.1 exists for.
type awaitKind uint8

const (
	// awaitNothing — executing, or finished.
	awaitNothing awaitKind = iota
	// awaitEvent — parked on an event subscription.
	awaitEvent
	// The two remaining kinds — awaiting a child scope's drain, and
	// awaiting a child instance — arrive with the sub-process and call
	// executors that can hold them (SRD-088.A M3). A leaf instance can
	// hold neither: it opens no scope and owns no child.
)

// instanceState reports one activity instance in the iteration vocabulary
// (ADR-025 v.3 §2.9.1): the ordinal identifies it, and the rest says what it
// is doing. The same shape is what the durable record persists, what the
// token view projects and what an incident carries — one vocabulary, so the
// three surfaces cannot describe an instance differently (SRD-088.C).
type instanceState struct {
	// ordinal is 0 for a non-iterated activity, which has exactly one
	// instance, and the 0-based instance number otherwise. It is the join
	// key across the record, the projection and an incident.
	ordinal int
	await   awaitKind
	done    bool
}

// activityExec executes ONE instance of an activity and owns whatever that
// instance awaits (ADR-025 v.3 §2.13).
//
// It is deliberately NOT exec.NodeExecutor (pkg/exec), which is the
// interface a NODE implements to execute itself: that answers "how does this
// node do its work", while this answers "what is running one instance of
// this activity, and what is it waiting for". A track drives one of these; a
// decorator holds N and implements the same interface, which closes the
// composition and is why a track cannot tell how many instances are behind
// the activity it is executing.
type activityExec interface {
	// run executes this instance and returns the flows to follow, or none
	// when the instance parks or belongs to a set whose decorator follows
	// the activity's flows once on its behalf.
	run(ctx context.Context) ([]*flow.SequenceFlow, error)

	// awaits reports what this instance is waiting on right now.
	awaits() awaitKind

	// state reports the instance in the iteration vocabulary.
	state() instanceState
}

// nodeExec is the leaf realization: it runs an activity that is not a scope
// host, in its own execution frame.
//
// In this slice a leaf activity has exactly one instance per track, so the
// frame identity the track already supplies is unambiguous. When a decorator
// holds several instances of one node (SRD-088.A M2), the ordinal joins that
// identity — which is why it is carried here from the start rather than
// introduced later at every call site.
type nodeExec struct {
	t    *track
	step *stepInfo
	ord  int
}

// execFor builds the executor that runs this node. Today every node it is
// asked about has exactly one instance; once the iteration branches move
// onto decorators (SRD-088.A M2) this is where a node carrying loop
// characteristics gets a decorator instead, and the caller does not change
// because both satisfy activityExec.
func execFor(t *track, step *stepInfo) activityExec {
	return newNodeExec(t, step, 0)
}

// newNodeExec builds the executor for one instance of a leaf activity.
func newNodeExec(t *track, step *stepInfo, ordinal int) *nodeExec {
	return &nodeExec{t: t, step: step, ord: ordinal}
}

// run executes the node once.
func (e *nodeExec) run(ctx context.Context) ([]*flow.SequenceFlow, error) {
	return e.t.executeNode(ctx, e.step)
}

// awaits reports an event wait, which is the only kind a leaf can hold: a
// leaf opens no scope and owns no child instance.
//
// It reads the driving track's state because in this slice the two are one
// to one. A decorator's instances report their own (M2), and the reading
// here does not change: a parked leaf awaits an event.
func (e *nodeExec) awaits() awaitKind {
	if e.t.inState(TrackWaitForEvent) {
		return awaitEvent
	}

	return awaitNothing
}

// state reports this instance in the iteration vocabulary.
func (e *nodeExec) state() instanceState {
	a := e.awaits()

	return instanceState{
		ordinal: e.ord,
		await:   a,
		done:    a == awaitNothing && e.step.state == StepEnded,
	}
}
