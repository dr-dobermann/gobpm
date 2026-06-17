package instance

import (
	"strconv"
	"sync"
	"testing"
)

// TestAssociateConversationKeySetIfAbsent verifies the set-if-absent semantics
// of the conversation key-set (SRD-017 FR-1): the first value for a key wins, a
// later value for a held key does not overwrite, and empty inputs are no-ops.
func TestAssociateConversationKeySetIfAbsent(t *testing.T) {
	inst := &Instance{convKeys: map[string]string{}}

	inst.AssociateConversationKey("orderKey", "ORD-1")
	if got := inst.convKeys["orderKey"]; got != "ORD-1" {
		t.Fatalf("first associate: got %q, want ORD-1", got)
	}

	// set-if-absent: a later value for a held key must not overwrite.
	inst.AssociateConversationKey("orderKey", "ORD-2")
	if got := inst.convKeys["orderKey"]; got != "ORD-1" {
		t.Fatalf("associate overwrote a held key: got %q, want ORD-1", got)
	}

	// empty name or value is a no-op.
	inst.AssociateConversationKey("", "X")
	inst.AssociateConversationKey("shipKey", "")
	if len(inst.convKeys) != 1 {
		t.Fatalf("empty associate must be a no-op: keys = %v", inst.convKeys)
	}

	// a distinct key is added.
	inst.AssociateConversationKey("shipKey", "SHP-9")
	if got := inst.convKeys["shipKey"]; got != "SHP-9" {
		t.Fatalf("second key: got %q, want SHP-9", got)
	}
}

// TestAssociateConversationKeyConcurrent exercises the convMu guard under
// concurrent association from many goroutines (forked tracks run concurrently).
func TestAssociateConversationKeyConcurrent(t *testing.T) {
	inst := &Instance{convKeys: map[string]string{}}

	var wg sync.WaitGroup
	for i := range 50 {
		wg.Add(1)

		go func(n int) {
			defer wg.Done()

			inst.AssociateConversationKey("k"+strconv.Itoa(n), "v")
		}(i)
	}

	wg.Wait()

	if len(inst.convKeys) != 50 {
		t.Fatalf("concurrent associate: %d keys, want 50", len(inst.convKeys))
	}
}
