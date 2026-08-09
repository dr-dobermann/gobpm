package scope

import (
	"sync"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/stretchr/testify/require"
)

// stubSupplier serves a single runtime variable named "alive".
type stubSupplier struct {
	t *testing.T
}

func (s *stubSupplier) RuntimeVar(name string) (data.Data, error) {
	if name != "alive" {
		return nil,
			errs.New(
				errs.M("unknown runtime variable %q", name))
	}

	return testData(s.t, name, true), nil
}

func (s *stubSupplier) RuntimeVarNames() []string {
	return []string{"alive"}
}

func TestPlaneRuntimeVars(t *testing.T) {
	root := mustPath(t, "/proc")
	rtPath := mustPath(t, "/proc/"+RuntimeVarsSegment)

	t.Run("supplier serves the reserved path", func(t *testing.T) {
		p, err := New(root, &stubSupplier{t: t})
		require.NoError(t, err)

		d, err := p.GetData(rtPath, "alive")
		require.NoError(t, err)
		require.Equal(t, "alive", d.Name())

		_, err = p.GetData(rtPath, "ghost")
		require.Error(t, err)
	})

	t.Run("reserved path is read-only with a supplier", func(t *testing.T) {
		p, err := New(root, &stubSupplier{t: t})
		require.NoError(t, err)

		require.Error(t, errOf(p.Commit(rtPath, testData(t, "x", 1))))
		require.Error(t, p.OpenScope(rtPath))
	})

	t.Run("reserved path is read-only without a supplier",
		func(t *testing.T) {
			p, err := New(root, nil)
			require.NoError(t, err)

			require.Error(t, errOf(p.Commit(rtPath, testData(t, "x", 1))))
			require.Error(t, p.OpenScope(rtPath))

			// nothing is served either — the lookup falls through to the
			// ordinary (empty) walk.
			_, err = p.GetData(rtPath, "alive")
			require.Error(t, err)
		})

	t.Run("subtree under the reserved path is protected too",
		func(t *testing.T) {
			p, err := New(root, nil)
			require.NoError(t, err)

			sub := mustPath(t, "/proc/"+RuntimeVarsSegment+"/deep")
			require.Error(t, errOf(p.Commit(sub, testData(t, "x", 1))))
			require.Error(t, p.OpenScope(sub))
		})
}

// blockingSupplier holds its RuntimeVar call until released, so a test can see
// whether the plane's lock is held across it.
type blockingSupplier struct {
	t       *testing.T
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (s *blockingSupplier) RuntimeVar(name string) (data.Data, error) {
	s.once.Do(func() { close(s.entered) })
	<-s.release

	return testData(s.t, name, true), nil
}

func (s *blockingSupplier) RuntimeVarNames() []string { return []string{"alive"} }

// TestRuntimeVarIsServedOutsideThePlaneLock is FIX-038 T-9. GetData held p.m
// across p.rt.RuntimeVar — a call out of this package inside the plane's
// critical section, the shape this FIX removes everywhere else. It was also
// gratuitous: the runtime branch reads nothing the lock protects, because a
// runtime variable comes from the SUPPLIER and not from the plane's maps.
//
// While the supplier is working, the rest of the plane must stay usable.
func TestRuntimeVarIsServedOutsideThePlaneLock(t *testing.T) {
	root := mustPath(t, "/proc")
	rtPath := mustPath(t, "/proc/"+RuntimeVarsSegment)

	sup := &blockingSupplier{
		t:       t,
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}

	p, err := New(root, sup)
	require.NoError(t, err)

	require.NoError(t, errOf(p.Commit(root, testData(t, "ordinary", 1))))

	served := make(chan struct{})

	go func() {
		defer close(served)

		_, _ = p.GetData(rtPath, "alive")
	}()

	<-sup.entered // inside the supplier, mid-lookup

	done := make(chan struct{})

	go func() {
		defer close(done)

		_, _ = p.GetData(root, "ordinary") // any operation needing p.m
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the plane lock was held across the runtime supplier")
	}

	close(sup.release)
	<-served
}
