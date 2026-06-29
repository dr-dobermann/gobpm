package thresher_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
	"github.com/stretchr/testify/require"
)

// TestRegistryConcurrentStress hammers the registry from many goroutines at once
// — register (new version), start latest/by-version, unregister one version /
// the whole process, read State and Registrations — on a small shared set of
// keys while the engine is running (SRD-031.B NFR-1, T-5).
//
// It is the test that exercises the B1+B2 invariants together: it would deadlock
// on the old State()-under-t.m code (every Start* reads State while another
// goroutine may hold t.m) and data-race on the old inline registry lock blocks.
// Worker goroutines deliberately ignore operation errors — a StartVersion on a
// version another goroutine just removed legitimately errors; the assertions are
// "no panic, no race (go test -race), engine survives, registry stays
// consistent", checked on the test goroutine after the workers join.
func TestRegistryConcurrentStress(t *testing.T) {
	th, err := thresher.New("registry-stress")
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, th.Run(ctx))

	keys := []string{"k0", "k1", "k2"}

	// One process per key, built on the test goroutine (linearProcess uses
	// require, which must not run from a worker goroutine). snapshot.New clones
	// it per registration, so sharing the read-only definition is safe.
	procs := make(map[string]*process.Process, len(keys))
	for _, k := range keys {
		p := linearProcess(t, k, 0)
		procs[k] = p
		_, err := th.RegisterProcess(p) // seed each key with v1
		require.NoError(t, err)
	}

	const (
		workers = 8
		iters   = 40
	)

	var wg sync.WaitGroup

	wg.Add(workers)
	for w := range workers {
		go func(w int) {
			defer wg.Done()

			for i := range iters {
				k := keys[(w+i)%len(keys)]
				switch i % 6 {
				case 0:
					reg, e := th.RegisterProcess(procs[k])
					if e == nil && i%12 == 0 {
						_ = th.UnregisterVersion(reg)
					}
				case 1:
					_, _ = th.StartLatest(k)
				case 2:
					_, _ = th.StartVersion(k, 1+(i%3))
				case 3:
					_ = th.State()
				case 4:
					_ = th.Registrations(k)
				case 5:
					if i%18 == 0 {
						_ = th.UnregisterProcess(k)
						_, _ = th.RegisterProcess(procs[k]) // re-seed
					}
				}
			}
		}(w)
	}
	wg.Wait()

	// The registry is consistent: each surviving key's versions are strictly
	// ascending (removals may leave gaps, but never reorder or duplicate).
	for _, k := range keys {
		vs := regVersions(th.Registrations(k))
		for i := 1; i < len(vs); i++ {
			require.Greater(t, vs[i], vs[i-1],
				"versions must stay strictly ascending for key %q", k)
		}
	}

	// The engine still shuts down cleanly after the concurrent churn.
	sctx, sc := context.WithTimeout(context.Background(), 5*time.Second)
	defer sc()
	require.NoError(t, th.Shutdown(sctx))
	require.Equal(t, thresher.Stopped, th.State())
}
