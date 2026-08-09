package scope

import (
	"fmt"
	"sync"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/stretchr/testify/require"
)

// errOf discards Commit's changed-path set, adapting the two-value return to
// the error-only require idiom of the pre-SRD-044 assertions.
func errOf(_ []data.Change, err error) error { return err }

// testData builds a named data.Data item carrying val.
func testData(t *testing.T, name string, val any) data.Data {
	t.Helper()

	_ = data.CreateDefaultStates()

	iae := data.MustItemAwareElement(
		data.MustItemDefinition(values.NewVariable(val)),
		data.ReadyDataState)

	p, err := data.NewParameter(name, iae)
	require.NoError(t, err)

	return p
}

// unnamedData misbehaves on Name() to exercise the plane's batch validation.
type unnamedData struct {
	data.Data
}

func (u unnamedData) Name() string { return "   " }

// noIDData misbehaves on ItemDefinition() to exercise the id-lookup guard.
type noIDData struct {
	data.Data
}

func (n noIDData) ItemDefinition() *data.ItemDefinition { return nil }

func mustPath(t *testing.T, p string) DataPath {
	t.Helper()

	dp, err := NewDataPath(p)
	require.NoError(t, err)

	return dp
}

func TestNewPlane(t *testing.T) {
	t.Run("invalid root", func(t *testing.T) {
		_, err := New(DataPath("no-slash"), nil)
		require.Error(t, err)
	})

	t.Run("valid root, nil supplier", func(t *testing.T) {
		p, err := New(mustPath(t, "/proc"), nil)
		require.NoError(t, err)
		require.Equal(t, mustPath(t, "/proc"), p.Root())
	})
}

func TestPlaneCommitAndGet(t *testing.T) {
	root := mustPath(t, "/proc")
	child := mustPath(t, "/proc/sub")

	p, err := New(root, nil)
	require.NoError(t, err)
	require.NoError(t, p.OpenScope(child))

	x := testData(t, "x", 42)
	require.NoError(t, errOf(p.Commit(root, x)))

	t.Run("get by name from root", func(t *testing.T) {
		d, err := p.GetData(root, "x")
		require.NoError(t, err)
		require.Equal(t, "x", d.Name())
	})

	t.Run("walk-up: child resolves root data", func(t *testing.T) {
		d, err := p.GetData(child, "x")
		require.NoError(t, err)
		require.Equal(t, "x", d.Name())
	})

	t.Run("child data shadows nothing upward", func(t *testing.T) {
		y := testData(t, "y", "child-only")
		require.NoError(t, errOf(p.Commit(child, y)))

		// visible from the child...
		_, err := p.GetData(child, "y")
		require.NoError(t, err)

		// ...but not from the root (no downward visibility).
		_, err = p.GetData(root, "y")
		require.Error(t, err)
	})

	t.Run("get by ItemDefinition id", func(t *testing.T) {
		d, err := p.GetDataByID(child, x.ItemDefinition().ID())
		require.NoError(t, err)
		require.Equal(t, "x", d.Name())
	})

	t.Run("unknown name and id fail", func(t *testing.T) {
		_, err := p.GetData(root, "ghost")
		require.Error(t, err)

		_, err = p.GetDataByID(root, "ghost-id")
		require.Error(t, err)
	})

	t.Run("batch is applied wholly", func(t *testing.T) {
		a, b := testData(t, "a", 1), testData(t, "b", 2)
		require.NoError(t, errOf(p.Commit(root, a, b)))

		for _, n := range []string{"a", "b"} {
			_, err := p.GetData(root, n)
			require.NoError(t, err)
		}
	})

	t.Run("empty batch is a no-op", func(t *testing.T) {
		require.NoError(t, errOf(p.Commit(root)))
	})
}

func TestPlaneCommitValidation(t *testing.T) {
	root := mustPath(t, "/proc")

	p, err := New(root, nil)
	require.NoError(t, err)

	t.Run("nil data rejected, nothing applied", func(t *testing.T) {
		ok := testData(t, "ok", 1)
		require.Error(t, errOf(p.Commit(root, ok, nil)))

		// all-or-nothing: the valid head of the batch was not applied.
		_, err := p.GetData(root, "ok")
		require.Error(t, err)
	})

	t.Run("unnamed data rejected", func(t *testing.T) {
		// data constructors reject blank names themselves, so an unnamed
		// item can only reach Commit through a misbehaving data.Data
		// implementation — modeled by the stub.
		require.Error(t,
			errOf(p.Commit(root, unnamedData{Data: testData(t, "stub", 1)})))
	})

	t.Run("unopened scope rejected", func(t *testing.T) {
		require.Error(t,
			errOf(p.Commit(mustPath(t, "/proc/ghost"), testData(t, "x", 1))))
	})

	t.Run("path outside the plane rejected", func(t *testing.T) {
		require.Error(t, errOf(p.Commit(mustPath(t, "/other"), testData(t, "x", 1))))
		_, err := p.GetData(mustPath(t, "/other"), "x")
		require.Error(t, err)
	})

	t.Run("invalid path rejected", func(t *testing.T) {
		require.Error(t, errOf(p.Commit(DataPath("proc"), testData(t, "x", 1))))
	})

	t.Run("empty lookup args rejected", func(t *testing.T) {
		_, err := p.GetData(root, "   ")
		require.Error(t, err)

		_, err = p.GetDataByID(root, "")
		require.Error(t, err)
	})

	t.Run("invalid paths rejected on lookups and close", func(t *testing.T) {
		_, err := p.GetDataByID(DataPath("bad"), "id")
		require.Error(t, err)

		require.Error(t, p.CloseScope(DataPath("bad")))
	})

	t.Run("nil ItemDefinition is skipped by id lookup", func(t *testing.T) {
		require.NoError(t, errOf(p.Commit(root, noIDData{testData(t, "noid", 1)})))

		_, err := p.GetDataByID(root, "any-id-at-all")
		require.Error(t, err)
	})

	t.Run("walk over a malformed path fails cleanly", func(t *testing.T) {
		// white-box: the public surface validates paths before the walk,
		// so the walk's own guard is exercised directly.
		_, err := p.getData(
			DataPath("no-slash"), "x",
			func(data.Data) bool { return false })
		require.Error(t, err)
	})
}

func TestPlaneOpenClose(t *testing.T) {
	root := mustPath(t, "/proc")
	child := mustPath(t, "/proc/sub")
	grand := mustPath(t, "/proc/sub/inner")

	p, err := New(root, nil)
	require.NoError(t, err)

	t.Run("open child of root", func(t *testing.T) {
		require.NoError(t, p.OpenScope(child))
	})

	t.Run("duplicate open rejected", func(t *testing.T) {
		require.Error(t, p.OpenScope(child))
	})

	t.Run("root reopen rejected", func(t *testing.T) {
		require.Error(t, p.OpenScope(root))
	})

	t.Run("orphan open rejected", func(t *testing.T) {
		require.Error(t, p.OpenScope(mustPath(t, "/proc/no-parent/inner")))
	})

	t.Run("invalid path open rejected", func(t *testing.T) {
		require.Error(t, p.OpenScope(DataPath("bad")))
	})

	t.Run("close with open child rejected", func(t *testing.T) {
		require.NoError(t, p.OpenScope(grand))
		require.Error(t, p.CloseScope(child))
		require.NoError(t, p.CloseScope(grand))
	})

	t.Run("close drops the scope data", func(t *testing.T) {
		require.NoError(t, errOf(p.Commit(child, testData(t, "gone", 1))))
		require.NoError(t, p.CloseScope(child))

		_, err := p.GetData(root, "gone")
		require.Error(t, err)
	})

	t.Run("close root rejected", func(t *testing.T) {
		require.Error(t, p.CloseScope(root))
	})

	t.Run("close unknown rejected", func(t *testing.T) {
		require.Error(t, p.CloseScope(mustPath(t, "/proc/ghost")))
	})
}

func TestPlaneRootDataPath(t *testing.T) {
	// a plane rooted at "/" contains every valid path.
	p, err := New(RootDataPath, nil)
	require.NoError(t, err)

	require.NoError(t, p.OpenScope(mustPath(t, "/any")))
	require.NoError(t, errOf(p.Commit(mustPath(t, "/any"), testData(t, "x", 1))))

	d, err := p.GetData(mustPath(t, "/any"), "x")
	require.NoError(t, err)
	require.Equal(t, "x", d.Name())

	// the root itself has no parent to attach to.
	require.Error(t, p.OpenScope(RootDataPath))
}

func TestPlaneConcurrent(t *testing.T) {
	const (
		writers    = 8
		readers    = 8
		iterations = 50
	)

	root := mustPath(t, "/proc")

	p, err := New(root, nil)
	require.NoError(t, err)

	seed := testData(t, "seed", 0)
	require.NoError(t, errOf(p.Commit(root, seed)))

	// items are pre-built serially: the engine's id generator isn't safe
	// for concurrent construction, and this test targets the PLANE's
	// concurrency, not the constructors'.
	batches := make([][]data.Data, writers)
	for w := range writers {
		batches[w] = make([]data.Data, iterations)
		for i := range iterations {
			batches[w][i] = testData(t, fmt.Sprintf("w%d-i%d", w, i), i)
		}
	}

	errCh := make(chan error, (writers+readers)*iterations)

	var wg sync.WaitGroup

	for w := range writers {
		wg.Go(func() {
			for i := range iterations {
				errCh <- errOf(p.Commit(root, batches[w][i]))
			}
		})
	}

	for range readers {
		wg.Go(func() {
			for range iterations {
				_, err := p.GetData(root, "seed")
				errCh <- err
			}
		})
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		require.NoError(t, err)
	}

	// no lost batch: every committed item is retrievable.
	for w := range writers {
		for i := range iterations {
			name := fmt.Sprintf("w%d-i%d", w, i)

			_, err := p.GetData(root, name)
			require.NoError(t, err, "lost commit %s", name)
		}
	}
}

// TestScopeNamesFromRoot covers FIX-014 1.4: a root ("/") scope is visible as an
// ancestor of a deeper `from`, where the old prefix test dropped it because
// "/" + PathSeparator is "//" and never prefixes a child path. namesFrom is
// exercised directly over a synthetic scope tree (a "/" root is unreachable in
// production, where the root is keyed at the process name).
func TestScopeNamesFromRoot(t *testing.T) {
	p := &Scope{
		scopes: map[DataPath]map[string]data.Data{
			"/":    {"rootVar": testData(t, "rootVar", 1)},
			"/sub": {"childVar": testData(t, "childVar", 2)},
		},
	}

	names := p.namesFrom("/sub")
	require.Contains(t, names, "rootVar")
	require.Contains(t, names, "childVar")
}

// sharedIDData carries a chosen ItemDefinition id, so a scope can hold several
// data of "one type" — the send/receive pattern that makes id resolution
// ambiguous in real models.
type sharedIDData struct {
	data.Data

	idef *data.ItemDefinition
}

func (s sharedIDData) ItemDefinition() *data.ItemDefinition { return s.idef }

// TestGetDataByIDIsDeterministic is FIX-038 T-7. Resolution by ItemDefinition
// id iterated a scope's map and returned the first match, and Go randomizes
// that order — so the same lookup answered differently on different runs.
//
// It is not an error for several data to share an id: a send/receive flow binds
// a message's payload definition to both the message variable and the variable
// receiving it. The rule is therefore total rather than strict — nearest scope
// first, lowest name within a scope — and, above all, the SAME every time.
func TestGetDataByIDIsDeterministic(t *testing.T) {
	root := mustPath(t, "/proc")

	p, err := New(root, nil)
	require.NoError(t, err)

	idef := data.MustItemDefinition(values.NewVariable("payload"))

	// three data of one "type" in one scope, added in a non-alphabetical order
	for _, n := range []string{"zulu", "alpha", "mike"} {
		require.NoError(t, errOf(p.Commit(root,
			sharedIDData{Data: testData(t, n, "v"), idef: idef})))
	}

	// The same answer, every time — the defect was that this varied.
	for range 50 {
		got, err := p.GetDataByID(root, idef.ID())
		require.NoError(t, err)
		require.Equal(t, "alpha", got.Name(),
			"within one scope the lowest name wins, and it wins every time")
	}

	// A nearer scope still outranks the tiebreak: proximity is the meaningful
	// rule, the name order only settles a tie.
	child, err := root.Append("child")
	require.NoError(t, err)
	require.NoError(t, p.OpenScope(child))

	require.NoError(t, errOf(p.Commit(child,
		sharedIDData{Data: testData(t, "omega", "v"), idef: idef})))

	got, err := p.GetDataByID(child, idef.ID())
	require.NoError(t, err)
	require.Equal(t, "omega", got.Name(),
		"the nearest scope answers before any ancestor")
}

// TestSnapshotAtIsAtomic is FIX-038 T-6. SnapshotAt read the visible NAMES
// under one acquisition and then each DATUM under another, so a concurrent
// mutation landed in between. It runs on the track goroutine, where commits
// bypass the loop, so that window is reachable — and the result was a snapshot
// of a world that never existed at any instant, which is exactly what the
// compensation ledger and the incident snapshot rely on it not to be.
//
// The assertion is internal consistency: every datum a snapshot returns must
// carry the value of ONE generation, never a mixture of two.
func TestSnapshotAtIsAtomic(t *testing.T) {
	root := mustPath(t, "/proc")

	p, err := New(root, nil)
	require.NoError(t, err)

	const names = 12

	for i := range names {
		require.NoError(t, errOf(p.Commit(root,
			testData(t, fmt.Sprintf("v%02d", i), 0))))
	}

	stop := make(chan struct{})

	var wg sync.WaitGroup

	// A writer advancing EVERY name to the same generation, over and over. A
	// torn snapshot shows two generations at once.
	wg.Add(1)

	go func() {
		defer wg.Done()

		for gen := 1; ; gen++ {
			select {
			case <-stop:
				return
			default:
			}

			batch := make([]data.Data, 0, names)
			for i := range names {
				batch = append(batch, testData(t, fmt.Sprintf("v%02d", i), gen))
			}

			_, _ = p.Commit(root, batch...)
		}
	}()

	for range 200 {
		snap, err := p.SnapshotAt(root)
		require.NoError(t, err, "a concurrent commit must not break the snapshot")
		require.Len(t, snap, names)

		seen := map[any]struct{}{}
		for _, d := range snap {
			seen[d.Value().Get(t.Context())] = struct{}{}
		}

		require.Len(t, seen, 1,
			"every datum must come from ONE generation — a torn snapshot mixes two")
	}

	close(stop)
	wg.Wait()
}

// TestGetDataByIDStopsAtAnUnrootedPath: the walk climbs toward the root, and a
// path that cannot yield a parent ends it. EmptyDataPath is the case a caller
// actually reaches — instances are constructed with it — so the lookup must
// answer "not visible", not spin or fault.
func TestGetDataByIDStopsAtAnUnrootedPath(t *testing.T) {
	p, err := New(mustPath(t, "/proc"), nil)
	require.NoError(t, err)

	d, err := p.dataByIDLocked(EmptyDataPath, "no-such-item")
	require.Error(t, err, "an unrooted path resolves nothing")
	require.Nil(t, d)
	require.ErrorContains(t, err, "no-such-item")
}
