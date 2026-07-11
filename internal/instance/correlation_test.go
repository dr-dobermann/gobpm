package instance

import (
	"sort"
	"strconv"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestCorrelatorAssociateSetIfAbsent (SRD-040 T-1): associate adds a new key
// reporting true, refuses to overwrite a held key reporting false, and no-ops
// an empty name or value.
func TestCorrelatorAssociateSetIfAbsent(t *testing.T) {
	c := correlator{inst: &Instance{}, keys: map[string]string{}}

	require.True(t, c.associate("orderKey", "ORD-1"))
	require.False(t, c.associate("orderKey", "ORD-2"),
		"a held key must not be overwritten")

	held, ok := c.held("orderKey")
	require.True(t, ok)
	require.Equal(t, "ORD-1", held)

	require.False(t, c.associate("", "X"))
	require.False(t, c.associate("shipKey", ""))
	require.Len(t, c.keys, 1, "empty inputs must be no-ops")
}

// TestCorrelatorValuesSnapshot (SRD-040 T-2): values() is nil while no key is
// held (a wildcard subscription) and returns every held value afterwards;
// concurrent associate calls are safe under -race (forked tracks associate
// concurrently — the correlator's own lock is the guard).
func TestCorrelatorValuesSnapshot(t *testing.T) {
	c := correlator{inst: &Instance{}, keys: map[string]string{}}

	require.Nil(t, c.values(), "no held keys → nil (wildcard)")

	var wg sync.WaitGroup
	for i := range 20 {
		wg.Add(1)

		go func(n int) {
			defer wg.Done()

			c.associate("k"+strconv.Itoa(n), "v"+strconv.Itoa(n))
		}(i)
	}

	wg.Wait()

	vals := c.values()
	sort.Strings(vals)
	require.Len(t, vals, 20)
	require.Equal(t, "v0", vals[0])
}
