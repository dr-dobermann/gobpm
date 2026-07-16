package activities

import (
	"context"
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/errs"
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

// ProcessEvent accepts the call-completion delivery that resumes the
// parked caller track (the composite precedent): the engine loop is the
// only producer, so the delivery itself is the signal.
func (ca *CallActivity) ProcessEvent(
	_ context.Context,
	eDef flow.EventDefinition,
) error {
	if eDef == nil {
		return errs.New(
			errs.M("a nil event definition isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed),
			errs.D("call_activity_id", ca.ID()))
	}

	return nil
}

// Exec runs after the child completed and the caller resumed: the outputs
// are already bound into the caller's scope by the loop, so the execution
// is the standard activity completion — select the outgoing flows.
func (ca *CallActivity) Exec(
	ctx context.Context,
	re renv.RuntimeEnvironment,
) ([]*flow.SequenceFlow, error) {
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
