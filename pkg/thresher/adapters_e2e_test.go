package thresher_test

import (
	"context"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/adapters"
	"github.com/dr-dobermann/gobpm/pkg/model/data/goexpr"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/gateways"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
	"github.com/dr-dobermann/gobpm/pkg/observability"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
	"github.com/stretchr/testify/require"
)

// HostOrder is the host-owned process property type — wrapped, not converted.
type HostOrder struct {
	Total int `gobpm:"total"`
}

// HostReceipt is the host-owned task-output type.
type HostReceipt struct {
	Sum int `gobpm:"sum"`
}

// wrappedReceiptTask returns an in-process task whose output is a WRAPPED
// host struct under the item id "receipt".
func wrappedReceiptTask(t *testing.T, name string, sum int) *activities.ServiceTask {
	t.Helper()

	op, err := gooper.New(name,
		func(_ context.Context, _ service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			return data.MustItemDefinition(
				adapters.MustWrap(&HostReceipt{Sum: sum}),
				foundation.WithID("receipt")), nil
		})
	require.NoError(t, err)

	st, err := activities.NewServiceTask(name, op, activities.WithoutParams())
	require.NoError(t, err)

	return st
}

// TestWrappedStructE2E (SRD-045 T-8): a wrapped host struct as process data —
// a gateway condition routes on its structural path through the real engine,
// and committing wrapped outputs emits per-path DataChange facts (the S3
// commit-diff running over adapter records). Zero engine changes.
func TestWrappedStructE2E(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	// the wrapped property the gateway routes on
	order := &HostOrder{Total: 150}

	proc, err := process.New("adapters-e2e",
		data.WithProperties(
			data.MustProperty("order",
				data.MustItemDefinition(adapters.MustWrap(order),
					foundation.WithID("order")),
				data.ReadyDataState)))
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	produce := wrappedReceiptTask(t, "produce", 5)
	reprice := wrappedReceiptTask(t, "reprice", 6)

	xor, err := gateways.NewExclusiveGateway()
	require.NoError(t, err)

	routedPremium := false

	premium, err := gooper.New("premium",
		func(_ context.Context, _ service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			routedPremium = true

			return nil, nil
		})
	require.NoError(t, err)

	premiumTask, err := activities.NewServiceTask("premium", premium,
		activities.WithoutParams())
	require.NoError(t, err)

	standardTask := wrappedReceiptTask(t, "standard", 0) // must not run

	endP, err := events.NewEndEvent("end-premium")
	require.NoError(t, err)

	endS, err := events.NewEndEvent("end-standard")
	require.NoError(t, err)

	for _, e := range []flow.Element{
		start, produce, reprice, xor, premiumTask, standardTask, endP, endS,
	} {
		require.NoError(t, proc.Add(e))
	}

	// the condition reads INTO the wrapped struct by path
	over100 := goexpr.Must(nil,
		data.MustItemDefinition(values.NewVariable(false)),
		func(ctx context.Context, ds data.Source) (data.Value, error) {
			d, err := ds.Find(ctx, "order.total")
			if err != nil {
				return nil, err
			}

			total, _ := d.Value().Get(ctx).(int)

			return values.NewVariable(total > 100), nil
		})

	link(t, start, produce)
	link(t, produce, reprice)
	link(t, reprice, xor)

	_, err = flow.Link(xor, premiumTask, flow.WithCondition(over100))
	require.NoError(t, err)

	def, err := flow.Link(xor, standardTask)
	require.NoError(t, err)
	require.NoError(t, xor.UpdateDefaultFlow(def))

	link(t, premiumTask, endP)
	link(t, standardTask, endS)

	th, err := thresher.New("adapters-e2e-engine")
	require.NoError(t, err)

	c := &collector{}
	sub := th.Observe(c)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, th.Run(ctx))

	_, err = th.RegisterProcess(proc)
	require.NoError(t, err)

	h, err := th.StartLatest(proc.ID())
	require.NoError(t, err)

	wctx, wcancel := context.WithTimeout(ctx, 5*time.Second)
	defer wcancel()

	state, err := h.WaitCompletion(wctx)
	require.NoError(t, err)
	require.Equal(t, thresher.StateCompleted, state)

	require.NoError(t, th.Shutdown(context.Background()))
	sub.Cancel() // drain before asserting

	// the gateway routed on the wrapped struct's path
	require.True(t, routedPremium, "order.total>100 must route premium")

	// the S3 commit-diff ran over the two wrapped receipts
	got := dataChanges(c)
	require.Len(t, got, 2)
	require.Equal(t, observability.PhaseValueAdded, got[0].Phase)
	require.Equal(t, "receipt", got[0].Details[observability.AttrDataPath])
	require.Equal(t, observability.PhaseValueUpdated, got[1].Phase)
	require.Equal(t, "receipt.sum", got[1].Details[observability.AttrDataPath])
}
