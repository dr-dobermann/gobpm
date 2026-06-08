// Package clocktest provides a controllable clock.Clock for time-dependent
// tests: Now is settable and After channels fire when the clock is advanced
// past their deadline.
package clocktest

import (
	"sync"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/clock"
)

// Clock is a manually controlled clock.Clock.
type Clock struct {
	now    time.Time
	timers []timer
	mu     sync.Mutex
}

type timer struct {
	at time.Time
	ch chan time.Time
}

// New returns a Clock set to now.
func New(now time.Time) *Clock { return &Clock{now: now} }

// Now returns the current (manually set) time.
func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.now
}

// After returns a channel that fires once the clock is advanced to at least d
// past the current time. A non-positive d fires immediately.
func (c *Clock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	ch := make(chan time.Time, 1)
	if d <= 0 {
		ch <- c.now

		return ch
	}

	c.timers = append(c.timers, timer{at: c.now.Add(d), ch: ch})

	return ch
}

// Advance moves the clock forward by d, firing any timers whose deadline has
// passed.
func (c *Clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.now = c.now.Add(d)
	c.fireDueLocked()
}

// Set moves the clock to t (forward only; earlier values are ignored), firing
// any timers whose deadline has passed.
func (c *Clock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if t.After(c.now) {
		c.now = t
	}

	c.fireDueLocked()
}

func (c *Clock) fireDueLocked() {
	remaining := c.timers[:0]

	for _, t := range c.timers {
		if !t.at.After(c.now) {
			t.ch <- c.now

			continue
		}

		remaining = append(remaining, t)
	}

	c.timers = remaining
}

var _ clock.Clock = (*Clock)(nil)
