package activities

import (
	"context"
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/exec"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/dr-dobermann/gobpm/pkg/renv"
)

// CallActivity invokes a separately registered process as a CHILD instance
// (ADR-023 §2.7): the reuse boundary. The caller's token parks while the
// child runs its own isolated instance; the declared Input/Output
// parameters are the call contract (§10.4 direct mapping — no data
// associations), matched by name. The callable resolves through the
// engine's registry AT CALL TIME: latest-at-launch by default, or the
// version pinned via WithCalledVersion.
type CallActivity struct {
	calledKey string

	// outcome stashes the child's completion (set by ProcessEvent on resume,
	// read by Exec — the ServiceTask worker-outcome idiom).
	outcome *exec.CallOutcome

	activity

	calledVersion int // 0 = latest-at-launch (ADR-019)
}

// callActivityConfig collects the CallActivity-specific options.
type callActivityConfig struct {
	version int
}

// CallActivityOption is a CallActivity-specific construction option.
// NewCallActivity separates these from the embedded activity's options and
// applies them to the CallActivity itself; a bad option value is rejected
// with an error.
type CallActivityOption func(*callActivityConfig) error

// Option marks CallActivityOption as an options.Option; NewCallActivity
// applies it by calling the func directly.
func (CallActivityOption) Option() {}

// WithCalledVersion pins the call to an exact registered version of the
// callable (1-based, ADR-019). Without it the call binds latest-at-launch
// — the newest version registered at the moment the call executes.
func WithCalledVersion(v int) CallActivityOption {
	return CallActivityOption(func(cfg *callActivityConfig) error {
		if v < 1 {
			return errs.New(
				errs.M("WithCalledVersion: a pinned version is 1-based, "+
					"got %d", v),
				errs.C(errorClass, errs.InvalidParameter))
		}

		cfg.version = v

		return nil
	})
}

// NewCallActivity creates a Call Activity invoking the registered process
// named by calledKey. The registry is deliberately NOT consulted here —
// resolution happens at call time (ADR-023 §2.7), so the callable may be
// registered later or re-versioned.
func NewCallActivity(
	name, calledKey string,
	opts ...options.Option,
) (*CallActivity, error) {
	calledKey = strings.TrimSpace(calledKey)
	if calledKey == "" {
		return nil, errs.New(
			errs.M("NewCallActivity: an empty called-process key isn't "+
				"allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	cfg := callActivityConfig{}
	actOpts := make([]options.Option, 0, len(opts))

	for _, o := range opts {
		switch opt := o.(type) {
		case CallActivityOption:
			if err := opt(&cfg); err != nil {
				return nil, err
			}

		default:
			actOpts = append(actOpts, o)
		}
	}

	a, err := newActivity(name, actOpts...)
	if err != nil {
		return nil, err
	}

	return &CallActivity{
		activity:      *a,
		calledKey:     calledKey,
		calledVersion: cfg.version,
	}, nil
}

// CalledKey returns the registry key of the callable process.
func (ca *CallActivity) CalledKey() string { return ca.calledKey }

// CalledVersion returns the pinned callable version, or 0 for the
// latest-at-launch binding.
func (ca *CallActivity) CalledVersion() int { return ca.calledVersion }

// ActivityType returns the CallActivity activity type.
func (ca *CallActivity) ActivityType() flow.ActivityType {
	return flow.CallActivity
}

// Node returns the CallActivity itself — the concrete-type override every
// node provides (the embedded activity base would otherwise surface,
// stripping the call capabilities from flow targets).
func (ca *CallActivity) Node() flow.Node {
	return ca
}

// Validate re-asserts the call contract at process validation (the
// per-node hook): a non-empty key and a legal pin. Registry existence is
// NOT checked — resolution is at call time.
func (ca *CallActivity) Validate() error {
	if strings.TrimSpace(ca.calledKey) == "" {
		return errs.New(
			errs.M("call activity %q has no called-process key", ca.Name()),
			errs.C(errorClass, errs.InvalidObject),
			errs.D("call_activity_id", ca.ID()))
	}

	if ca.calledVersion < 0 {
		return errs.New(
			errs.M("call activity %q has a negative version pin", ca.Name()),
			errs.C(errorClass, errs.InvalidObject),
			errs.D("call_activity_id", ca.ID()))
	}

	return nil
}

// CallInputs returns the names of the declared Input parameters — the call
// contract's inputs the loop resolves at the caller's scope and hands the child
// (SRD-050 §10.4 direct mapping, by name). Empty when the activity declares no
// IoSpec (a call that passes no data).
func (ca *CallActivity) CallInputs() []string {
	return ca.paramNames(data.Input)
}

// CallOutputs returns the names of the declared Output parameters — the call
// contract's return values the loop reads from the completed child and commits
// into the caller's scope (SRD-050 §10.4, by name).
func (ca *CallActivity) CallOutputs() []string {
	return ca.paramNames(data.Output)
}

// paramNames lists the declared parameter names of one direction, or nil when
// the activity has no IoSpec.
func (ca *CallActivity) paramNames(dir data.Direction) []string {
	if ca.IoSpec == nil {
		return nil
	}

	var pp []*data.Parameter
	if dir == data.Input {
		pp = ca.IoSpec.InputSet()
	} else {
		pp = ca.IoSpec.OutputSet()
	}

	names := make([]string, 0, len(pp))
	for _, p := range pp {
		names = append(names, p.Name())
	}

	return names
}

// ProcessEvent stashes the call-completion the instance loop delivers to the
// parked caller track when the child ends (the ServiceTask worker-outcome
// idiom): the engine loop is the only producer. Exec reads it on resume.
func (ca *CallActivity) ProcessEvent(
	_ context.Context,
	eDef flow.EventDefinition,
) error {
	co, ok := eDef.(*exec.CallOutcome)
	if !ok {
		return errs.New(
			errs.M("call activity %q expects a call-outcome event", ca.ID()),
			errs.C(errorClass, errs.InvalidParameter),
			errs.D("call_activity_id", ca.ID()))
	}

	ca.outcome = co

	return nil
}

// Exec runs after the child ended and the caller resumed. On a normal
// completion the outputs are already committed into the caller's scope by the
// loop, so the execution is the standard activity completion — select the
// outgoing flows. On a child fault the stashed outcome carries the terminal
// error: return it so the caller track faults and the §2.6 error chain catches
// it at THIS node (a typed BpmnError → an Error boundary; otherwise uncaught).
func (ca *CallActivity) Exec(
	ctx context.Context,
	re renv.RuntimeEnvironment,
) ([]*flow.SequenceFlow, error) {
	if ca.outcome != nil {
		if err := ca.outcome.Err(); err != nil {
			return nil, err
		}
	}

	return ca.selectOutgoing(ctx, re)
}

// Clone implements flow.Node: the activity base clones per the shared
// contract; the call binding (key + version pin) is immutable config,
// copied by value.
func (ca *CallActivity) Clone() (flow.Node, error) {
	a, err := ca.clone()
	if err != nil {
		return nil, errs.New(
			errs.M("couldn't clone call activity %q", ca.ID()),
			errs.C(errorClass, errs.BulidingFailed),
			errs.E(err))
	}

	return &CallActivity{
		activity:      a,
		calledKey:     ca.calledKey,
		calledVersion: ca.calledVersion,
	}, nil
}

// interface checks
var (
	_ flow.ActivityNode = (*CallActivity)(nil)
)
