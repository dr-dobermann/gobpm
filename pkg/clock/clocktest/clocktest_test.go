package clocktest

import (
	"testing"
	"time"
)

func TestNowAndAdvance(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	c := New(base)

	if !c.Now().Equal(base) {
		t.Fatalf("Now() = %v, want %v", c.Now(), base)
	}

	c.Advance(time.Hour)
	if !c.Now().Equal(base.Add(time.Hour)) {
		t.Fatalf("after Advance, Now() = %v, want %v", c.Now(), base.Add(time.Hour))
	}
}

func TestAfterFiresOnAdvance(t *testing.T) {
	base := time.Unix(0, 0)
	c := New(base)

	ch := c.After(time.Minute)

	select {
	case <-ch:
		t.Fatal("timer fired before the clock advanced")
	default:
	}

	c.Advance(time.Minute)

	select {
	case got := <-ch:
		if !got.Equal(base.Add(time.Minute)) {
			t.Fatalf("timer fired at %v, want %v", got, base.Add(time.Minute))
		}
	default:
		t.Fatal("timer did not fire after the clock advanced past its deadline")
	}
}

func TestAfterNonPositiveFiresImmediately(t *testing.T) {
	c := New(time.Unix(0, 0))

	select {
	case <-c.After(0):
	default:
		t.Fatal("non-positive After must fire immediately")
	}
}

func TestSetIgnoresEarlierAndFiresDue(t *testing.T) {
	base := time.Unix(100, 0)
	c := New(base)

	ch := c.After(10 * time.Second)

	c.Set(base.Add(-time.Hour)) // earlier — ignored, timer not yet due
	if !c.Now().Equal(base) {
		t.Fatalf("Set to an earlier time changed Now() to %v", c.Now())
	}

	select {
	case <-ch:
		t.Fatal("timer fired while still pending")
	default:
	}

	c.Set(base.Add(10 * time.Second)) // forward past the deadline — fires

	select {
	case <-ch:
	default:
		t.Fatal("Set past the deadline did not fire the timer")
	}
}
