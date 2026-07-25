package thresher_test

import (
	"context"
	"sync/atomic"
	"testing"

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
	"github.com/stretchr/testify/require"
)

// TestSubProcessDataObjectE2E (SRD-063 FR-4): a DataObject declared on an
// embedded Sub-Process is seeded into the sub-process's child scope, carries a
// value from a producer body task (output association, Node → DataObject) to a
// consumer body task (read by name from the sub-process scope), end-to-end
// through the public engine.
func TestSubProcessDataObjectE2E(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	var got atomic.Int64

	// the Sub-Process-level DataObject "scratch".
	scratch, err := dataobjects.New("scratch",
		data.MustItemDefinition(values.NewVariable(int64(0)),
			foundation.WithID("scratch-id")),
		data.ReadyDataState)
	require.NoError(t, err)

	// producer: emits 42 into an output the association feeds into "scratch".
	prodOp, err := gooper.New("producer-op",
		func(_ context.Context, _ service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			return data.MustItemDefinition(values.NewVariable(int64(42)),
				foundation.WithID("scratch-id")), nil
		})
	require.NoError(t, err)

	outParam := data.MustParameter("scratch-out",
		data.MustItemAwareElement(
			data.MustItemDefinition(values.NewVariable(int64(0)),
				foundation.WithID("scratch-id")),
			data.UnavailableDataState))

	producer, err := activities.NewServiceTask("producer", prodOp,
		activities.WithParameters(data.Output, outParam))
	require.NoError(t, err)
	require.NoError(t,
		scratch.AssociateSource(producer, []string{"scratch-id"}, nil))

	// consumer: reads "scratch" back by name from the sub-process scope.
	consOp, err := gooper.New("consumer-op",
		func(ctx context.Context, r service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			d, derr := r.GetData("scratch")
			if derr != nil {
				return nil, derr
			}

			if v, ok := d.Value().Get(ctx).(int64); ok {
				got.Store(v)
			}

			return nil, nil
		})
	require.NoError(t, err)

	consumer, err := activities.NewServiceTask("consumer", consOp,
		activities.WithoutParams())
	require.NoError(t, err)

	// sub-process: start → producer → consumer → end, carrying "scratch".
	sub, err := activities.NewSubProcess("sub")
	require.NoError(t, err)
	sStart, err := events.NewStartEvent("s-start")
	require.NoError(t, err)
	sEnd, err := events.NewEndEvent("s-end")
	require.NoError(t, err)

	for _, e := range []flow.Element{sStart, producer, consumer, sEnd, scratch} {
		require.NoError(t, sub.Add(e))
	}
	link(t, sStart, producer)
	link(t, producer, consumer)
	link(t, consumer, sEnd)

	// process: start → sub → end.
	proc, err := process.New("subprocess-dataobject-e2e")
	require.NoError(t, err)
	start, err := events.NewStartEvent("start")
	require.NoError(t, err)
	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, sub, end} {
		require.NoError(t, proc.Add(e))
	}
	link(t, start, sub)
	link(t, sub, end)

	require.NoError(t, runFlows(t, proc))

	require.EqualValues(t, 42, got.Load(),
		"the consumer read the producer's value from the sub-process "+
			"DataObject by name")
}
