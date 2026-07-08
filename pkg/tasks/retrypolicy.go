package tasks

import "time"

// RetryPolicy decides whether a technical fault is retried and the backoff
// before the next attempt (ADR-021 §2.7). attempt is the 1-based number of the
// attempt that just failed; cause is its technical error.
//
// The batteries (NoRetry / FixedDelay / ExponentialBackoff / DefaultRetryPolicy)
// and the dispatcher-side retry loop land in SRD-038 M7; the interface is
// defined here (M6) so Policy and WorkerConfig carry their final shape.
type RetryPolicy interface {
	Retry(attempt int, cause error) (backoff time.Duration, retry bool)
}
