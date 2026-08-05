package thresher

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"time"

	"github.com/dr-dobermann/gobpm/internal/instance"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
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
}

// current returns the instance object the handle speaks for right now.
func (h *InstanceHandle) current() *instance.Instance {
	return h.inst.Load()
}

// adopt points the handle at a rebuilt instance (the wake path), so callers
// holding it follow the instance across a dehydration cycle.
func (h *InstanceHandle) adopt(inst *instance.Instance) {
	h.inst.Store(inst)
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
	h.current().Cancel()

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
