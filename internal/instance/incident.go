package instance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/dr-dobermann/gobpm/internal/instance/checkpoint"
	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/observability"
	"github.com/dr-dobermann/gobpm/pkg/tasks"
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
	firstAt time.Time
	lastAt  time.Time
	// retryAt is the scheduled policy-retry deadline; zero unless the state
	// is incidentRetryScheduled (SRD-079 §3.4).
	retryAt    time.Time
	id         string
	nodeID     string
	nodeName   string
	trackID    string // the last failed attempt's track
	scopePath  scope.DataPath
	scopeSeg   string
	cause      string   // rendered chain of the failing error
	causeClass string   // errs class of the failing error, when typed
	prev       []string // lineage of the failed track
	// data is the failure-time snapshot (ADR-036 §2.1): the variables visible
	// from the incident's scope at the last raise, so the operator sees what
	// the attempt saw, immune to later sibling writes. Refreshed per raise.
	data     json.RawMessage
	attempts int
	state    incidentState
}

// IncidentView is the lock-free, read-only projection of one incident, served
// off the loop through Instance.IncidentViews (SRD-079 §3.6).
type IncidentView struct {
	FirstAt    time.Time
	LastAt     time.Time
	RetryAt    time.Time // zero if no policy retry is scheduled
	ID         string
	NodeID     string
	NodeName   string
	Cause      string
	CauseClass string
	State      string
	Data       json.RawMessage // failure-time snapshot (visible scope)
	Attempts   int
}

// IncidentViews returns the current incident projections — open and closed.
// Lock-free; safe off the loop goroutine.
func (inst *Instance) IncidentViews() []IncidentView {
	snap := inst.incidentsSnap.Load()
	if snap == nil {
		return nil
	}

	return *snap
}

// refreshIncidentsSnap rebuilds the copy-on-write incident projection after a
// mutation, ordered by first raise (then id, for a deterministic tie-break).
// Loop goroutine only — the single writer, like tracksSnap.
func (inst *Instance) refreshIncidentsSnap() {
	views := make([]IncidentView, 0, len(inst.incidents))

	for _, inc := range inst.incidents {
		views = append(views, IncidentView{
			FirstAt:    inc.firstAt,
			LastAt:     inc.lastAt,
			RetryAt:    inc.retryAt,
			ID:         inc.id,
			NodeID:     inc.nodeID,
			NodeName:   inc.nodeName,
			Cause:      inc.cause,
			CauseClass: inc.causeClass,
			State:      inc.state.String(),
			Data:       inc.data,
			Attempts:   inc.attempts,
		})
	}

	slices.SortFunc(views, func(a, b IncidentView) int {
		if c := a.FirstAt.Compare(b.FirstAt); c != 0 {
			return c
		}

		return strings.Compare(a.ID, b.ID)
	})

	inst.incidentsSnap.Store(&views)
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
func (ls *loopState) raiseIncident(ctx context.Context, t *track) {
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
	inc.data = ls.snapshotIncidentScope(ctx, t)

	t.updateState(TrackIncident)
	ls.scheduleIncidentRetry(inc, t.lastErr)
	ls.inst.refreshIncidentsSnap()
	ls.rearmIncidentRetryTimer()

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

// incidentStateFromName is String()'s inverse, for restore.
var incidentStateFromName = func() map[string]incidentState {
	m := make(map[string]incidentState, len(incidentStateNames))
	for st, n := range incidentStateNames {
		m[n] = incidentState(st)
	}

	return m
}()

// incidentRecords serializes the incident table for the checkpoint (SRD-079
// §3.3) — open AND closed, ordered by first raise (then id): a dead-lettered
// incident's record is the durable dead letter. Loop goroutine only.
func (inst *Instance) incidentRecords() []checkpoint.IncidentRecord {
	if len(inst.incidents) == 0 {
		return nil
	}

	out := make([]checkpoint.IncidentRecord, 0, len(inst.incidents))

	for _, inc := range inst.incidents {
		var retryAt *time.Time
		if !inc.retryAt.IsZero() {
			at := inc.retryAt
			retryAt = &at
		}

		out = append(out, checkpoint.IncidentRecord{
			FirstAt:    inc.firstAt,
			LastAt:     inc.lastAt,
			RetryAt:    retryAt,
			ID:         inc.id,
			NodeID:     inc.nodeID,
			NodeName:   inc.nodeName,
			TrackID:    inc.trackID,
			ScopePath:  string(inc.scopePath),
			ScopeSeg:   inc.scopeSeg,
			Cause:      inc.cause,
			CauseClass: inc.causeClass,
			State:      inc.state.String(),
			Prev:       inc.prev,
			Data:       inc.data,
			Attempts:   inc.attempts,
		})
	}

	slices.SortFunc(out, func(a, b checkpoint.IncidentRecord) int {
		if c := a.FirstAt.Compare(b.FirstAt); c != 0 {
			return c
		}

		return strings.Compare(a.ID, b.ID)
	})

	return out
}

// restoreIncidents rebuilds the incident table from a checkpoint document
// (SRD-079 FR-5). An unknown state name is a corrupt document — loud and
// per-instance, per ADR-033 §2.5.
func (inst *Instance) restoreIncidents(doc *checkpoint.Document) error {
	for i := range doc.Incidents {
		rec := &doc.Incidents[i]

		st, ok := incidentStateFromName[rec.State]
		if !ok {
			return errs.New(
				errs.M("restoreIncidents: unknown incident state %q (id %q)",
					rec.State, rec.ID),
				errs.C(errorClass, errs.InvalidState))
		}

		var retryAt time.Time
		if rec.RetryAt != nil {
			retryAt = *rec.RetryAt
		}

		inst.incidents[rec.ID] = &incident{
			firstAt:    rec.FirstAt,
			lastAt:     rec.LastAt,
			retryAt:    retryAt,
			id:         rec.ID,
			nodeID:     rec.NodeID,
			nodeName:   rec.NodeName,
			trackID:    rec.TrackID,
			scopePath:  scope.DataPath(rec.ScopePath),
			scopeSeg:   rec.ScopeSeg,
			cause:      rec.Cause,
			causeClass: rec.CauseClass,
			prev:       rec.Prev,
			data:       rec.Data,
			attempts:   rec.Attempts,
			state:      st,
		}

		if st.open() {
			inst.openIncCount.Add(1)
		}
	}

	if len(doc.Incidents) > 0 {
		inst.refreshIncidentsSnap()
	}

	return nil
}

// incidentPolicyCarrier is the capability a node or the engine runtime offers
// to drive automatic incident retries (SRD-079 §3.5). Read by assertion so
// neither the flow.Node nor the renv.EngineRuntime contract grows a method.
type incidentPolicyCarrier interface {
	IncidentRetryPolicy() tasks.RetryPolicy
}

// resolveIncidentRetryPolicy resolves the retry policy for a failing node:
// per-activity first, engine-wide second, nil — no automatic retry, the
// conservative default — last.
func (ls *loopState) resolveIncidentRetryPolicy(
	node flow.Node,
) tasks.RetryPolicy {
	if a, ok := node.(incidentPolicyCarrier); ok {
		if p := a.IncidentRetryPolicy(); p != nil {
			return p
		}
	}

	if r, ok := ls.inst.EngineRuntime.(incidentPolicyCarrier); ok {
		return r.IncidentRetryPolicy()
	}

	return nil
}

// scheduleIncidentRetry consults the policy for a just-raised incident
// (SRD-079 §3.4): a positive verdict flips it to retry-scheduled with its
// deadline and announces it; otherwise the incident stays open for the
// operator. Loop goroutine only.
func (ls *loopState) scheduleIncidentRetry(inc *incident, cause error) {
	node, ok := ls.inst.s.Nodes[inc.nodeID]
	if !ok {
		return
	}

	p := ls.resolveIncidentRetryPolicy(node)
	if p == nil {
		return
	}

	backoff, retry := p.Retry(inc.attempts, cause)
	if !retry {
		return
	}

	inc.state = incidentRetryScheduled
	inc.retryAt = ls.inst.now().Add(backoff)

	ls.inst.report(observability.Fact{
		Kind:     observability.KindFault,
		Phase:    observability.PhaseIncident,
		NodeID:   inc.nodeID,
		NodeName: inc.nodeName,
		Details: map[string]string{
			"action":      "retry-scheduled",
			"incident_id": inc.id,
			"retry_at":    inc.retryAt.Format(time.RFC3339Nano),
		},
	})
}

// rearmIncidentRetryTimer points the loop's single retry channel at the
// earliest pending deadline — one Clock.After per instance, never one per
// retry (NFR-3). A nil channel blocks its select case. Loop goroutine only.
func (ls *loopState) rearmIncidentRetryTimer() {
	var earliest time.Time

	for _, inc := range ls.inst.incidents {
		if inc.state == incidentRetryScheduled &&
			(earliest.IsZero() || inc.retryAt.Before(earliest)) {
			earliest = inc.retryAt
		}
	}

	if earliest.IsZero() {
		ls.retryC = nil

		return
	}

	d := earliest.Sub(ls.inst.now())
	if d < 0 {
		d = 0
	}

	ls.retryC = ls.inst.Clock().After(d)
}

// pendingRetries counts the scheduled automatic retries — the reason the loop
// stays resident at active == 0 instead of parking. Loop goroutine only.
func (ls *loopState) pendingRetries() int {
	n := 0

	for _, inc := range ls.inst.incidents {
		if inc.state == incidentRetryScheduled {
			n++
		}
	}

	return n
}

// applyDueIncidentRetries fires every scheduled retry whose deadline passed,
// then re-arms the timer. Loop goroutine only.
func (ls *loopState) applyDueIncidentRetries(ctx context.Context) {
	now := ls.inst.now()

	for _, inc := range ls.inst.incidents {
		if inc.state == incidentRetryScheduled && !inc.retryAt.After(now) {
			ls.retryIncident(ctx, inc)
		}
	}

	ls.inst.refreshIncidentsSnap()
	ls.rearmIncidentRetryTimer()
}

// retryIncident is the respawn (ADR-036 §2.2/§2.3, SRD-079 §3.4): a fresh
// track re-enters the failed node, bound by the incident RECORD — scope path,
// segment, lineage — so it works identically after a restore, where the failed
// track object no longer exists. The armed boundary watches transfer to the
// new attempt instead of re-arming (FR-6: failing repeatedly must not reset an
// SLA clock). Loop goroutine only.
func (ls *loopState) retryIncident(ctx context.Context, inc *incident) {
	node, ok := ls.inst.s.Nodes[inc.nodeID]
	if !ok {
		ls.inst.Logger().Error("incident retry: node not in snapshot",
			observability.AttrInstanceID, ls.inst.ID(),
			observability.AttrNodeID, inc.nodeID,
			"incident_id", inc.id)

		inc.state = incidentOpen
		inc.retryAt = time.Time{}

		return
	}

	nt, err := newTrack(node, ls.inst, nil)
	if err != nil {
		ls.inst.Logger().Error("incident retry: track build failed",
			observability.AttrInstanceID, ls.inst.ID(),
			observability.AttrNodeID, inc.nodeID,
			"incident_id", inc.id,
			observability.AttrError, err.Error())

		inc.state = incidentOpen
		inc.retryAt = time.Time{}

		return
	}

	nt.scopePath = inc.scopePath
	nt.scopeSeg = inc.scopeSeg
	nt.prev = append(append([]string{}, inc.prev...), inc.trackID)
	nt.skipInitialArm = true

	ls.transferWatches(inc.trackID, nt)

	// the retry attempt takes over the incident's single scope pin: release
	// the failed attempt's, let the new track's own spawn count stand in.
	ls.decScopePinned(ctx, inc.scopePath)

	prevTrackID := inc.trackID
	inc.trackID = nt.ID()
	inc.state = incidentOpen
	inc.retryAt = time.Time{}

	ls.inst.report(observability.Fact{
		Kind:     observability.KindFault,
		Phase:    observability.PhaseIncident,
		NodeID:   inc.nodeID,
		NodeName: inc.nodeName,
		Details: map[string]string{
			"action":      "retried",
			"incident_id": inc.id,
			"prev_track":  prevTrackID,
		},
	})

	ls.inst.trackCount.Add(1)
	ls.spawn(ctx, nt)
}

// transferWatches re-keys the armed boundary watches from the failed attempt
// to its retry (FR-6): the watches — and a timer boundary's running deadline —
// carry over, they are never rebuilt. Loop goroutine only.
func (ls *loopState) transferWatches(oldTrackID string, nt *track) {
	ws, ok := ls.watchers[oldTrackID]
	if !ok {
		return
	}

	delete(ls.watchers, oldTrackID)

	for _, w := range ws {
		w.host = nt
	}

	ls.watchers[nt.ID()] = ws
}

// closeIncidentsOnProgress closes an open incident whose current attempt just
// moved PAST the incident's node — the retry succeeded (SRD-079 §3.4). No
// scope pin releases here: the retry already took the pin over as its own
// live count, which the attempt's eventual normal end releases. Loop
// goroutine only.
func (ls *loopState) closeIncidentsOnProgress(t *track) {
	for _, inc := range ls.inst.incidents {
		if !inc.state.open() || inc.trackID != t.ID() {
			continue
		}

		inc.state = incidentResolved
		inc.retryAt = time.Time{}
		inc.lastAt = ls.inst.now()

		ls.inst.openIncCount.Add(-1)

		ls.inst.report(observability.Fact{
			Kind:     observability.KindFault,
			Phase:    observability.PhaseIncident,
			NodeID:   inc.nodeID,
			NodeName: inc.nodeName,
			Details: map[string]string{
				"action":      "resolved",
				"incident_id": inc.id,
				"resolution":  "retry",
			},
		})
	}

	ls.inst.refreshIncidentsSnap()
	ls.rearmIncidentRetryTimer()
}

// snapshotIncidentScope captures the failure-time data snapshot (ADR-036
// §2.1): value-copies of every variable visible from the failing node's scope
// (SnapshotAt — the same walk-up surface the compensation ledger freezes),
// encoded with the checkpoint's scope codec. A capture failure must not mask
// the incident itself: it logs once at Warn and the incident rises without a
// snapshot. Loop goroutine only.
func (ls *loopState) snapshotIncidentScope(
	ctx context.Context,
	t *track,
) json.RawMessage {
	dd, err := ls.inst.sc.plane.SnapshotAt(t.scopePath)
	if err == nil {
		var raw json.RawMessage
		if raw, err = checkpoint.EncodeData(
			ctx, string(t.scopePath), dd); err == nil {
			return raw
		}
	}

	ls.inst.Logger().Warn("incident data snapshot failed",
		observability.AttrInstanceID, ls.inst.ID(),
		observability.AttrScopePath, string(t.scopePath),
		observability.AttrError, err.Error())

	return nil
}
