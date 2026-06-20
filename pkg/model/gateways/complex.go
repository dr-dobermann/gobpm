package gateways

import (
	"context"
	"errors"
	"slices"
	"sync"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/exec"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/dr-dobermann/gobpm/pkg/renv"
)

// Triple is one disjunct of a ComplexGateway's activation rule (ADR-005 v.3 §2.11):
// the join fires when cond holds (nil = always true), count incoming flows have
// arrived, and every required incoming flow is among the arrived. The bare threshold
// "N of M" is a triple with no guard and no required flows.
type Triple struct {
	cond     data.FormalExpression
	required []string
	count    int
}

// TripleOption configures a Triple built by NewTriple.
type TripleOption func(*Triple) error

// NewTriple builds one activation disjunct requiring count arrivals, refined by
// WithGuard / WithRequired. It rejects count < 1 and count < len(requiredFlows) — a
// triple that demands more specific gates than its budget can never fire.
func NewTriple(count int, opts ...TripleOption) (Triple, error) {
	t := Triple{count: count}

	for _, o := range opts {
		if err := o(&t); err != nil {
			return Triple{},
				errs.New(
					errs.M("NewTriple: option failed"),
					errs.C(errorClass, errs.BulidingFailed),
					errs.E(err))
		}
	}

	if t.count < 1 {
		return Triple{},
			errs.New(
				errs.M("NewTriple: count must be >= 1"),
				errs.C(errorClass, errs.InvalidParameter),
				errs.D("count", t.count))
	}

	if t.count < len(t.required) {
		return Triple{},
			errs.New(
				errs.M("NewTriple: count must be >= the number of required flows"),
				errs.C(errorClass, errs.InvalidParameter),
				errs.D("count", t.count),
				errs.D("required", len(t.required)))
	}

	return t, nil
}

// WithGuard adds a process-data guard to a Triple; a nil condition is rejected (an
// unconditional triple simply omits the guard).
func WithGuard(cond data.FormalExpression) TripleOption {
	return func(t *Triple) error {
		if cond == nil {
			return errs.New(
				errs.M("WithGuard: a nil condition isn't allowed"),
				errs.C(errorClass, errs.InvalidParameter))
		}

		t.cond = cond

		return nil
	}
}

// WithRequired pins the incoming flows that must be among the arrived for the Triple
// to fire (gate-identity activation). Empty / blank ids are rejected.
func WithRequired(incomingFlowIDs ...string) TripleOption {
	return func(t *Triple) error {
		if len(incomingFlowIDs) == 0 {
			return errs.New(
				errs.M("WithRequired: at least one incoming flow id expected"),
				errs.C(errorClass, errs.InvalidParameter))
		}

		if slices.Contains(incomingFlowIDs, "") {
			return errs.New(
				errs.M("WithRequired: an empty flow id isn't allowed"),
				errs.C(errorClass, errs.InvalidParameter))
		}

		t.required = append(t.required, incomingFlowIDs...)

		return nil
	}
}

// complexConfig collects the Complex-specific activation rule during construction.
type complexConfig struct {
	activation []Triple
	set        bool
}

// Validate requires that an activation rule was supplied.
func (cc *complexConfig) Validate() error {
	if !cc.set || len(cc.activation) == 0 {
		return errs.New(
			errs.M("complex gateway requires an activation rule "+
				"(WithActivationThreshold or WithActivation)"),
			errs.C(errorClass, errs.InvalidParameter))
	}

	return nil
}

// ComplexOption configures a ComplexGateway's activation rule. It satisfies
// options.Option so it can be passed alongside the base/gateway options.
type ComplexOption func(*complexConfig) error

// Apply implements options.Option against the complexConfig.
func (o ComplexOption) Apply(cfg options.Configurator) error {
	if cc, ok := cfg.(*complexConfig); ok {
		return o(cc)
	}

	return errs.New(
		errs.M("cfg isn't a complexConfig"),
		errs.C(errorClass, errs.InvalidParameter, errs.TypeCastingError))
}

// WithActivationThreshold sets a single guard-less threshold triple ("N of M").
// Mutually exclusive with WithActivation.
func WithActivationThreshold(n int) ComplexOption {
	return func(cc *complexConfig) error {
		if cc.set {
			return errs.New(
				errs.M("WithActivationThreshold: an activation rule is already set"),
				errs.C(errorClass, errs.InvalidParameter))
		}

		t, err := NewTriple(n)
		if err != nil {
			return err
		}

		cc.activation = []Triple{t}
		cc.set = true

		return nil
	}
}

// WithActivation sets explicit activation triples (a disjunction). Mutually exclusive
// with WithActivationThreshold.
func WithActivation(triples ...Triple) ComplexOption {
	return func(cc *complexConfig) error {
		if cc.set {
			return errs.New(
				errs.M("WithActivation: an activation rule is already set"),
				errs.C(errorClass, errs.InvalidParameter))
		}

		if len(triples) == 0 {
			return errs.New(
				errs.M("WithActivation: at least one triple expected"),
				errs.C(errorClass, errs.InvalidParameter))
		}

		cc.activation = append(cc.activation, triples...)
		cc.set = true

		return nil
	}
}

// ComplexGateway is a BPMN complex gateway (ADR-005 v.3 §2.11).
//
// Diverging, it is the inclusive split (§2.9) — it forks the conditionally-true
// outgoing subset. Converging, it is an activation-driven synchronizing join: it owns
// its per-instance arrival state under its own mutex (§2.4 / ADR-009) and decides
// fire / park / abort against its activation rule — a disjunction of Triples — using a
// data-guard evaluator and reachability supplied by the instance loop. Unlike the
// OR-join, a token death makes it abort (the arrival count is monotonic), never fire.
type ComplexGateway struct {
	activation []Triple
	order      []string
	arrived    map[string]string
	Gateway
	mu    sync.Mutex
	fired bool
}

// NewComplexGateway creates a ComplexGateway. Besides the base options
// (foundation.WithID/WithDoc, options.WithName, gateways.WithDirection) it requires
// exactly one activation source: WithActivationThreshold xor WithActivation.
func NewComplexGateway(opts ...options.Option) (*ComplexGateway, error) {
	cc := complexConfig{}
	baseOpts := make([]options.Option, 0, len(opts))
	ee := []error{}

	for _, opt := range opts {
		if co, ok := opt.(ComplexOption); ok {
			if err := co.Apply(&cc); err != nil {
				ee = append(ee, err)
			}

			continue
		}

		baseOpts = append(baseOpts, opt)
	}

	if err := cc.Validate(); err != nil {
		ee = append(ee, err)
	}

	if len(ee) != 0 {
		return nil,
			errs.New(
				errs.M("complex gateway building failed"),
				errs.C(errorClass, errs.BulidingFailed),
				errs.E(errors.Join(ee...)))
	}

	g, err := New(baseOpts...)
	if err != nil {
		return nil,
			errs.New(
				errs.M("complex gateway building failed"),
				errs.C(errorClass, errs.BulidingFailed),
				errs.E(err))
	}

	return &ComplexGateway{
			Gateway:    *g,
			activation: cc.activation,
			arrived:    map[string]string{},
		},
		nil
}

// Clone returns a per-instance copy: the embedded Gateway is cloned and the
// synchronizing-join state starts fresh (ADR-009); the activation rule is shared by
// reference (immutable after construction).
func (cg *ComplexGateway) Clone() flow.Node {
	return &ComplexGateway{
		Gateway:    cg.clone(),
		activation: cg.activation,
		arrived:    map[string]string{},
	}
}

// Node returns the gateway as its concrete flow node.
func (cg *ComplexGateway) Node() flow.Node {
	return cg
}

// Exec routes a diverging token through the inclusive split (§2.9); a converging /
// single-outgoing gateway is the survivor's post-merge continuation (pass-through).
// The join decision is Activate/Recheck, not Exec.
func (cg *ComplexGateway) Exec(
	ctx context.Context,
	re renv.RuntimeEnvironment,
) ([]*flow.SequenceFlow, error) {
	return cg.forkTrueSubset(ctx, re)
}

// Record registers arrivingTrackID's arrival on incomingFlowID and reports whether
// the gateway has already fired (the arrival is then a trailing token to consume). It
// makes no activation decision — reachability and guards are read only by the loop
// (Recheck), never off the arriving track's goroutine. Atomic under the gateway's own
// mutex.
func (cg *ComplexGateway) Record(incomingFlowID, arrivingTrackID string) bool {
	cg.mu.Lock()
	defer cg.mu.Unlock()

	if cg.fired {
		return true
	}

	if _, seen := cg.arrived[incomingFlowID]; !seen {
		cg.arrived[incomingFlowID] = arrivingTrackID
		cg.order = append(cg.order, arrivingTrackID)
	}

	return false
}

// Recheck is the loop's activation decision (ADR-005 v.3 §2.11): it fires when a
// triple is satisfied (survivor = last-in), aborts when the rule is unsatisfiable, or
// waits. It runs after an arrival parks (the firing path) and on every token death
// (the abort path — a death can only make the rule unsatisfiable, never newly satisfy
// it). Atomic under the gateway's own mutex.
func (cg *ComplexGateway) Recheck(
	eval exec.GuardEval, fc exec.FlowChecker,
) (exec.Decision, error) {
	cg.mu.Lock()
	defer cg.mu.Unlock()

	if cg.fired || len(cg.order) == 0 {
		return exec.Decision{}, nil
	}

	return cg.decide(eval, fc)
}

// decide evaluates the activation rule against the current arrivals and reachability.
// Fire when some triple is satisfied (survivor = last-in); abort when every triple is
// dead; otherwise wait. The caller holds cg.mu.
func (cg *ComplexGateway) decide(
	eval exec.GuardEval, fc exec.FlowChecker,
) (exec.Decision, error) {
	reachable, err := fc.CheckFlows(cg, cg.unmarkedFlows())
	if err != nil {
		// A reachability error is treated conservatively: wait (no fire, no abort).
		return exec.Decision{}, nil
	}

	reachableIDs := flowIDSet(reachable)
	anyAlive := false

	for _, t := range cg.activation {
		fireable, alive, err := cg.evalTriple(t, eval, reachableIDs)
		if err != nil {
			return exec.Decision{}, err
		}

		if fireable {
			cg.fired = true
			survivor := cg.order[len(cg.order)-1] // the completing arrival, last-in

			return exec.Decision{
				Fired:    true,
				Survivor: survivor,
				Merged:   absorb(cg.order, survivor),
			}, nil
		}

		if alive {
			anyAlive = true
		}
	}

	if !anyAlive {
		// Abort does NOT latch fired: the loop owns the abort action (it fails the
		// instance), and leaving fired clear lets a later death-triggered Recheck
		// re-detect the same abort idempotently.
		return exec.Decision{Aborted: true}, nil
	}

	return exec.Decision{}, nil
}

// evalTriple reports whether a triple fires now, and whether it can still fire later
// (alive). A triple is dead — never fireable — when its count is unreachable
// (arrived + reachable < count) or a required gate is neither arrived nor reachable.
// A structurally-satisfiable triple stays alive only while more tokens can still
// arrive (reachable non-empty); once arrivals are exhausted an unmet count or false
// guard is terminal (the exhaustion no-match).
func (cg *ComplexGateway) evalTriple(
	t Triple, eval exec.GuardEval, reachableIDs map[string]bool,
) (fireable, alive bool, err error) {
	countMet := len(cg.arrived) >= t.count
	requiredMet := cg.requiredMet(t.required)

	guardTrue := true
	if t.cond != nil {
		if guardTrue, err = eval(t.cond); err != nil {
			return false, false, err
		}
	}

	if guardTrue && countMet && requiredMet {
		return true, true, nil
	}

	if len(cg.arrived)+len(reachableIDs) < t.count {
		return false, false, nil // count can never be reached
	}

	for _, req := range t.required {
		if _, arrived := cg.arrived[req]; !arrived && !reachableIDs[req] {
			return false, false, nil // a required gate can never arrive
		}
	}

	// Structurally satisfiable: alive only while more arrivals can still come.
	return false, len(reachableIDs) > 0, nil
}

// requiredMet reports whether every required incoming flow has arrived.
func (cg *ComplexGateway) requiredMet(required []string) bool {
	for _, req := range required {
		if _, ok := cg.arrived[req]; !ok {
			return false
		}
	}

	return true
}

// unmarkedFlows returns the incoming flows that have not yet delivered a token.
func (cg *ComplexGateway) unmarkedFlows() []*flow.SequenceFlow {
	var unmarked []*flow.SequenceFlow

	for _, in := range cg.Incoming() {
		if _, marked := cg.arrived[in.ID()]; !marked {
			unmarked = append(unmarked, in)
		}
	}

	return unmarked
}

// Validate is the registration-time check (called by Process.Validate once the
// incoming flows are linked): per triple 1 <= count <= M, count >= len(required), and
// every required id is an actual incoming flow (M = the incoming-flow count).
func (cg *ComplexGateway) Validate() error {
	incoming := cg.Incoming()
	m := len(incoming)

	inIDs := make(map[string]bool, m)
	for _, f := range incoming {
		inIDs[f.ID()] = true
	}

	ee := []error{}

	for i, t := range cg.activation {
		if t.count < 1 || t.count > m {
			ee = append(ee,
				errs.New(
					errs.M("complex gateway triple count out of range [1, incoming]"),
					errs.C(errorClass, errs.InvalidObject),
					errs.D("triple", i),
					errs.D("count", t.count),
					errs.D("incoming", m)))
		}

		// count >= len(required) is a build invariant (NewTriple), not re-checked here.
		for _, req := range t.required {
			if !inIDs[req] {
				ee = append(ee,
					errs.New(
						errs.M("complex gateway required flow is not an incoming flow"),
						errs.C(errorClass, errs.InvalidObject),
						errs.D("triple", i),
						errs.D("flow_id", req)))
			}
		}
	}

	if len(ee) != 0 {
		return errors.Join(ee...)
	}

	return nil
}

// flowIDSet returns the set of ids of the given flows.
func flowIDSet(flows []*flow.SequenceFlow) map[string]bool {
	s := make(map[string]bool, len(flows))

	for _, f := range flows {
		if f != nil {
			s[f.ID()] = true
		}
	}

	return s
}

// interface checks
var (
	_ exec.NodeExecutor   = (*ComplexGateway)(nil)
	_ exec.ActivationJoin = (*ComplexGateway)(nil)
)
