// Package syscl provides the system wall-clock implementation of clock.Clock,
// backed by time.Now and time.After. It is the engine's default Clock.
package syscl

import (
	"time"

	"github.com/dr-dobermann/gobpm/pkg/clock"
)

// Clock is the system wall clock.
type Clock struct{}

// New returns the system Clock.
func New() clock.Clock { return Clock{} }

// Now returns the current wall-clock time.
func (Clock) Now() time.Time { return time.Now() }

// After returns time.After(d).
func (Clock) After(d time.Duration) <-chan time.Time { return time.After(d) }

var _ clock.Clock = Clock{}
