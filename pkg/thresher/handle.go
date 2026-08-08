package thresher

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dr-dobermann/gobpm/internal/instance"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/observability"
)

// ErrNotImplemented marks a control operation that is part of the stable handle
// contract but not yet implemented — Suspend/Resume await the Paused subsystem
// (ADR-013 §2.3, SRD-019).
var ErrNotImplemented = errs.New(
	errs.M("operation reserved, not yet implemented"),
	errs.C(errorClass, errs.OperationFailed))

// InstanceHandle is a read-only window onto one running process instance
// (SRD-018, ADR-013 §2.1). It is returned by StartProcess and found by
// Thresher.Instance. It wraps the engine's internal instance by reference but
// exposes only observation — never the instance object itself nor any mutating
// method, so a host cannot corrupt a running instance.
type InstanceHandle struct {
	// inst is the instance object the handle currently speaks for. It is
	// SWAPPED when the engine rebuilds a dehydrated instance (SRD-071): an
	// instance's identity outlives its object, so a handle that captured the
	// first object would answer "Dehydrated" forever while the real instance
	// ran on and finished.
	inst atomic.Pointer[instance.Instance]
	// settled is the terminal signal, owned per instance ID and shared with
	// every rebuild — closed only when the instance genuinely finishes, never
	// when it merely releases its goroutines.
	settled chan struct{}
	// th is the owning engine — the incident operations route through it so
	// an op on a PARKED instance (its loop exited on an incident park or a
	// dehydration) can rebuild it from its checkpoint first (SRD-079 §3.6).
	th *Thresher
	// parentID/callNodeID cache the call linkage — immutable identity,
	// captured at adopt so a handle answers correctly even before or
	// after the instance object itself is reachable (SRD-082 FR-7,
	// independent-review note A3).
	// observers are the handle's own observer registrations. They live HERE,
	// not on the instance object, because the object is replaced on every
	// rebuild: registering on h.current() alone meant a host observer stopped
	// receiving facts after the first dehydration, silently, while its
	// Subscription still reported itself live (FIX-038 §1.8). The handle owns
	// identity across rebuilds (SRD-071), so it owns these too.
	observers  map[uint64]*handleObserver
	parentID   string
	callNodeID string
	nextObs    uint64
	obsMu      sync.Mutex
}

// cancelParkSeam runs inside Cancel between the state check and the direct
// cancel — the window an instance can park in, and the reason the state is
// re-read afterwards. It is nil in production and exists because that window
// cannot otherwise be aimed at: a test that merely races the two hits it by
// luck, which is indistinguishable from a test that cannot fail.
var cancelParkSeam func()

// cancelRouteAttempts bounds Cancel's re-read of the instance's state. Each
// pass either cancels a live instance or routes a parked one; the bound only
// matters if the instance keeps parking between the two, which other traffic
// can cause but cannot sustain.
const cancelRouteAttempts = 3

// handleObserver is one registration the handle re-attaches after a rebuild:
// the fan-out closure is stable, the deregistration is not — it belongs to
// whichever instance object the closure is currently registered on.
type handleObserver struct {
	fanout func(observability.Fact)
	cancel func()
	// on is the instance object this registration currently sits on. Without
	// it a re-attach cannot tell "not yet moved" from "already moved": an
	// Observe landing between adopt and reattachObservers registers on the NEW
	// object, and re-registering it there delivered every fact twice while
	// overwriting the cancel of the first registration, which could then never
	// be removed.
	on *instance.Instance
}

// reattachObservers re-registers the handle's observers on the instance it now
// speaks for. Call it AFTER the engine lock is released: AddObserver takes the
// instance's own observer lock, and the engine does not hold t.m across another
// component's lock (the rule locked.go states).
//
// It is idempotent per instance object: a registration already sitting on this
// one is left alone, and one sitting on the previous object is canceled there
// before it moves.
func (h *InstanceHandle) reattachObservers() {
	inst := h.current()
	if inst == nil {
		return
	}

	h.obsMu.Lock()
	defer h.obsMu.Unlock()

	for _, ho := range h.observers {
		if ho.on == inst {
			continue
		}

		if ho.cancel != nil {
			ho.cancel()
		}

		ho.cancel = inst.AddObserver(ho.fanout)
		ho.on = inst
	}
}

// current returns the instance object the handle speaks for right now.
func (h *InstanceHandle) current() *instance.Instance {
	return h.inst.Load()
}

// adopt points the handle at a rebuilt instance (the wake path), so callers
// holding it follow the instance across a dehydration cycle.
func (h *InstanceHandle) adopt(inst *instance.Instance) {
	h.inst.Store(inst)
	h.parentID = inst.ParentID()
	h.callNodeID = inst.CallNodeID()
}

// ID returns the instance id.
func (h *InstanceHandle) ID() string {
	return h.current().ID()
}

// State returns the instance's current lifecycle state from the standard-named,
// open vocabulary (ADR-013 §2.4); read lock-free. Treat an unknown value
// gracefully — the set grows additively as deferred states land.
func (h *InstanceHandle) State() InstanceState {
	return InstanceState(h.current().State().String())
}

// Data returns a read-only reader over the instance's process properties and
// runtime variables. Read-only by interface (service.DataReader has no mutator).
func (h *InstanceHandle) Data() service.DataReader {
	return h.current().DataReader()
}

// Tokens returns a snapshot of where execution currently is — one TokenView per
// active track (Alive or WaitForEvent). Lock-free (copy-on-write snapshot).
func (h *InstanceHandle) Tokens() []TokenView {
	src := h.current().GetTokens()
	out := make([]TokenView, 0, len(src))

	for _, t := range src {
		out = append(out, TokenView{
			NodeID:   t.Node.ID(),
			NodeName: t.Node.Name(),
			State:    tokenState(t.State),
		})
	}

	return out
}

// History returns every track's recorded path — active and finished, the
// finished ones (ended, merged, canceled) projecting to a Consumed terminal —
// with fork lineage (ParentID) and per-step visit timings. This is the
// "including merged tokens" view; Tokens() stays the live-active snapshot.
// Lock-free (copy-on-write).
func (h *InstanceHandle) History() []TokenPath {
	src := h.current().TokenHistory()
	out := make([]TokenPath, 0, len(src))

	for _, p := range src {
		steps := make([]StepVisit, 0, len(p.Steps))
		for _, s := range p.Steps {
			steps = append(steps, StepVisit{
				NodeID:   s.Node.ID(),
				NodeName: s.Node.Name(),
				State:    tokenState(s.State),
				At:       s.At,
			})
		}

		out = append(out, TokenPath{
			TrackID:    p.TrackID,
			ParentID:   p.ParentID,
			MergedInto: p.MergedInto,
			Steps:      steps,
			Terminal:   tokenState(p.Terminal),
		})
	}

	return out
}

// OpenIncidents reports the number of open incidents on the instance — the
// failures waiting for a retry or an operator (SRD-079). Lock-free. The full
// incident view and the resolution operations arrive with the visibility and
// resolution slices; this count is the minimal "does it need me?" probe.
func (h *InstanceHandle) OpenIncidents() int {
	return h.current().OpenIncidents()
}

// IncidentView is one incident's read-only projection (SRD-079 §3.6): a
// failure the model did not handle, waiting for a retry or an operator —
// or closed (resolved / dead-lettered / overtaken), retained as the record.
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
	// Data is the failure-time snapshot: the variables visible from the
	// failing node's scope at the last raise — what the attempt saw, immune
	// to later sibling writes (ADR-036 §2.1).
	Data     json.RawMessage
	Attempts int
}

// RetryIncident re-enters the incident's failed node now, regardless of the
// retry policy's remaining budget (ADR-036 §2.6, SRD-079 §3.6).
func (h *InstanceHandle) RetryIncident(
	ctx context.Context, incidentID string,
) error {
	return h.submitIncidentOp(ctx, instance.IncidentRetry, incidentID)
}

// ResolveIncident closes the incident as handled outside the engine: the
// continuation proceeds from the node's outgoing flows with the scope's
// current data, without re-executing the node — the operator asserts the
// work's effect exists (ADR-036 §2.6).
func (h *InstanceHandle) ResolveIncident(
	ctx context.Context, incidentID string,
) error {
	return h.submitIncidentOp(ctx, instance.IncidentResolve, incidentID)
}

// DropIncident closes the incident as dead-lettered: the record is retained
// durably as the dead letter, and the instance never completes normally past
// it — it waits for the operator's next act, a Cancel or a compensation
// (ADR-036 §2.5/§2.6).
func (h *InstanceHandle) DropIncident(
	ctx context.Context, incidentID string,
) error {
	return h.submitIncidentOp(ctx, instance.IncidentDrop, incidentID)
}

// submitIncidentOp delivers an operator incident operation. A parked instance
// (its loop exited on an incident park or a dehydration) is first rebuilt from
// its checkpoint through the engine — the op is never lost to that race.
func (h *InstanceHandle) submitIncidentOp(
	ctx context.Context, op instance.IncidentOp, incidentID string,
) error {
	delivered, err := h.current().SubmitIncidentOp(ctx, op, incidentID)
	if err != nil || delivered {
		return err
	}

	if h.th == nil {
		return errs.New(
			errs.M("incident op on a parked instance needs its engine"),
			errs.C(errorClass, errs.InvalidState))
	}

	return h.th.wakeForIncidentOp(ctx, h, op, incidentID)
}

// Incidents returns the instance's incidents, open and closed, ordered by
// first raise. Lock-free (copy-on-write snapshot).
func (h *InstanceHandle) Incidents() []IncidentView {
	src := h.current().IncidentViews()
	out := make([]IncidentView, 0, len(src))

	for i := range src {
		v := &src[i]
		out = append(out, IncidentView{
			FirstAt:    v.FirstAt,
			LastAt:     v.LastAt,
			RetryAt:    v.RetryAt,
			ID:         v.ID,
			NodeID:     v.NodeID,
			NodeName:   v.NodeName,
			Cause:      v.Cause,
			CauseClass: v.CauseClass,
			State:      v.State,
			Data:       v.Data,
			Attempts:   v.Attempts,
		})
	}

	return out
}

// WaitCompletion blocks until the instance reaches a terminal state (Completed
// or Terminated) or ctx is done, returning the state observed and the fatal
// error that stopped the instance (or ctx.Err() on timeout/cancel). It is
// backed by a terminal signal close — a guaranteed, never-dropped signal
// (ADR-013 §2.2), unlike the lossy observation stream.
//
// A DEHYDRATED instance does not unblock it: releasing the goroutines of an
// instance waiting on a two-day timer finishes nothing, so the wait continues
// across as many dehydration/hydration cycles as the instance goes through
// (SRD-071).
func (h *InstanceHandle) WaitCompletion(
	ctx context.Context,
) (InstanceState, error) {
	select {
	case <-h.settled:
		return h.State(), h.current().LastErr()

	case <-ctx.Done():
		return h.State(), ctx.Err()
	}
}

// Cancel requests termination of the instance and blocks until it reaches a
// terminal state (Completed/Terminated) or ctx is done, returning the observed
// state (+ ctx.Err() on timeout). Coarse, engine-mediated control (ADR-013 §2.3):
// it drives the instance's ctx-cancel cascade, never a back door. Idempotent — a
// second call, or Cancel of an already-terminal instance, returns the terminal
// state at once.
func (h *InstanceHandle) Cancel(ctx context.Context) (InstanceState, error) {
	// A DEHYDRATED instance has no loop to observe a context cancellation, so
	// canceling here canceled a context nobody was reading: the request was
	// lost and the next wake resumed the instance as if it had never been made
	// (FIX-038 §1.10). It rides a rebuild instead, like an incident operation.
	//
	// The check and the cancel are NOT atomic, and the gap is the same defect
	// again: an instance that parks between them takes the direct cancel to a
	// loop that has already exited. So the state is re-read after canceling
	// and the parked case routed — bounded, because an instance woken and
	// re-parked by other traffic must not spin here.
	// A handle always speaks for an instance — every constructor adopts one —
	// so current() is not nil-guarded here, exactly as State() and Data() are
	// not: a guard would only defer the same nil to WaitCompletion below.
	for range cancelRouteAttempts {
		inst := h.current()

		if inst.State() == instance.Dehydrated && h.th != nil {
			if err := h.th.cancelParked(ctx, h); err != nil {
				return h.State(), err
			}

			return h.WaitCompletion(ctx)
		}

		if cancelParkSeam != nil {
			cancelParkSeam()
		}

		inst.Cancel()

		// It parked while that cancel was in flight, so nothing observed it.
		if h.current().State() != instance.Dehydrated || h.th == nil {
			break
		}
	}

	return h.WaitCompletion(ctx)
}

// Suspend is reserved (ADR-013 §2.3): pausing token movement needs the deferred
// Paused subsystem. The method exists so the control contract is stable; it
// returns ErrNotImplemented until that subsystem lands.
func (h *InstanceHandle) Suspend(_ context.Context) error {
	return ErrNotImplemented
}

// Resume is reserved (ADR-013 §2.3) — the counterpart of Suspend; returns
// ErrNotImplemented until the Paused subsystem lands.
func (h *InstanceHandle) Resume(_ context.Context) error {
	return ErrNotImplemented
}

// InstanceState is the standard-named, OPEN instance lifecycle vocabulary
// (ADR-013 §2.4). Consumers must tolerate unknown values: the set grows
// additively (Failing/Paused/Compensating join as their subsystems land) with
// no breaking change.
type InstanceState string

// The instance lifecycle states the runtime exercises today (ADR-001 §4.2).
const (
	StateCreated     InstanceState = "Created"
	StateActive      InstanceState = "Active"
	StateCompleted   InstanceState = "Completed"
	StateTerminating InstanceState = "Terminating"
	StateTerminated  InstanceState = "Terminated"
	// StateDehydrated: the instance is waiting with NO goroutines — it released
	// them while every track was parked on a held, dehydratable wait, and its
	// checkpoint is the wake source (SRD-071). It is IN-FLIGHT, not terminal:
	// a trigger rebuilds it and it returns to Active, so treat it exactly as
	// you would a running instance that happens to be idle.
	StateDehydrated InstanceState = "Dehydrated"
)

// TokenState is the standard-named, OPEN projected token-position vocabulary.
// The engine collapses ended/merged/canceled/failed tracks to Consumed. (The
// Event-Based gateway routes without minting arm tokens, so there is no Withdrawn
// state — ADR-005 v.4 §2.12.1.)
type TokenState string

// The projected token states (token.go tokenStateFor).
const (
	TokenAlive        TokenState = "Alive"
	TokenWaitForEvent TokenState = "WaitForEvent"
	TokenConsumed     TokenState = "Consumed"
	// TokenIncident is a token preserved at a node whose failure opened an
	// incident (SRD-079 FR-4): visible, not consumed, until the incident
	// closes.
	TokenIncident TokenState = "Incident"
	TokenInvalid  TokenState = "Invalid"
)

// TokenView is a live token position: the node a token currently sits on and
// its state.
type TokenView struct {
	NodeID   string
	NodeName string
	State    TokenState
}

// TokenPath is one track's recorded path — including finished (Consumed)
// tracks — with its fork lineage and per-step timings.
type TokenPath struct {
	TrackID  string
	ParentID string
	// MergedInto is the survivor track this one was absorbed into at a
	// synchronizing join ("" if not merged) — a forward, acyclic merge edge.
	MergedInto string
	Terminal   TokenState
	Steps      []StepVisit
}

// StepVisit is one entry of a token's path: the node visited, the projected
// state there, and when.
type StepVisit struct {
	At       time.Time
	NodeID   string
	NodeName string
	State    TokenState
}

// tokenState maps the engine's internal projected token state onto the public
// vocabulary.
func tokenState(ts instance.TokenState) TokenState {
	switch ts {
	case instance.TokenAlive:
		return TokenAlive

	case instance.TokenWaitForEvent:
		return TokenWaitForEvent

	case instance.TokenConsumed:
		return TokenConsumed

	case instance.TokenIncident:
		return TokenIncident

	default:
		return TokenInvalid
	}
}

// ParentID returns the caller instance's id when this instance is a
// Call Activity child, "" for a root instance (SRD-082 FR-7 — the
// discovery separation: a host lists roots and reaches children
// through their parent).
func (h *InstanceHandle) ParentID() string { return h.parentID }

// CallNodeID returns the caller's Call Activity node id for a child,
// "" for a root instance.
func (h *InstanceHandle) CallNodeID() string { return h.callNodeID }
