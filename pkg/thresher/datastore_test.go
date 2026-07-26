package thresher_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/pkg/datastore/memstore"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	datastores "github.com/dr-dobermann/gobpm/pkg/model/data_stores"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
)

// TestDataStoreSharedAcrossInstances (SRD-068 FR-6): two processes share one
// engine-global DataStore — a writer instance stores a value into a
// DataStoreReference, and a separate reader instance reads it back through a
// reference to the same store, both wired via WithDataStore.
func TestDataStoreSharedAcrossInstances(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	const item = "counter-id"

	singleTaskProc := func(t *testing.T, name string, task flow.Node) *process.Process {
		t.Helper()

		p, err := process.New(name)
		require.NoError(t, err)
		start, err := events.NewStartEvent("start-" + name)
		require.NoError(t, err)
		end, err := events.NewEndEvent("end-" + name)
		require.NoError(t, err)

		for _, e := range []flow.Element{start, task, end} {
			require.NoError(t, p.Add(e))
		}
		link(t, start, task)
		link(t, task, end)

		return p
	}

	// writer: emit 42 into an output the association feeds into "counter".
	writeOp, err := gooper.New("write-op",
		func(_ context.Context, _ service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			return data.MustItemDefinition(values.NewVariable(42),
				foundation.WithID(item)), nil
		})
	require.NoError(t, err)

	writer, err := activities.NewServiceTask("writer", writeOp,
		activities.WithParameters(data.Output, data.MustParameter("out",
			data.MustItemAwareElement(
				data.MustItemDefinition(values.NewVariable(0),
					foundation.WithID(item)),
				data.UnavailableDataState))))
	require.NoError(t, err)

	wref, err := datastores.New("counter", "shared",
		data.MustItemDefinition(values.NewVariable(0), foundation.WithID(item)),
		data.ReadyDataState)
	require.NoError(t, err)
	require.NoError(t, wref.AssociateSource(writer, []string{item}, nil))

	// reader: read the DataStoreReference input (filled from the shared store).
	var got atomic.Value
	readOp, err := gooper.New("read-op",
		func(ctx context.Context, r service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			d, derr := r.GetDataByID(item)
			if derr != nil {
				return nil, derr
			}

			got.Store(d.Value().Get(ctx))

			return nil, nil
		})
	require.NoError(t, err)

	reader, err := activities.NewServiceTask("reader", readOp,
		activities.WithParameters(data.Input, data.MustParameter("in",
			data.MustItemAwareElement(
				data.MustItemDefinition(values.NewVariable(0),
					foundation.WithID(item)),
				data.UnavailableDataState))))
	require.NoError(t, err)

	rref, err := datastores.New("counter", "shared",
		data.MustItemDefinition(values.NewVariable(0), foundation.WithID(item)),
		data.ReadyDataState)
	require.NoError(t, err)
	require.NoError(t, rref.AssociateTarget(reader, nil))

	wproc := singleTaskProc(t, "writer-proc", writer)
	rproc := singleTaskProc(t, "reader-proc", reader)

	// one engine, one shared store registered under "shared".
	th, err := thresher.New("datastore-e2e",
		thresher.WithDataStore("shared", memstore.New()))
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, th.Run(ctx))

	_, err = th.RegisterProcess(wproc)
	require.NoError(t, err)
	_, err = th.RegisterProcess(rproc)
	require.NoError(t, err)

	run := func(procID string) {
		t.Helper()

		h, herr := th.StartLatest(procID)
		require.NoError(t, herr)

		wctx, wcancel := context.WithTimeout(ctx, 5*time.Second)
		defer wcancel()
		state, werr := h.WaitCompletion(wctx)
		require.NoError(t, werr)
		require.Equal(t, thresher.StateCompleted, state)
	}

	// the writer instance stores 42; the reader instance reads it back.
	run(wproc.ID())
	run(rproc.ID())

	require.EqualValues(t, 42, got.Load(),
		"the reader read the writer's value from the shared engine DataStore")
}
