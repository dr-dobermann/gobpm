package thresher_test

import (
	"context"
	"sync"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
	"github.com/stretchr/testify/require"
)

// TestParallelMultiInstanceE2E (SRD-056.A): a parallel Multi-Instance
// Sub-Process over an input collection runs all its instances concurrently
// through the public engine — each sees its own element bound as `n` and
// assembles an output item `r` — completing when the last drains. Completion
// order is nondeterministic, so the set of processed elements is asserted.
func TestParallelMultiInstanceE2E(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	var (
		mu   sync.Mutex
		seen []any
	)

	mi, err := activities.NewMultiInstance(
		// no WithSequential → parallel (the §13.3.7 default).
		activities.WithInputCollection("nums", "n"),
		activities.WithOutputCollection("results", "r"))
	require.NoError(t, err)

	body, err := activities.NewSubProcess("body", activities.WithLoop(mi))
	require.NoError(t, err)

	bStart, err := events.NewStartEvent("b-start")
	require.NoError(t, err)

	op, err := gooper.New("scale",
		func(ctx context.Context, r service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			d, err := r.GetData("n")
			if err != nil {
				return nil, err
			}

			n, _ := d.Value().Get(ctx).(int)

			mu.Lock()
			seen = append(seen, n)
			mu.Unlock()

			return data.MustItemDefinition(
				values.NewVariable(n*10), foundation.WithID("r")), nil
		})
	require.NoError(t, err)
	work, err := activities.NewServiceTask("scale", op,
		activities.WithoutParams())
	require.NoError(t, err)

	bEnd, err := events.NewEndEvent("b-end")
	require.NoError(t, err)

	for _, e := range []flow.Element{bStart, work, bEnd} {
		require.NoError(t, body.Add(e))
	}
	link(t, bStart, work)
	link(t, work, bEnd)

	proc := wrapBody(t, "mi-par-e2e", body,
		data.WithProperties(data.MustProperty("nums",
			data.MustItemDefinition(values.NewArray(2, 3, 4),
				foundation.WithID("nums")),
			data.ReadyDataState)))

	require.NoError(t, runFlows(t, proc))
	require.ElementsMatch(t, []any{2, 3, 4}, seen,
		"all instances run concurrently over the collection")
}
