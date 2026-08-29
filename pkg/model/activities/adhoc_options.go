package activities

import (
	"github.com/dr-dobermann/gobpm/pkg/adhoc"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
)

// AdHocOrdering constrains how many inner activities of an Ad-Hoc Sub-Process
// may be live at once (BPMN §13.3.5 `ordering`).
type AdHocOrdering string

const (
	// AdHocParallel lets another activity be selected at any time, including a
	// second iteration of one already running. It is gobpm's default: the
	// metamodel declares none, and parallel is the less restrictive mode
	// (ADR-035 v.1 §2.5, a registered engine choice).
	AdHocParallel AdHocOrdering = "PARALLEL"

	// AdHocSequential permits at most one live inner activity: another may be
	// selected only after the previous one terminates. A Router answering with
	// more than one successor under this ordering is a modeling error, reported
	// rather than truncated to the first.
	AdHocSequential AdHocOrdering = "SEQUENTIAL"
)

// AdHocSpec is the routing configuration of an Ad-Hoc Sub-Process, read by the
// runtime through SubProcess.AdHoc. It is an interface so the configuration
// stays immutable once the container is built: a modeler sets it with the
// WithAdHoc* options, and the engine only reads it.
type AdHocSpec interface {
	// Router answers which inner activities may run next; an empty answer ends
	// the ad-hoc work.
	Router() adhoc.Router

	// Ordering reports whether one inner activity may be live at a time or many.
	Ordering() AdHocOrdering

	// IsManual reports whether the Router's answer is offered for selection
	// (true) or run directly (false).
	IsManual() bool

	// CancelsRemaining reports what happens to activities still running when
	// routing stops: cancel them (true, the BPMN default) or wait for them.
	CancelsRemaining() bool

	// CompletionCondition is BPMN's completionCondition, or nil when the
	// container ends only on an empty Router answer.
	CompletionCondition() data.FormalExpression
}

// adHocSpec is the Ad-Hoc configuration of a SubProcess: nil on every other
// Sub-Process variant, non-nil exactly when the container is ad-hoc.
type adHocSpec struct {
	router     adhoc.Router
	completion data.FormalExpression
	ordering   AdHocOrdering
	manual     bool
	cancelRest bool
}

// Router implements AdHocSpec.
func (s *adHocSpec) Router() adhoc.Router { return s.router }

// Ordering implements AdHocSpec.
func (s *adHocSpec) Ordering() AdHocOrdering { return s.ordering }

// IsManual implements AdHocSpec.
func (s *adHocSpec) IsManual() bool { return s.manual }

// CancelsRemaining implements AdHocSpec.
func (s *adHocSpec) CancelsRemaining() bool { return s.cancelRest }

// CompletionCondition implements AdHocSpec.
func (s *adHocSpec) CompletionCondition() data.FormalExpression {
	return s.completion
}

// WithAdHoc makes the SubProcess an Ad-Hoc Sub-Process (BPMN §13.3.5,
// ADR-035 v.1) routed by r: inner activities carry no fixed order, and r
// answers which of them may run next each time the container's scope opens and
// each time an inner activity settles. An empty answer ends the container.
//
// The ordering defaults to AdHocParallel and remaining instances are canceled
// when routing stops (the metamodel's cancelRemainingInstances default);
// WithAdHocOrdering, WithAdHocManualSelection, WithAdHocCancelRemaining and
// WithAdHocCompletion adjust that. A nil Router is rejected — routing is never
// implied, and in particular never inferred from the order elements were added
// (ADR-035 v.1 §2.9).
//
// Mutually exclusive with WithTriggeredByEvent and WithTransaction.
func WithAdHoc(r adhoc.Router) SubProcessOption {
	return SubProcessOption(func(cfg *subProcessConfig) error {
		if r == nil {
			return errs.New(
				errs.M("WithAdHoc: a nil Router isn't allowed — an Ad-Hoc "+
					"Sub-Process must state how its activities are routed"),
				errs.C(errorClass, errs.InvalidParameter))
		}

		cfg.adHoc = &adHocSpec{
			router:     r,
			ordering:   AdHocParallel,
			cancelRest: true,
		}

		return nil
	})
}

// WithAdHocOrdering sets how many inner activities may run at once
// (BPMN §13.3.5 `ordering`). It applies only to an Ad-Hoc Sub-Process, so it
// must follow WithAdHoc.
func WithAdHocOrdering(o AdHocOrdering) SubProcessOption {
	return SubProcessOption(func(cfg *subProcessConfig) error {
		if o != AdHocParallel && o != AdHocSequential {
			return errs.New(
				errs.M("WithAdHocOrdering: unknown ordering %q", string(o)),
				errs.C(errorClass, errs.InvalidParameter))
		}

		if err := cfg.requireAdHoc("WithAdHocOrdering"); err != nil {
			return err
		}

		cfg.adHoc.ordering = o

		return nil
	})
}

// WithAdHocManualSelection makes selection explicit: instead of running the
// Router's answer directly, the container offers it as the enabled set and
// waits for a host to activate one of the candidates. It is how BPMN's "one
// enabled activity is selected, typically by a Human Performer" is expressed
// without the engine blocking on a person (ADR-035 v.1 §2.6).
func WithAdHocManualSelection() SubProcessOption {
	return SubProcessOption(func(cfg *subProcessConfig) error {
		if err := cfg.requireAdHoc("WithAdHocManualSelection"); err != nil {
			return err
		}

		cfg.adHoc.manual = true

		return nil
	})
}

// WithAdHocCancelRemaining sets what happens to inner activities still running
// when routing stops (BPMN §13.3.5 `cancelRemainingInstances`): true — the
// metamodel default — cancels them, false waits for them to finish.
func WithAdHocCancelRemaining(cancel bool) SubProcessOption {
	return SubProcessOption(func(cfg *subProcessConfig) error {
		if err := cfg.requireAdHoc("WithAdHocCancelRemaining"); err != nil {
			return err
		}

		cfg.adHoc.cancelRest = cancel

		return nil
	})
}

// WithAdHocCompletion attaches BPMN's `completionCondition` (§13.3.5): it is
// evaluated after each inner activity settles and, when true, ends the
// container. It composes with the Router rather than competing with it — a true
// condition is the empty answer, otherwise the Router decides (ADR-035 v.1
// §2.4).
func WithAdHocCompletion(expr data.FormalExpression) SubProcessOption {
	return SubProcessOption(func(cfg *subProcessConfig) error {
		if expr == nil {
			return errs.New(
				errs.M("WithAdHocCompletion: a nil expression isn't allowed"),
				errs.C(errorClass, errs.EmptyNotAllowed))
		}

		if err := cfg.requireAdHoc("WithAdHocCompletion"); err != nil {
			return err
		}

		cfg.adHoc.completion = expr

		return nil
	})
}

// requireAdHoc guards the options that refine an Ad-Hoc container: applied to a
// plain Sub-Process they would silently do nothing, so they name themselves in
// the error instead.
func (cfg *subProcessConfig) requireAdHoc(option string) error {
	if cfg.adHoc == nil {
		return errs.New(
			errs.M("%s: applies to an Ad-Hoc Sub-Process — pass WithAdHoc first",
				option),
			errs.C(errorClass, errs.InvalidState))
	}

	return nil
}
