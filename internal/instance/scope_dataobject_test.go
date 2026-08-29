package instance

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/internal/scope"
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
)

// readDOTask builds a body task that reads DataObject doName by name from its
// scope (the walk-up) and records the value it saw.
func readDOTask(
	t *testing.T, name, doName string, saw *atomic.Value,
) *activities.ServiceTask {
	t.Helper()

	op, err := gooper.New(name,
		func(ctx context.Context, ds service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			d, derr := ds.GetData(doName)
			if derr != nil {
				return nil, derr
			}

			saw.Store(d.Value().Get(ctx))

			return nil, nil
		})
	require.NoError(t, err)

	st, err := activities.NewServiceTask(name, op, activities.WithoutParams())
	require.NoError(t, err)

	return st
}

// TestSubProcessDataObjectSeeding (SRD-063 FR-4): a DataObject declared on an
// embedded Sub-Process is seeded into that sub-process's child scope when the
// scope opens, so a body task resolves its seeded value by name via the
// walk-up.
func TestSubProcessDataObjectSeeding(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	var saw atomic.Value

	// sub-process: start → reader → end, carrying "scratch" (seeded = 7).
	sub, err := activities.NewSubProcess("sub")
	require.NoError(t, err)

	scratch, err := dataobjects.New("scratch",
		data.MustItemDefinition(values.NewVariable(7),
			foundation.WithID("scratch-id")),
		data.ReadyDataState)
	require.NoError(t, err)

	sStart, err := events.NewStartEvent("s-start")
	require.NoError(t, err)
	reader := readDOTask(t, "reader", "scratch", &saw)
	sEnd, err := events.NewEndEvent("s-end")
	require.NoError(t, err)

	for _, e := range []flow.Element{sStart, reader, sEnd, scratch} {
		require.NoError(t, sub.Add(e))
	}
	linkAll(t,
		[2]flow.Element{sStart, reader}, [2]flow.Element{reader, sEnd})

	var ran atomic.Int32
	after := hitTask(t, "after", &ran, "", 0)

	inst := runIteration(t, wrapSP(t, "subproc-do-seed", sub, after))
	require.Equal(t, Completed, inst.State())
	require.EqualValues(t, 7, saw.Load(),
		"the body task must read the sub-process DataObject seeded into its scope")
}

// TestProcessDataObjectSeeding (SRD-063 FR-3): a Process-level DataObject is
// seeded into the root scope at iteration start, so a task resolves its seeded
// value by name via the walk-up (the instanceScope.load seed path).
func TestProcessDataObjectSeeding(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	var saw atomic.Value

	p, err := process.New("proc-do-seed")
	require.NoError(t, err)

	pdo, err := dataobjects.New("proc-scratch",
		data.MustItemDefinition(values.NewVariable(11),
			foundation.WithID("proc-scratch-id")),
		data.ReadyDataState)
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)
	reader := readDOTask(t, "reader", "proc-scratch", &saw)
	end, err := events.NewEndEvent("end")
	require.NoError(t, err)

	for _, e := range []flow.Element{start, reader, end, pdo} {
		require.NoError(t, p.Add(e))
	}
	linkAll(t,
		[2]flow.Element{start, reader}, [2]flow.Element{reader, end})

	inst := runIteration(t, p)
	require.Equal(t, Completed, inst.State())
	require.EqualValues(t, 11, saw.Load(),
		"the task must read the Process-level DataObject seeded into root scope")
}

// TestSeedDataObjects covers the seeding helper's non-run branches (SRD-063
// FR-4): a non-host node is a no-op, and a Commit into an unopened child scope
// surfaces a wrapped error rather than dropping it. The happy path and the
// empty-set no-op are covered by the run-based seeding tests above.
func TestSeedDataObjects(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	pl, err := scope.New(scope.RootDataPath, nil)
	require.NoError(t, err)

	// a node that is not a dataObjectHost seeds nothing.
	var ran atomic.Int32
	require.NoError(t,
		seedDataObjects(pl, hitTask(t, "plain", &ran, "", 0), pl.Root()))

	// a sub-process Data Object committed into an UNOPENED child scope fails at
	// Commit — the error is wrapped, not silently dropped.
	sub, err := activities.NewSubProcess("sub")
	require.NoError(t, err)
	scratch, err := dataobjects.New("scratch",
		data.MustItemDefinition(values.NewVariable(1),
			foundation.WithID("scratch-id")),
		data.ReadyDataState)
	require.NoError(t, err)
	require.NoError(t, sub.Add(scratch))

	unopened, err := pl.Root().Append("nope")
	require.NoError(t, err)
	require.Error(t, seedDataObjects(pl, sub, unopened))
}
