package syscl

import (
	"testing"
	"time"
)

func TestNowTracksWallClock(t *testing.T) {
	c := New()

	before := time.Now()
	got := c.Now()
	after := time.Now()

	if got.Before(before) || got.After(after) {
		t.Fatalf("Now() = %v, outside [%v, %v]", got, before, after)
	}
}

func TestAfterFires(t *testing.T) {
	c := New()

	select {
	case <-c.After(time.Millisecond):
	case <-time.After(time.Second):
		t.Fatal("After channel did not fire within the timeout")
	}
}
