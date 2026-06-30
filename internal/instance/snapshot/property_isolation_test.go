package snapshot_test

import (
	"context"
	"sync"
	"testing"

	"github.com/dr-dobermann/gobpm/internal/instance/snapshot"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/stretchr/testify/require"
)

// propProcess builds a minimal valid process (start -> end) carrying a single
// process property "counter" initialised to 0.
func propProcess(t *testing.T) *process.Process {
	t.Helper()

	require.NoError(t, data.CreateDefaultStates())

	p, err := process.New("prop-iso",
		data.WithProperties(
			data.MustProperty("counter",
				data.MustItemDefinition(values.NewVariable(0),
					foundation.WithID("counter")),
				data.ReadyDataState)))
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)
	end, err := events.NewEndEvent("end")
	require.NoError(t, err)
	require.NoError(t, p.Add(start))
	require.NoError(t, p.Add(end))
	_, err = flow.Link(start, end)
	require.NoError(t, err)

	return p
}

// TestSnapshotClonePropertiesIsolated covers FIX-016 1.1: two per-instance
// clones of one snapshot own distinct property objects, and a write through one
// is not visible through the other (the audited cross-instance leak, now an
// isolation invariant).
func TestSnapshotClonePropertiesIsolated(t *testing.T) {
	s, err := snapshot.New(propProcess(t))
	require.NoError(t, err)

	c1, err := s.Clone()
	require.NoError(t, err)
	c2, err := s.Clone()
	require.NoError(t, err)

	require.NotEmpty(t, c1.Properties)
	require.NotSame(t, c1.Properties[0], c2.Properties[0],
		"clones must own distinct property objects")

	ctx := context.Background()
	require.NoError(t, c1.Properties[0].Value().Update(ctx, 99))
	require.Equal(t, 0, c2.Properties[0].Value().Get(ctx),
		"a property write in one instance must not leak into another")
}

// TestSnapshotNewFreezesProperties covers FIX-016 1.2: a process property
// mutated after snapshot.New does not reach the frozen snapshot — the
// registered version owns private property copies.
func TestSnapshotNewFreezesProperties(t *testing.T) {
	p := propProcess(t)

	s, err := snapshot.New(p)
	require.NoError(t, err)

	ctx := context.Background()
	require.NoError(t, p.Properties()[0].Value().Update(ctx, 7))

	require.NotEmpty(t, s.Properties)
	require.Equal(t, 0, s.Properties[0].Value().Get(ctx),
		"a process edit after snapshot.New must not reach the frozen snapshot")
}

// TestSnapshotNewRejectsValuelessProperty covers the FIX-016 consequence that
// snapshot.New clones each property: a value-less (declared-but-unset) property
// can't be cloned — gobpm can never fill it (see AB-005) — so registration is
// rejected up front instead of silently sharing a degenerate object.
func TestSnapshotNewRejectsValuelessProperty(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	p, err := process.New("p-valueless",
		data.WithProperties(
			data.MustProperty("empty",
				data.MustItemDefinition(nil), data.UnavailableDataState)))
	require.NoError(t, err)

	start, err := events.NewStartEvent("start")
	require.NoError(t, err)
	end, err := events.NewEndEvent("end")
	require.NoError(t, err)
	require.NoError(t, p.Add(start))
	require.NoError(t, p.Add(end))
	_, err = flow.Link(start, end)
	require.NoError(t, err)

	_, err = snapshot.New(p)
	require.Error(t, err)
}

// TestSnapshotCloneConcurrentPropertyWrites covers FIX-016 1.1 under -race:
// concurrent property writes across two clones touch distinct objects, so they
// are race-free.
func TestSnapshotCloneConcurrentPropertyWrites(t *testing.T) {
	s, err := snapshot.New(propProcess(t))
	require.NoError(t, err)

	c1, err := s.Clone()
	require.NoError(t, err)
	c2, err := s.Clone()
	require.NoError(t, err)

	ctx := context.Background()

	var wg sync.WaitGroup
	for _, c := range []*snapshot.Snapshot{c1, c2} {
		wg.Add(1)

		go func(c *snapshot.Snapshot) {
			defer wg.Done()

			for i := range 100 {
				_ = c.Properties[0].Value().Update(ctx, i)
			}
		}(c)
	}

	wg.Wait()
}
