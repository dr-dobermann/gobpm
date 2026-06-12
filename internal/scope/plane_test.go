package scope

import (
	"fmt"
	"sync"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/stretchr/testify/require"
)

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
		_, err := NewPlane(DataPath("no-slash"), nil)
		require.Error(t, err)
	})

	t.Run("valid root, nil supplier", func(t *testing.T) {
		p, err := NewPlane(mustPath(t, "/proc"), nil)
		require.NoError(t, err)
		require.Equal(t, mustPath(t, "/proc"), p.Root())
	})
}

func TestPlaneCommitAndGet(t *testing.T) {
	root := mustPath(t, "/proc")
	child := mustPath(t, "/proc/sub")

	p, err := NewPlane(root, nil)
	require.NoError(t, err)
	require.NoError(t, p.OpenScope(child))

	x := testData(t, "x", 42)
	require.NoError(t, p.Commit(root, x))

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
		require.NoError(t, p.Commit(child, y))

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
		require.NoError(t, p.Commit(root, a, b))

		for _, n := range []string{"a", "b"} {
			_, err := p.GetData(root, n)
			require.NoError(t, err)
		}
	})

	t.Run("empty batch is a no-op", func(t *testing.T) {
		require.NoError(t, p.Commit(root))
	})
}

func TestPlaneCommitValidation(t *testing.T) {
	root := mustPath(t, "/proc")

	p, err := NewPlane(root, nil)
	require.NoError(t, err)

	t.Run("nil data rejected, nothing applied", func(t *testing.T) {
		ok := testData(t, "ok", 1)
		require.Error(t, p.Commit(root, ok, nil))

		// all-or-nothing: the valid head of the batch was not applied.
		_, err := p.GetData(root, "ok")
		require.Error(t, err)
	})

	t.Run("unnamed data rejected", func(t *testing.T) {
		// data constructors reject blank names themselves, so an unnamed
		// item can only reach Commit through a misbehaving data.Data
		// implementation — modeled by the stub.
		require.Error(t,
			p.Commit(root, unnamedData{Data: testData(t, "stub", 1)}))
	})

	t.Run("unopened scope rejected", func(t *testing.T) {
		require.Error(t,
			p.Commit(mustPath(t, "/proc/ghost"), testData(t, "x", 1)))
	})

	t.Run("path outside the plane rejected", func(t *testing.T) {
		require.Error(t, p.Commit(mustPath(t, "/other"), testData(t, "x", 1)))
		_, err := p.GetData(mustPath(t, "/other"), "x")
		require.Error(t, err)
	})

	t.Run("invalid path rejected", func(t *testing.T) {
		require.Error(t, p.Commit(DataPath("proc"), testData(t, "x", 1)))
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
		require.NoError(t, p.Commit(root, noIDData{testData(t, "noid", 1)}))

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

	p, err := NewPlane(root, nil)
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

	t.Run("close with open child rejected", func(t *testing.T) {
		require.NoError(t, p.OpenScope(grand))
		require.Error(t, p.CloseScope(child))
		require.NoError(t, p.CloseScope(grand))
	})

	t.Run("close drops the scope data", func(t *testing.T) {
		require.NoError(t, p.Commit(child, testData(t, "gone", 1)))
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
	p, err := NewPlane(RootDataPath, nil)
	require.NoError(t, err)

	require.NoError(t, p.OpenScope(mustPath(t, "/any")))
	require.NoError(t, p.Commit(mustPath(t, "/any"), testData(t, "x", 1)))

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

	p, err := NewPlane(root, nil)
	require.NoError(t, err)

	seed := testData(t, "seed", 0)
	require.NoError(t, p.Commit(root, seed))

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
				errCh <- p.Commit(root, batches[w][i])
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
