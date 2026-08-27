package instance

import (
	"context"
	"errors"
	"testing"

	"github.com/dr-dobermann/gobpm/internal/enginert"
	"github.com/dr-dobermann/gobpm/internal/instance/snapshot"
	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/datastore/memstore"
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	datastores "github.com/dr-dobermann/gobpm/pkg/model/data_stores"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/stretchr/testify/require"
)

// startToStore builds a message-start → end process declaring the required
// input "order" whose start pushes its payload into the Data Store "shared"
// under refName, and — when wire is set — into the input as well. twice
// wires the input through two associations.
func startToStore(
	t *testing.T, refName string, wire, twice bool,
) (*snapshot.Snapshot, flow.Node, flow.EventDefinition) {
	t.Helper()

	require.NoError(t, data.CreateDefaultStates())

	med := events.MustMessageEventDefinition(
		bpmncommon.MustMessage("order placed",
			data.MustItemDefinition(values.NewVariable(""),
				foundation.WithID("order_in"))),
		nil)

	p, err := process.New("born-to-store",
		data.WithInputs(strParam("order", "order-item", data.ReadyDataState)))
	require.NoError(t, err)

	start, err := events.NewStartEvent("start", events.WithMessageTrigger(med))
	require.NoError(t, err)

	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	ref, err := datastores.New(refName, "shared",
		data.MustItemDefinition(values.NewVariable(""),
			foundation.WithID("ref-item")),
		data.UnavailableDataState)
	require.NoError(t, err)

	require.NoError(t, p.Add(start))
	require.NoError(t, p.Add(end))
	_, err = flow.Link(start, end)
	require.NoError(t, err)

	require.NoError(t, ref.AssociateSource(start, []string{"order_in"}, nil))

	if wire {
		require.NoError(t, p.AssociateInput("order", start, start.Outputs()[0].ID()))
	}

	if twice {
		require.NoError(t, p.AssociateInput("order", start, start.Outputs()[0].ID()))
	}

	s, err := snapshot.New(p)
	require.NoError(t, err)

	datum := data.MustParameter("order_in",
		data.MustItemAwareElement(
			data.MustItemDefinition(values.NewVariable("ORD-9"),
				foundation.WithID("order_in")),
			data.ReadyDataState))

	fired, err := med.CloneEventDefinition([]data.Data{datum})
	require.NoError(t, err)

	return s, start, fired
}

// storeRuntime is a default runtime with one memory store registered as
// "shared"; the store is returned for the assertions.
func storeRuntime(t *testing.T) (*enginert.Runtime, *memstore.Store) {
	t.Helper()

	rt := enginert.Default()
	store := memstore.New()

	reg, ok := rt.DataStores().(*memstore.Registry)
	require.True(t, ok)
	require.NoError(t, reg.Register("shared", store))

	return rt, store
}

// TestBornStartDefersStoreWrites — a born start's Data Store writes land
// only once the contract accepted the launch: an accepted launch finds the
// store written, a refused one leaves the engine-global store untouched.
func TestBornStartDefersStoreWrites(t *testing.T) {
	ctx := context.Background()

	t.Run("accepted: the store is written after the contract bound",
		func(t *testing.T) {
			s, start, fired := startToStore(t, "counter", true, false)
			rt, store := storeRuntime(t)

			inst, err := NewFromEvent(s, scope.EmptyDataPath, rt,
				failEventProducer{}, nil, start.ID(), fired, "", "")
			require.NoError(t, err)

			d, err := inst.sc.plane.GetData(inst.sc.root, "order")
			require.NoError(t, err)
			require.Equal(t, "ORD-9", d.Value().Get(ctx))

			got, ok, err := store.Get(ctx, "counter")
			require.NoError(t, err)
			require.True(t, ok, "the deferred write was flushed")
			require.Equal(t, "ORD-9", got.Value().Get(ctx))
		})

	t.Run("refused: the store stays untouched", func(t *testing.T) {
		s, start, fired := startToStore(t, "counter", false, false)
		rt, store := storeRuntime(t)

		_, err := NewFromEvent(s, scope.EmptyDataPath, rt,
			failEventProducer{}, nil, start.ID(), fired, "", "")
		require.ErrorContains(t, err, `required input "order" is unbound`)

		_, ok, err := store.Get(ctx, "counter")
		require.NoError(t, err)
		require.False(t, ok, "a refused launch writes no store")
	})

	t.Run("a store reference named like an input stages no placeholder",
		func(t *testing.T) {
			// the reference "order" is the store's key, not the input: the
			// input stays unbound and the launch is refused
			s, start, fired := startToStore(t, "order", false, false)
			rt, _ := storeRuntime(t)

			_, err := NewFromEvent(s, scope.EmptyDataPath, rt,
				failEventProducer{}, nil, start.ID(), fired, "", "")
			require.ErrorContains(t, err, `required input "order" is unbound`)
		})

	t.Run("two associations to one input stage it once", func(t *testing.T) {
		s, start, fired := startToStore(t, "counter", true, true)
		rt, _ := storeRuntime(t)

		inst, err := NewFromEvent(s, scope.EmptyDataPath, rt,
			failEventProducer{}, nil, start.ID(), fired, "", "")
		require.NoError(t, err)

		d, err := inst.sc.plane.GetData(inst.sc.root, "order")
		require.NoError(t, err)
		require.Equal(t, "ORD-9", d.Value().Get(ctx))
	})

	t.Run("an unregistered store fails the seed", func(t *testing.T) {
		s, start, fired := startToStore(t, "counter", true, false)

		_, err := NewFromEvent(s, scope.EmptyDataPath, enginert.Default(),
			failEventProducer{}, nil, start.ID(), fired, "", "")
		require.ErrorContains(t, err, "output associations couldn't run")
	})

	t.Run("a store refusing the flushed write fails the launch", func(t *testing.T) {
		s, start, fired := startToStore(t, "counter", true, false)

		rt := enginert.Default()
		reg, ok := rt.DataStores().(*memstore.Registry)
		require.True(t, ok)
		require.NoError(t, reg.Register("shared",
			refusingPutStore{Store: memstore.New()}))

		_, err := NewFromEvent(s, scope.EmptyDataPath, rt,
			failEventProducer{}, nil, start.ID(), fired, "", "")
		require.ErrorContains(t, err, "couldn't write the Data Stores")
		require.ErrorContains(t, err, "store is read-only")
	})

	t.Run("no registry wired: the store cannot be resolved", func(t *testing.T) {
		_, err := (&deferringStores{}).Store("shared")
		require.ErrorContains(t, err, "no Data Store registry wired")
	})
}

// refusingPutStore is a memory store whose Put fails — the flush's error
// path.
type refusingPutStore struct {
	*memstore.Store
}

// Put refuses every write.
func (refusingPutStore) Put(context.Context, string, data.Data) error {
	return errors.New("store is read-only")
}
