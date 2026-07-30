package activities_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/pkg/adhoc"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/gateways"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

// fixedRouter answers with a stated list, ignoring state — enough to build a
// valid ad-hoc container in model-layer tests.
type fixedRouter []string

func (r fixedRouter) Next(context.Context, adhoc.State) ([]string, error) {
	return []string(r), nil
}

// requireClass asserts err is a classified ApplicationError of the given class.
func requireClass(t *testing.T, err error, class string) {
	t.Helper()

	require.Error(t, err)

	var ae *errs.ApplicationError

	require.ErrorAs(t, err, &ae)
	require.True(t, ae.HasClass(class),
		"expected class %q, got %v", class, ae.Classes)
}

func TestAdHocOptionExclusivity(t *testing.T) {
	r := fixedRouter{"a"}

	t.Run("a nil Router is rejected — routing is never implied", func(t *testing.T) {
		_, err := activities.NewSubProcess("ah", activities.WithAdHoc(nil))
		requireClass(t, err, errs.InvalidParameter)
	})

	t.Run("ad-hoc and Event Sub-Process are exclusive", func(t *testing.T) {
		_, err := activities.NewSubProcess("ah",
			activities.WithAdHoc(r), activities.WithTriggeredByEvent())
		requireClass(t, err, errs.InvalidParameter)
	})

	t.Run("ad-hoc and Transaction are exclusive", func(t *testing.T) {
		_, err := activities.NewSubProcess("ah",
			activities.WithAdHoc(r), activities.WithTransaction())
		requireClass(t, err, errs.InvalidParameter)
	})

	t.Run("a refining option without WithAdHoc names itself", func(t *testing.T) {
		_, err := activities.NewSubProcess("plain",
			activities.WithAdHocManualSelection())
		requireClass(t, err, errs.InvalidState)
	})

	t.Run("an unknown ordering is rejected", func(t *testing.T) {
		_, err := activities.NewSubProcess("ah",
			activities.WithAdHoc(r),
			activities.WithAdHocOrdering(activities.AdHocOrdering("diagonal")))
		requireClass(t, err, errs.InvalidParameter)
	})

	t.Run("the variant reports itself and defaults to parallel", func(t *testing.T) {
		sp, err := activities.NewSubProcess("ah", activities.WithAdHoc(r))
		require.NoError(t, err)
		require.True(t, sp.IsAdHoc())
		require.False(t, sp.IsTransaction())
		require.False(t, sp.IsEventSubProcess())
	})

	t.Run("every refining option applies over WithAdHoc", func(t *testing.T) {
		require.NoError(t, data.CreateDefaultStates())

		expr, err := goexpr.New(nil,
			data.MustItemDefinition(values.NewVariable(false)),
			func(context.Context, data.Source) (data.Value, error) {
				return values.NewVariable(true), nil
			})
		require.NoError(t, err)

		sp, err := activities.NewSubProcess("ah",
			activities.WithAdHoc(r),
			activities.WithAdHocOrdering(activities.AdHocSequential),
			activities.WithAdHocManualSelection(),
			activities.WithAdHocCancelRemaining(false),
			activities.WithAdHocCompletion(expr))
		require.NoError(t, err)
		require.True(t, sp.IsAdHoc())

		// The spec is what the runtime reads, so every option must be visible
		// through it — a silently dropped option would look configured and
		// behave otherwise.
		spec := sp.AdHoc()
		require.NotNil(t, spec)
		require.Equal(t, r, spec.Router())
		require.Equal(t, activities.AdHocSequential, spec.Ordering())
		require.True(t, spec.IsManual())
		require.False(t, spec.CancelsRemaining())
		require.Equal(t, expr, spec.CompletionCondition())
	})

	t.Run("a plain Sub-Process reports a true nil spec", func(t *testing.T) {
		sp, err := activities.NewSubProcess("plain")
		require.NoError(t, err)
		require.Nil(t, sp.AdHoc(),
			"a typed nil would defeat every == nil check at the call site")
	})

	t.Run("a nil completion expression is rejected", func(t *testing.T) {
		_, err := activities.NewSubProcess("ah",
			activities.WithAdHoc(r), activities.WithAdHocCompletion(nil))
		requireClass(t, err, errs.EmptyNotAllowed)
	})

	t.Run("cancel-remaining without WithAdHoc names itself", func(t *testing.T) {
		_, err := activities.NewSubProcess("plain",
			activities.WithAdHocCancelRemaining(false))
		requireClass(t, err, errs.InvalidState)
	})

	t.Run("ordering without WithAdHoc names itself", func(t *testing.T) {
		_, err := activities.NewSubProcess("plain",
			activities.WithAdHocOrdering(activities.AdHocSequential))
		requireClass(t, err, errs.InvalidState)
	})

	t.Run("completion without WithAdHoc names itself", func(t *testing.T) {
		require.NoError(t, data.CreateDefaultStates())

		expr, err := goexpr.New(nil,
			data.MustItemDefinition(values.NewVariable(false)),
			func(context.Context, data.Source) (data.Value, error) {
				return values.NewVariable(true), nil
			})
		require.NoError(t, err)

		_, err = activities.NewSubProcess("plain",
			activities.WithAdHocCompletion(expr))
		requireClass(t, err, errs.InvalidState)
	})
}

func TestAdHocValidationRejectsInnerElements(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	adHoc := func(t *testing.T) *activities.SubProcess {
		t.Helper()

		sp, err := activities.NewSubProcess("triage",
			activities.WithAdHoc(fixedRouter{"step"}))
		require.NoError(t, err)

		return sp
	}

	t.Run("a leaf Task is admitted", func(t *testing.T) {
		sp := adHoc(t)

		task, err := activities.NewManualTask("step")
		require.NoError(t, err)
		require.NoError(t, sp.Add(task))
		require.NoError(t, sp.Validate())
	})

	t.Run("a plain inner Sub-Process is admitted", func(t *testing.T) {
		sp := adHoc(t)

		inner, err := activities.NewSubProcess("bigger step")
		require.NoError(t, err)

		task, err := activities.NewManualTask("inner step")
		require.NoError(t, err)
		require.NoError(t, inner.Add(task))
		require.NoError(t, sp.Add(inner))
		require.NoError(t, sp.Validate())
	})

	t.Run("a sequence flow between inner activities is rejected", func(t *testing.T) {
		sp := adHoc(t)

		first, err := activities.NewManualTask("first")
		require.NoError(t, err)

		second, err := activities.NewManualTask("second")
		require.NoError(t, err)

		require.NoError(t, sp.Add(first))
		require.NoError(t, sp.Add(second))

		_, err = flow.Link(first, second)
		require.NoError(t, err)

		requireClass(t, sp.Validate(), errs.InvalidObject)
	})

	t.Run("a gateway is rejected", func(t *testing.T) {
		sp := adHoc(t)

		gw, err := gateways.NewExclusiveGateway(options.WithName("split"))
		require.NoError(t, err)
		require.NoError(t, sp.Add(gw))
		requireClass(t, sp.Validate(), errs.InvalidObject)
	})

	t.Run("a start event is rejected", func(t *testing.T) {
		sp := adHoc(t)

		start, err := events.NewStartEvent("s")
		require.NoError(t, err)
		require.NoError(t, sp.Add(start))
		requireClass(t, sp.Validate(), errs.InvalidObject)
	})

	t.Run("an Event Sub-Process is rejected", func(t *testing.T) {
		sp := adHoc(t)

		esp, err := activities.NewSubProcess("handler",
			activities.WithTriggeredByEvent())
		require.NoError(t, err)
		require.NoError(t, sp.Add(esp))
		requireClass(t, sp.Validate(), errs.InvalidObject)
	})

	t.Run("a Transaction is rejected", func(t *testing.T) {
		sp := adHoc(t)

		tx, err := activities.NewSubProcess("tx", activities.WithTransaction())
		require.NoError(t, err)
		require.NoError(t, sp.Add(tx))
		requireClass(t, sp.Validate(), errs.InvalidObject)
	})
}
