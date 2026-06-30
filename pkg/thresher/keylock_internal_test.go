package thresher

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestKeyLockManagerGetSameAndDistinct verifies get returns the same mutex for a
// given key (so two operations on that key contend on one lock) and a distinct
// mutex per key (so different keys never serialize against each other).
func TestKeyLockManagerGetSameAndDistinct(t *testing.T) {
	m := newKeyLockManager()

	a1 := m.get("a")
	a2 := m.get("a")
	b := m.get("b")

	require.Same(t, a1, a2, "same key must yield the same mutex")
	require.NotSame(t, a1, b, "different keys must yield distinct mutexes")
}

// TestKeyLockSerializesRegisterUnregister proves the per-key lock makes a key
// operation mutually exclusive with another operation on the SAME key (closing
// the FIX-013 §1.4 TOCTOU window) while leaving a DIFFERENT key free to proceed.
//
// The test holds key "k" itself, then launches UnregisterProcess("k") and
// UnregisterProcess("other") in goroutines: the same-key call must block until
// the test releases, the different-key call must return without waiting.
func TestKeyLockSerializesRegisterUnregister(t *testing.T) {
	th, err := New("keylock-serialize")
	require.NoError(t, err)

	release := th.lockKey("k")

	sameKey := make(chan struct{})
	go func() {
		// Blocks on the per-key lock for "k" until release() runs; the error
		// (ObjectNotFound — nothing registered) is irrelevant, we assert timing.
		_ = th.UnregisterProcess("k")
		close(sameKey)
	}()

	otherKey := make(chan struct{})
	go func() {
		_ = th.UnregisterProcess("other")
		close(otherKey)
	}()

	// A different key never contends on "k"'s lock — it returns promptly.
	select {
	case <-otherKey:
	case <-time.After(time.Second):
		t.Fatal("UnregisterProcess on a different key blocked behind key k")
	}

	// The same key is held: its goroutine must still be blocked.
	select {
	case <-sameKey:
		t.Fatal("UnregisterProcess on key k did not wait for the held per-key lock")
	case <-time.After(50 * time.Millisecond):
	}

	release()

	// Once released, the same-key operation proceeds.
	select {
	case <-sameKey:
	case <-time.After(time.Second):
		t.Fatal("UnregisterProcess on key k did not proceed after release")
	}
}
