package thresher_test

import (
	"context"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
	"github.com/dr-dobermann/gobpm/pkg/observability"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
	"github.com/stretchr/testify/require"
)

// receiptTask builds an in-process task named id whose operation returns a
// {sum} record under the item id "receipt" — its frame commit is the
// activity-boundary data change the test observes.
func receiptTask(t *testing.T, id string, sum int) *activities.ServiceTask {
	t.Helper()

	op, err := gooper.New(id,
		func(_ context.Context, _ service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			return data.MustItemDefinition(
				values.MustRecord(values.F("sum", values.NewVariable(sum))),
				foundation.WithID("receipt")), nil
		})
	require.NoError(t, err)

	st, err := activities.NewServiceTask(id, op, activities.WithoutParams())
	require.NoError(t, err)

	return st
}

// dataChanges filters the collector's facts down to the DataChange kind.
func dataChanges(c *collector) []observability.Fact {
	c.mu.Lock()
	defer c.mu.Unlock()

	var out []observability.Fact

	for _, e := range c.events {
		if e.Kind == observability.KindDataChange {
			out = append(out, e)
		}
	}

	return out
}

// TestDataChangeFactsEmitted (SRD-044 T-4): a node committing an output emits
// one KindDataChange fact per changed path, attributed to the committing node;
// a birth-init commit (the process property) emits none; an unchanged commit
// emits none.
func TestDataChangeFactsEmitted(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	// start → produce (receipt={sum:5}) → reprice (receipt={sum:6}) → end,
	// with a birth property "note" that must NOT surface as a DataChange.
	proc, err := process.New("dc-proc",
		data.WithProperties(
			data.MustProperty("note",
				data.MustItemDefinition(values.NewVariable("expedite"),
					foundation.WithID("note")),
				data.ReadyDataState)))
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)

	produce := receiptTask(t, "produce", 5)
	reprice := receiptTask(t, "reprice", 6)

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, produce, reprice, end} {
		require.NoError(t, proc.Add(e))
	}

	link(t, start, produce)
	link(t, produce, reprice)
	link(t, reprice, end)

	th, err := thresher.New("dc-engine")
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
	sub.Cancel() // drains the buffered facts before asserting

	got := dataChanges(c)
	require.Len(t, got, 2)

	// produce: the first commit of "receipt" — one Added at its root. NodeID
	// is engine-generated; the human name is NodeName.
	require.Equal(t, observability.PhaseValueAdded, got[0].Phase)
	require.Equal(t, "receipt", got[0].Details[observability.AttrDataPath])
	require.NotEmpty(t, got[0].NodeID)
	require.Equal(t, "produce", got[0].NodeName)

	// reprice: the re-commit changed one leaf — Updated at "receipt.sum".
	require.Equal(t, observability.PhaseValueUpdated, got[1].Phase)
	require.Equal(t, "receipt.sum", got[1].Details[observability.AttrDataPath])
	require.Equal(t, "reprice", got[1].NodeName)

	// The birth property never surfaced as a change (SRD-044 §4.4).
	for _, f := range got {
		require.NotEqual(t, "note", f.Details[observability.AttrDataPath])
	}
}
