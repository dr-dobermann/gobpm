package tasks

import (
	"math/rand/v2"
	"time"
)

// RetryPolicy decides whether a technical fault is retried and the backoff
// before the next attempt (ADR-021 §2.7). attempt is the 1-based number of the
// attempt that just failed; cause is its technical error.
type RetryPolicy interface {
	Retry(attempt int, cause error) (backoff time.Duration, retry bool)
}

// Default retry knobs (ADR-021 §2.7): 3 attempts total (Camunda's
// defaultNumberOfRetries) with jittered exponential backoff from 500ms, capped.
const (
	defaultRetryAttempts = 3
	defaultRetryBase     = 500 * time.Millisecond
	defaultRetryMax      = 30 * time.Second
)

// noRetry never retries — the first technical fault is terminal.
type noRetry struct{}

func (noRetry) Retry(int, error) (time.Duration, bool) { return 0, false }

// NoRetry returns a RetryPolicy that never retries: a technical fault is
// terminal on first report (a fail-fast worker).
func NoRetry() RetryPolicy { return noRetry{} }

// fixedDelay retries up to maxAttempts total, waiting a constant delay.
type fixedDelay struct {
	delay       time.Duration
	maxAttempts int
}

// FixedDelay returns a RetryPolicy that retries a technical fault until
// maxAttempts executions have been made, waiting delay before each retry.
func FixedDelay(maxAttempts int, delay time.Duration) RetryPolicy {
	return fixedDelay{delay: delay, maxAttempts: maxAttempts}
}

// Retry retries while fewer than maxAttempts executions have run.
func (f fixedDelay) Retry(attempt int, _ error) (time.Duration, bool) {
	if attempt >= f.maxAttempts {
		return 0, false
	}

	return f.delay, true
}

// exponentialBackoff retries up to maxAttempts total, doubling the backoff from
// base (capped at maxBackoff), optionally jittered.
type exponentialBackoff struct {
	base        time.Duration
	maxBackoff  time.Duration
	maxAttempts int
	jitter      bool
}

// ExponentialBackoff returns a RetryPolicy that retries until maxAttempts
// executions have run, doubling the backoff (base, 2·base, 4·base, …) capped at
// maxBackoff. With jitter, each backoff is randomized into [d/2, d] to spread
// retries.
func ExponentialBackoff(
	maxAttempts int, base, maxBackoff time.Duration, jitter bool,
) RetryPolicy {
	return exponentialBackoff{
		base: base, maxBackoff: maxBackoff, maxAttempts: maxAttempts, jitter: jitter,
	}
}

// Retry computes the capped (optionally jittered) exponential backoff for the
// next attempt, or (0, false) once maxAttempts executions have run.
func (e exponentialBackoff) Retry(attempt int, _ error) (time.Duration, bool) {
	if attempt >= e.maxAttempts {
		return 0, false
	}

	// base · 2^(attempt-1), guarding overflow and the max cap.
	d := e.base << (attempt - 1)
	if d <= 0 || d > e.maxBackoff {
		d = e.maxBackoff
	}

	if e.jitter {
		// spread into [d/2, d]; Int64N's argument is always >= 1, so it is safe
		// for any d >= 0.
		d = d/2 + time.Duration(rand.Int64N(int64(d/2)+1)) //nolint:gosec // jitter, not security
	}

	return d, true
}

// DefaultRetryPolicy is the engine default when neither a per-service nor an
// engine-wide RetryPolicy is set: 3 attempts with jittered exponential backoff
// from 500ms capped at 30s — improving on Camunda's zero-wait default
// (ADR-021 §2.7).
func DefaultRetryPolicy() RetryPolicy {
	return ExponentialBackoff(
		defaultRetryAttempts, defaultRetryBase, defaultRetryMax, true)
}
