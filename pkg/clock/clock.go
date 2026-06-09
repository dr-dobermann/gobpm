// Package clock defines the Clock extension: the engine's source of time and
// timer scheduling, isolated behind an interface so timer-driven behavior is
// testable. The default implementation lives in syscl (system wall clock); a
// controllable fake for tests lives in clocktest (ADR-002 §4.2).
package clock

import "time"

// Clock is the engine's source of time.
type Clock interface {
	// Now returns the current time.
	Now() time.Time
	// After returns a channel that delivers one value after d elapses.
	After(d time.Duration) <-chan time.Time
}
