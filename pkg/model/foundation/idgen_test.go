package foundation_test

import (
	"sync"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func GenerateUUID() string {
	return uuid.New().String()
}

func TestGenerator(t *testing.T) {
	id1 := foundation.GenerateID()
	require.NotEmpty(t, id1)
	// t.Log(id1)

	id2 := foundation.GenerateID()
	require.NotEmpty(t, id2)
	// t.Log(id2)

	require.NotEqual(t, id1, id2)

	// nil generator
	require.Error(t, foundation.SetGenerator(nil))

	// normal generator
	foundation.SetGenerator(
		foundation.GenFunc(GenerateUUID))

	id1 = foundation.GenerateID()
	require.NotEmpty(t, id1)
	// t.Log(id1)

	id2 = foundation.GenerateID()
	require.NotEmpty(t, id2)
	// t.Log(id2)

	require.NotEqual(t, id1, id2)
}

// TestGeneratorConcurrent is the regression for the unsynchronized
// generator: model elements are built from concurrent goroutines
// (per-execution frames, concurrent instance startup), so GenerateID and
// SetGenerator must be race-free (run with -race).
func TestGeneratorConcurrent(t *testing.T) {
	const (
		goroutines = 8
		iterations = 200
	)

	var wg sync.WaitGroup

	ids := make(chan string, goroutines*iterations)

	for range goroutines {
		wg.Go(func() {
			for range iterations {
				ids <- foundation.GenerateID()
			}
		})
	}

	// a concurrent generator swap must not race in-flight generations.
	var swapErr error

	wg.Go(func() {
		swapErr = foundation.SetGenerator(foundation.GenFunc(GenerateUUID))
	})

	wg.Wait()
	close(ids)

	require.NoError(t, swapErr)

	for id := range ids {
		require.NotEmpty(t, id)
	}
}
