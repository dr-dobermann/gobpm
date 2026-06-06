package instance

import (
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// assertNoGoroutineLeak captures the goroutine baseline and returns a check to
// run at test end (typically via defer). It allows a short settle window for
// goroutines to unwind. Used by later milestones' -race / leak assertions.
func assertNoGoroutineLeak(t *testing.T) func() {
	t.Helper()

	base := runtime.NumGoroutine()

	return func() {
		t.Helper()

		for range 20 {
			if runtime.NumGoroutine() <= base {
				return
			}

			time.Sleep(50 * time.Millisecond)
		}

		require.LessOrEqualf(t, runtime.NumGoroutine(), base,
			"goroutine leak: baseline was %d", base)
	}
}

func TestNoGoroutineLeakHelper(t *testing.T) {
	done := assertNoGoroutineLeak(t)
	defer done()

	// no-op: a clean test must return to the goroutine baseline.
}
