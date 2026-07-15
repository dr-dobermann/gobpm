package adapters_test

import (
	"sync"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/adapters"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/stretchr/testify/require"
)

// node is a self-referential type — the registry must terminate on it.
type node struct {
	Name string `gobpm:"name"`
	Next *node  `gobpm:"next"`
}

// TestAdapterRegistry (SRD-045 T-2): one build per type (cache), concurrent
// wraps race-free, self-referential types terminate.
func TestAdapterRegistry(t *testing.T) {
	t.Run("concurrent wraps of one type", func(t *testing.T) {
		var wg sync.WaitGroup
		for range 16 {
			wg.Add(1)

			go func() {
				defer wg.Done()

				v, err := adapters.Wrap(testOrder())
				require.NoError(t, err)
				require.Equal(t, "adapters_test.Order", v.Type())
			}()
		}
		wg.Wait()
	})

	t.Run("self-referential type terminates", func(t *testing.T) {
		n := &node{Name: "a", Next: &node{Name: "b"}}

		v, err := adapters.Wrap(n)
		require.NoError(t, err)

		next, err := v.(data.Record).Field(ctxb(), "next")
		require.NoError(t, err)

		name, err := next.(data.Record).Field(ctxb(), "name")
		require.NoError(t, err)
		require.Equal(t, "b", name.Get(ctxb()))
	})

	t.Run("duplicate tag names rejected at build", func(t *testing.T) {
		type dup struct {
			A int `gobpm:"x"`
			B int `gobpm:"x"`
		}

		_, err := adapters.Wrap(&dup{})
		require.ErrorContains(t, err, "two fields")
	})

	t.Run("path-illegal tag name rejected at build", func(t *testing.T) {
		type bad struct {
			A int `gobpm:"a.b"`
		}

		_, err := adapters.Wrap(&bad{})
		require.Error(t, err)
	})
}

// stamp is a third-party-style type the host cannot modify — the Register
// target.
type stamp struct {
	at time.Time
}

// TestRegisterCustomAdapter (SRD-045 T-2a): Register[T] pre-empts the
// reflection builder at Wrap AND at field classification; nil build errs.
func TestRegisterCustomAdapter(t *testing.T) {
	require.Error(t, adapters.Register[stamp](nil))

	require.NoError(t, adapters.Register[stamp](
		func(v *stamp) data.Value {
			return values.MustRecord(
				values.F("unix", values.NewVariable(v.at.Unix())))
		}))

	t.Run("pre-empts at Wrap", func(t *testing.T) {
		s := &stamp{at: time.Unix(42, 0)}

		v, err := adapters.Wrap(s)
		require.NoError(t, err)

		unix, err := v.(data.Record).Field(ctxb(), "unix")
		require.NoError(t, err)
		require.Equal(t, int64(42), unix.Get(ctxb()))
	})

	t.Run("pre-empts at field classification", func(t *testing.T) {
		type shipment struct {
			Sent stamp `gobpm:"sent"`
		}

		v, err := adapters.Wrap(&shipment{Sent: stamp{at: time.Unix(7, 0)}})
		require.NoError(t, err)

		sent, err := v.(data.Record).Field(ctxb(), "sent")
		require.NoError(t, err)

		unix, err := sent.(data.Record).Field(ctxb(), "unix")
		require.NoError(t, err)
		require.Equal(t, int64(7), unix.Get(ctxb()))
	})
}
