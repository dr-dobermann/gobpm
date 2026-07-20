package instance

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
)

// miShape is a Multi-Instance builder for one execution shape (sequential or
// parallel), so a behavior test can assert the same semantics on both — the
// behavior helper is shared between the two paths (SRD-056.B).
type miShape struct {
	name string
	mk   func(
		t *testing.T, opts ...activities.MultiInstanceOption,
	) *activities.MultiInstanceLoopCharacteristics
}

// miShapes returns the two Multi-Instance execution shapes a behavior test runs
// against.
func miShapes() []miShape {
	return []miShape{
		{"sequential", mustSeqMI},
		{"parallel", mustParallelMI},
	}
}

// signalDef builds a signal event definition to use as a Multi-Instance behavior
// event reference (SRD-056.B).
func signalDef(t *testing.T, name string) flow.EventDefinition {
	t.Helper()

	sig, err := events.NewSignal(name, nil)
	require.NoError(t, err)
	def, err := events.NewSignalEventDefinition(sig)
	require.NoError(t, err)

	return def
}

// boolExprBoom is a boolean expression whose evaluation errors — the runtime
// failure a Complex behavior condition can hit.
func boolExprBoom(t *testing.T) data.FormalExpression {
	t.Helper()

	return goexpr.Must(nil,
		data.MustItemDefinition(values.NewVariable(false)),
		func(_ context.Context, _ data.Source) (data.Value, error) {
			return nil, errs.New(errs.M("condition boom"),
				errs.C(errorClass, errs.OperationFailed))
		})
}

// runMIBehavior runs a Multi-Instance host to completion and returns the event
// definitions its behavior throws propagated (SRD-056.B FR-6). The same builder
// drives both the sequential and the parallel shape — the mi's IsSequential()
// selects the path.
func runMIBehavior(
	t *testing.T, mi *activities.MultiInstanceLoopCharacteristics,
) []flow.EventDefinition {
	t.Helper()

	var count atomic.Int32

	inst, prod := miBehaviorInstance(t, countOp(t, &count), mi)
	runToDone(t, inst)

	require.Equal(t, Completed, inst.State())

	return prod.propagatedDefs()
}

// TestMultiInstanceBehaviorThrows: the §2.9 behavior modes throw the right event
// the right number of times on each completion, for BOTH the sequential and the
// parallel shape (ADR-025 §2.8, SRD-056.B FR-1..FR-6). None throws on every
// completion, One only on the first, Complex only when its condition holds, and
// All (the default) throws nothing.
func TestMultiInstanceBehaviorThrows(t *testing.T) {
	for _, sh := range miShapes() {
		t.Run(sh.name, func(t *testing.T) {
			t.Run("None throws on every completion", func(t *testing.T) {
				def := signalDef(t, "none")
				mi := sh.mk(t,
					activities.WithCardinality(cardExpr(t, 3)),
					activities.WithBehavior(activities.BehaviorNone),
					activities.WithNoneBehaviorEvent(def))

				got := runMIBehavior(t, mi)

				require.Len(t, got, 3, "None throws once per completion")
				for _, d := range got {
					require.Equal(t, def.ID(), d.ID())
				}
			})

			t.Run("One throws once", func(t *testing.T) {
				def := signalDef(t, "one")
				mi := sh.mk(t,
					activities.WithCardinality(cardExpr(t, 3)),
					activities.WithBehavior(activities.BehaviorOne),
					activities.WithOneBehaviorEvent(def))

				got := runMIBehavior(t, mi)

				require.Len(t, got, 1, "One throws only on the first completion")
				require.Equal(t, def.ID(), got[0].ID())
			})

			t.Run("Complex throws when the condition holds", func(t *testing.T) {
				def := signalDef(t, "complex")
				ite, err := events.NewImplicitThrowEvent("cx", def)
				require.NoError(t, err)

				cbd, err := activities.NewComplexBehaviorDefinition(
					attrAtLeast(t, "numberOfCompletedInstances", 3), ite)
				require.NoError(t, err)

				mi := sh.mk(t,
					activities.WithCardinality(cardExpr(t, 3)),
					activities.WithBehavior(activities.BehaviorComplex),
					activities.WithComplexBehavior(cbd))

				got := runMIBehavior(t, mi)

				require.Len(t, got, 1,
					"Complex throws only on the completion where the condition holds")
				require.Equal(t, def.ID(), got[0].ID())
			})

			t.Run("All throws nothing", func(t *testing.T) {
				mi := sh.mk(t, activities.WithCardinality(cardExpr(t, 3)))

				got := runMIBehavior(t, mi)

				require.Empty(t, got, "All (the default) throws no behavior event")
			})
		})
	}
}

// TestMultiInstanceComplexConditionError: a Complex behavior whose condition
// errors on evaluation faults the instance — the error propagates out of the
// completion path on both shapes (SRD-056.B).
func TestMultiInstanceComplexConditionError(t *testing.T) {
	for _, sh := range miShapes() {
		t.Run(sh.name, func(t *testing.T) {
			var count atomic.Int32

			ite, err := events.NewImplicitThrowEvent("cx", signalDef(t, "cx"))
			require.NoError(t, err)
			cbd, err := activities.NewComplexBehaviorDefinition(boolExprBoom(t), ite)
			require.NoError(t, err)

			mi := sh.mk(t,
				activities.WithCardinality(cardExpr(t, 3)),
				activities.WithBehavior(activities.BehaviorComplex),
				activities.WithComplexBehavior(cbd))

			inst, _ := miBehaviorInstance(t, countOp(t, &count), mi)
			runToDone(t, inst)

			require.NotEqual(t, Completed, inst.State(),
				"a Complex condition evaluation error faults the instance")
			require.Error(t, inst.LastErr())
		})
	}
}

// TestMultiInstanceBehaviorPropagateError: a failed behavior-event propagation
// faults the instance — the always-true Complex condition throws, and the
// producer's propagate error surfaces out of the completion path on both shapes.
func TestMultiInstanceBehaviorPropagateError(t *testing.T) {
	for _, sh := range miShapes() {
		t.Run(sh.name, func(t *testing.T) {
			var count atomic.Int32

			ite, err := events.NewImplicitThrowEvent("cx", signalDef(t, "cx"))
			require.NoError(t, err)
			cbd, err := activities.NewComplexBehaviorDefinition(
				attrAtLeast(t, "numberOfCompletedInstances", 1), ite)
			require.NoError(t, err)

			mi := sh.mk(t,
				activities.WithCardinality(cardExpr(t, 2)),
				activities.WithBehavior(activities.BehaviorComplex),
				activities.WithComplexBehavior(cbd))

			inst, prod := miBehaviorInstance(t, countOp(t, &count), mi)
			prod.propagateErr = errRegRejected
			runToDone(t, inst)

			require.NotEqual(t, Completed, inst.State(),
				"a failed behavior-event propagation faults the instance")
			require.Error(t, inst.LastErr())
		})
	}
}

// TestMultiInstanceAfterDrainBindCountersError covers the sequential completion
// path's defensive counter-publish error: draining against an unopened host
// scope fails at bindMICounters before any behavior is thrown.
func TestMultiInstanceAfterDrainBindCountersError(t *testing.T) {
	var count atomic.Int32

	mi := mustSeqMI(t, activities.WithCardinality(cardExpr(t, 1)))

	inst := miSubProcessInstance(t, &count, mi)
	inst.tracks = map[string]*track{}
	ls := newLoopState(inst)
	node := findNode(t, inst.s, "body")

	host, err := newTrack(node, inst, nil)
	require.NoError(t, err)
	// an unopened host scope makes the counter Commit fail.
	host.scopePath = scope.DataPath("/does/not/exist")
	host.miState = &miState{numberOfInstances: 1}

	_, err = miIterator{mi: mi}.afterDrain(t.Context(), ls, host, node)
	require.Error(t, err)
}
