package instance

import (
	"errors"
	"fmt"
	"time"

	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/observability"
)

// incidentState enumerates an incident's lifecycle (ADR-036 §2.1): it opens on
// a raise, may hold a scheduled retry, and closes exactly once — resolved by an
// operator, dead-lettered on give-up, or overtaken by an interrupting boundary
// that canceled its node.
type incidentState uint8

const (
	// incidentOpen is a raised incident waiting for a retry or an operator.
	incidentOpen incidentState = iota
	// incidentRetryScheduled is an open incident with a policy retry deadline.
	incidentRetryScheduled
	// incidentResolved is closed: the operator asserted the work's effect
	// exists and execution continued past the node.
	incidentResolved
	// incidentDeadLettered is closed on give-up; the record is the durable
	// dead letter (ADR-036 §2.5).
	incidentDeadLettered
	// incidentOvertaken is closed by an interrupting boundary firing on the
	// incident's node — the model canceled the host and made the operator's
	// decision (ADR-036 §2.4).
	incidentOvertaken
)

// incidentStateNames is the state→name table, keyed by the constant so it stays
// correct if the iota block is reordered.
var incidentStateNames = [...]string{
	incidentOpen:           "open",
	incidentRetryScheduled: "retry-scheduled",
	incidentResolved:       "resolved",
	incidentDeadLettered:   "dead-lettered",
	incidentOvertaken:      "overtaken",
}

// String returns the lower-case incident state name.
func (s incidentState) String() string {
	if int(s) >= len(incidentStateNames) {
		return fmt.Sprintf("incidentState(%d)", s)
	}

	return incidentStateNames[s]
}

// open reports whether the incident still needs a continuation — a retry, a
// resolution, or an operator's give-up.
func (s incidentState) open() bool {
	return s == incidentOpen || s == incidentRetryScheduled
}

// incident is the durable record of a failure the model did not handle
// (ADR-036 §2.1, SRD-079 §3.1). The failing track ends (TrackIncident); the
// incident carries everything a future attempt needs: the node, the scope
// binding, the lineage, the cause and the attempt history. Mutated only on the
// loop goroutine.
type incident struct {
	firstAt    time.Time
	lastAt     time.Time
	id         string
	nodeID     string
	nodeName   string
	trackID    string // the last failed attempt's track
	scopePath  scope.DataPath
	scopeSeg   string
	cause      string   // rendered chain of the failing error
	causeClass string   // errs class of the failing error, when typed
	prev       []string // lineage of the failed track
	attempts   int
	state      incidentState
}

// OpenIncidents reports the number of incidents still needing a continuation —
// a retry, a resolution, or an operator's give-up. Lock-free; safe off the
// loop goroutine.
func (inst *Instance) OpenIncidents() int {
	return int(inst.openIncCount.Load())
}

// openIncidents counts incidents still needing a continuation. Loop goroutine
// only.
func (inst *Instance) openIncidents() int {
	n := 0

	for _, inc := range inst.incidents {
		if inc.state.open() {
			n++
		}
	}

	return n
}

// openIncidentAt finds the open incident for a node+scope, if any — a repeated
// failure of the same failure point re-arms its incident rather than opening a
// second one (SRD-079 §3.1). Loop goroutine only.
func (inst *Instance) openIncidentAt(
	nodeID string,
	path scope.DataPath,
	seg string,
) *incident {
	for _, inc := range inst.incidents {
		if inc.state.open() && inc.nodeID == nodeID &&
			inc.scopePath == path && inc.scopeSeg == seg {
			return inc
		}
	}

	return nil
}

// causeClassOf extracts the errs class of a failure for the incident record and
// its fact; an untyped error has none.
func causeClassOf(err error) string {
	var ae *errs.ApplicationError
	if errors.As(err, &ae) && len(ae.Classes) > 0 {
		return ae.Classes[len(ae.Classes)-1]
	}

	return ""
}

// isInvariantFailure reports whether the failure denies the engine's own state
// (errs.BrokenInvariant). Such a failure keeps the fatal path: an incident
// asserts "the world outside misbehaved, the engine's state is sound", and
// retrying against corrupt state compounds it (ADR-036 §2.1).
func isInvariantFailure(err error) bool {
	// walk the whole wrap chain: a task layer wraps the original failure in
	// its own ApplicationError, and errors.As would stop at that outer one.
	for e := err; e != nil; e = errors.Unwrap(e) {
		var ae *errs.ApplicationError
		if errors.As(e, &ae) && ae.HasClass(errs.BrokenInvariant) {
			return true
		}
	}

	return false
}

// isModeledErrorEnd reports whether the failure is an Error End Event's own
// throw — the model's explicit verdict, not a defect. It keeps the fatal path
// (ADR-036 §2.1): the extract's end-events clause says an Error End Event fails
// the instance, and turning the author's modeled outcome into an operator
// ticket would invert its intent.
func isModeledErrorEnd(t *track) bool {
	if _, isEnd := t.currentStep().node.(*events.EndEvent); !isEnd {
		return false
	}

	var be *events.BpmnError

	return errors.As(t.lastErr, &be)
}

// raiseIncident turns a failed track into an incident (ADR-036 §2.2, SRD-079
// §3.2): the track ends in TrackIncident, and the instance-level record opens —
// or, for a repeated failure of the same node+scope, re-arms with the new cause.
// The raise is the failure's single handling point (ADR-022 §2.1), so the one
// Error log record lives here. Called only from the loop goroutine.
func (ls *loopState) raiseIncident(t *track) {
	step := t.currentStep()
	node := step.node
	now := ls.inst.now()
	cause := fmt.Sprintf("%v", t.lastErr)

	inc := ls.inst.openIncidentAt(node.ID(), t.scopePath, t.scopeSeg)
	if inc == nil {
		ls.inst.openIncCount.Add(1)
		inc = &incident{
			id:        foundation.GenerateID(),
			nodeID:    node.ID(),
			nodeName:  node.Name(),
			scopePath: t.scopePath,
			scopeSeg:  t.scopeSeg,
			firstAt:   now,
			state:     incidentOpen,
		}
		ls.inst.incidents[inc.id] = inc
	}

	inc.trackID = t.ID()
	inc.prev = append(inc.prev[:0], t.prev...)
	inc.cause = cause
	inc.causeClass = causeClassOf(t.lastErr)
	inc.attempts++
	inc.lastAt = now
	inc.state = incidentOpen

	t.updateState(TrackIncident)

	ls.inst.Logger().Error("incident raised",
		observability.AttrInstanceID, ls.inst.ID(),
		observability.AttrNodeID, inc.nodeID,
		observability.AttrNodeName, inc.nodeName,
		"incident_id", inc.id,
		observability.AttrAttempts, inc.attempts,
		observability.AttrError, inc.cause)

	details := map[string]string{
		"action":                "raised",
		"incident_id":           inc.id,
		observability.AttrError: inc.cause,
	}
	if inc.causeClass != "" {
		details["cause_class"] = inc.causeClass
	}

	ls.inst.report(observability.Fact{
		Kind:     observability.KindFault,
		Phase:    observability.PhaseIncident,
		NodeID:   inc.nodeID,
		NodeName: inc.nodeName,
		Details:  details,
	})
}
