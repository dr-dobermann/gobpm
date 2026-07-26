package thresher_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	dataobjects "github.com/dr-dobermann/gobpm/pkg/model/data_objects"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
)

// TestDataObjectScopeE2E: a Process-level DataObject seeded into the instance
// scope carries data end-to-end through the public engine (SRD-063) — a producer
// task writes it via a DataOutputAssociation (resolved to the per-instance DO by
// name), and a consumer task reads it back by name from scope.
func TestDataObjectScopeE2E(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	var got atomic.Value

	// the Process-level DataObject "order-total".
	sharedDO, err := dataobjects.New("order-total",
		data.MustItemDefinition(values.NewVariable(0),
			foundation.WithID("order-total-id")),
		data.ReadyDataState)
	require.NoError(t, err)

	// producer: emits 42 into an output the association feeds into the DataObject.
	prodOp, err := gooper.New("producer-op",
		func(_ context.Context, _ service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			return data.MustItemDefinition(values.NewVariable(42),
				foundation.WithID("order-total-id")), nil
		})
	require.NoError(t, err)

	outParam := data.MustParameter("total-out",
		data.MustItemAwareElement(
			data.MustItemDefinition(values.NewVariable(0),
				foundation.WithID("order-total-id")),
			data.UnavailableDataState))

	producer, err := activities.NewServiceTask("producer", prodOp,
		activities.WithParameters(data.Output, outParam))
	require.NoError(t, err)
	require.NoError(t,
		sharedDO.AssociateSource(producer, []string{"order-total-id"}, nil))

	// consumer: reads the DataObject back by name from scope.
	consOp, err := gooper.New("consumer-op",
		func(ctx context.Context, r service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			d, derr := r.GetData("order-total")
			if derr != nil {
				return nil, derr
			}

			got.Store(d.Value().Get(ctx))

			return nil, nil
		})
	require.NoError(t, err)

	consumer, err := activities.NewServiceTask("consumer", consOp,
		activities.WithoutParams())
	require.NoError(t, err)

	proc, err := process.New("dataobject-scope-e2e")
	require.NoError(t, err)
	start, err := events.NewStartEvent("start")
	require.NoError(t, err)
	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, producer, consumer, end, sharedDO} {
		require.NoError(t, proc.Add(e))
	}
	link(t, start, producer)
	link(t, producer, consumer)
	link(t, consumer, end)

	th, err := thresher.New("do-scope-e2e-engine")
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, th.Run(ctx))
	_, err = th.RegisterProcess(proc)
	require.NoError(t, err)

	h, err := th.StartLatest(proc.ID())
	require.NoError(t, err)

	wctx, wcancel := context.WithTimeout(ctx, 5*time.Second)
	defer wcancel()
	state, werr := h.WaitCompletion(wctx)
	require.NoError(t, werr)
	require.Equal(t, thresher.StateCompleted, state)

	require.EqualValues(t, 42, got.Load(),
		"the consumer read the producer's value from the DataObject by name")
}
