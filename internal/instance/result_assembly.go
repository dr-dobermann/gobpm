package instance

import (
	"context"
	"fmt"
	"sync"

	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/observability"
)

// resultAssembly collects an iteration's instances' results the way the model
// declared they should be read (ADR-025 §2.6.1).
//
// Nil for an undeclared activity, which keeps the last-wins default: each
// instance's writes land in the enclosing scope and a later one replaces an
// earlier. That is a fold for a sequential shape and order-dependent for a
// parallel one — stated plainly rather than hidden, and exactly why a model
// that needs every instance's result declares one of these.
//
// Guarded by its own mutex. A fan-out whose iterations HOLD WAITS is applied
// serially on the decorator's goroutine (§2.15a) and would need no lock — but
// one that holds none runs its iterations at once, each taking its result from
// its own goroutine, and a Go map written from two of them is a fatal race
// rather than a lost update.
type resultAssembly struct {
	strategy *activities.ResultStrategy

	// byOrdinal is the array strategy's slots, pre-sized to N so a parallel
	// instance completing out of order writes its own rather than appending.
	byOrdinal *values.Array[any]

	// byKey is the map strategy's entries, and keyedBy the ordinal that put
	// each one there — kept so a duplicate can name BOTH ordinals rather
	// than only the one that lost.
	byKey   map[string]any
	keyedBy map[string]int

	// m guards the three collections above. Held only across the bookkeeping,
	// never across the key expression: evaluating one runs arbitrary model
	// code, and holding a lock through it would make an activity's own
	// expression able to stall its siblings.
	m sync.Mutex
}

// newResultAssembly builds the assembly a declared strategy needs, or nil when
// the activity declares none or declares reduce.
//
// Reduce assembles nothing by design: it IS the default, named so a model can
// state the intent it is relying on. Building a collection for it would create
// a second copy of a value that is already in the enclosing scope.
func newResultAssembly(r *activities.ResultStrategy, n int) *resultAssembly {
	if r == nil || r.Kind() == activities.ResultReduce {
		return nil
	}

	a := resultAssembly{strategy: r}

	switch r.Kind() {
	case activities.ResultArray:
		a.byOrdinal = values.NewArray[any](make([]any, n)...)

	case activities.ResultMap:
		a.byKey = map[string]any{}
		a.keyedBy = map[string]int{}
	}

	return &a
}

// take records instance ord's result, read from ITS OWN frame before the
// commit makes the name a shared one.
//
// The map's key is evaluated here, in that same frame, which is the point of
// the timing: it lets the key use something the instance PRODUCED — the
// assignee of a User Task being the motivating case, since it is not known
// until the task is claimed.
func (a *resultAssembly) take(
	ctx context.Context, inst *Instance, f *scope.Frame, ord int,
) error {
	if a == nil {
		return nil
	}

	d, err := f.GetData(a.strategy.Item())
	if err != nil {
		// an instance that produced no result leaves its slot empty, as a
		// canceled one does: an activity whose output is optional is not an
		// error here.
		return nil
	}

	v := d.Value().Get(ctx)

	if a.byOrdinal != nil {
		a.m.Lock()
		defer a.m.Unlock()

		return a.byOrdinal.SetAt(ctx, ord, v)
	}

	return a.keyed(ctx, inst, f, ord, v)
}

// keyed places one result under the key its own instance computed.
func (a *resultAssembly) keyed(
	ctx context.Context, inst *Instance, f *scope.Frame, ord int, v any,
) error {
	res, err := inst.ExpressionEngine().Evaluate(
		ctx, a.strategy.Key(), newExecEnv(inst, f, nil))
	if err != nil {
		return fmt.Errorf(
			"the result key of instance %d didn't evaluate: %w", ord, err)
	}

	key, ok := res.Get(ctx).(string)
	if !ok || key == "" {
		// AN EMPTY OR MISSING KEY REFUSES (§2.6.1). There is no sensible slot
		// for a result with no key, and silently dropping one instance's
		// output is the failure the declared strategies exist to make
		// impossible.
		return errs.New(
			errs.M("instance %d produced an empty result key: a keyed result "+
				"has nowhere to go without one", ord),
			errs.C(errorClass, errs.InvalidState),
			errs.D(observability.AttrDataName, a.strategy.Name()))
	}

	a.m.Lock()
	defer a.m.Unlock()

	if had, taken := a.keyedBy[key]; taken && a.strategy.ErrorOnKeyRewrite() {
		// declared as a modeling error — a fan-out over participants who must
		// each answer once — so it names BOTH ordinals and the key.
		return errs.New(
			errs.M("instances %d and %d produced the same result key %q, and "+
				"the activity declares ErrorOnKeyRewrite", had, ord, key),
			errs.C(errorClass, errs.InvalidState),
			errs.D(observability.AttrDataName, a.strategy.Name()))
	}

	// otherwise the later instance overwrites, consistent with the last-wins
	// default rather than an exception to it. The loss stays detectable:
	// RUNTIME/ITERATIONS publishes the instance total, so a map holding fewer
	// entries than that says so.
	a.byKey[key] = v
	a.keyedBy[key] = ord

	return nil
}

// publish commits the assembled result at the host scope, ONCE, at activity
// completion (§2.6's visibility barrier, extended to a declared result by
// §2.6.1).
//
// Never incrementally: a concurrent activity must not be able to read a
// half-assembled collection. The default has no barrier by construction — it
// is the enclosing scope, written as the instances go.
func (a *resultAssembly) publish(host *track) error {
	if a == nil {
		return nil
	}

	a.m.Lock()
	defer a.m.Unlock()

	if a.byOrdinal != nil {
		return host.instance.sc.bindValueAt(
			host.scopePath, a.strategy.Name(), a.byOrdinal)
	}

	m, err := values.NewMap(a.byKey)
	if err != nil {
		return fmt.Errorf("the %q result didn't assemble: %w",
			a.strategy.Name(), err)
	}

	return host.instance.sc.bindValueAt(
		host.scopePath, a.strategy.Name(), m)
}
