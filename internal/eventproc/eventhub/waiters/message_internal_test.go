package waiters

import "testing"

// TestMessageWaiterAddKeyNilSub covers AddKey before Service has subscribed: a
// nil broker subscription makes it a no-op (SRD-017 §4.5).
func TestMessageWaiterAddKeyNilSub(t *testing.T) {
	if err := (&messageWaiter{}).AddKey("K1"); err != nil {
		t.Fatalf("AddKey on a pre-Service waiter: %v", err)
	}
}
