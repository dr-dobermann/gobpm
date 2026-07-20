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

// TestMultiInstanceSequentialE2E (SRD-055): a sequential Multi-Instance
// Sub-Process over an input collection runs once per element through the public
// engine — each instance sees its element bound as `n` and assembles an output
// item `r` — completing the instances in order.
func TestMultiInstanceSequentialE2E(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	var (
		mu   sync.Mutex
		seen []any
	)

	mi, err := activities.NewMultiInstance(
		activities.WithSequential(),
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

	proc := wrapBody(t, "mi-seq-e2e", body,
		data.WithProperties(data.MustProperty("nums",
			data.MustItemDefinition(values.NewArray(2, 3, 4),
				foundation.WithID("nums")),
			data.ReadyDataState)))

	require.NoError(t, runFlows(t, proc))
	require.Equal(t, []any{2, 3, 4}, seen,
		"the sub-process runs once per collection element, in order")
}
