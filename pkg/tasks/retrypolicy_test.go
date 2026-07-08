package tasks_test

import (
	"errors"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/tasks"
	"github.com/stretchr/testify/require"
)

// TestRetryPolicyNoRetry: NoRetry never retries — a technical fault is terminal.
func TestRetryPolicyNoRetry(t *testing.T) {
	_, retry := tasks.NoRetry().Retry(1, errors.New("x"))
	require.False(t, retry)
}

// TestRetryPolicyFixedDelay: FixedDelay retries with a constant delay until
// maxAttempts executions have run.
func TestRetryPolicyFixedDelay(t *testing.T) {
	p := tasks.FixedDelay(3, 2*time.Second)

	d, ok := p.Retry(1, nil)
	require.True(t, ok)
	require.Equal(t, 2*time.Second, d)

	d, ok = p.Retry(2, nil)
	require.True(t, ok)
	require.Equal(t, 2*time.Second, d)

	_, ok = p.Retry(3, nil) // the 3rd (last) execution failed — no more retries
	require.False(t, ok)
}

// TestRetryPolicyExponentialBackoff: ExponentialBackoff doubles the backoff from
// base each attempt until maxAttempts.
func TestRetryPolicyExponentialBackoff(t *testing.T) {
	p := tasks.ExponentialBackoff(4, 100*time.Millisecond, time.Second, false)

	d, ok := p.Retry(1, nil)
	require.True(t, ok)
	require.Equal(t, 100*time.Millisecond, d) // base

	d, _ = p.Retry(2, nil)
	require.Equal(t, 200*time.Millisecond, d) // 2·base

	d, _ = p.Retry(3, nil)
	require.Equal(t, 400*time.Millisecond, d) // 4·base

	_, ok = p.Retry(4, nil)
	require.False(t, ok) // exhausted
}

// TestRetryPolicyExponentialCap: the backoff is capped at max.
func TestRetryPolicyExponentialCap(t *testing.T) {
	// base 1s, attempt 3 → 4·base = 4s, capped at 2s.
	p := tasks.ExponentialBackoff(5, time.Second, 2*time.Second, false)

	d, ok := p.Retry(3, nil)
	require.True(t, ok)
	require.Equal(t, 2*time.Second, d)
}

// TestRetryPolicyExponentialJitter: with jitter, the backoff is randomized into
// [d/2, d] of the computed backoff.
func TestRetryPolicyExponentialJitter(t *testing.T) {
	p := tasks.ExponentialBackoff(3, time.Second, 10*time.Second, true)

	for range 20 {
		d, ok := p.Retry(1, nil) // base 1s → jittered into [500ms, 1s]
		require.True(t, ok)
		require.GreaterOrEqual(t, d, 500*time.Millisecond)
		require.LessOrEqual(t, d, time.Second)
	}
}

// TestDefaultRetryPolicy: the engine default is 3 attempts (attempts 1 and 2
// retry, the 3rd is terminal).
func TestDefaultRetryPolicy(t *testing.T) {
	p := tasks.DefaultRetryPolicy()

	_, ok := p.Retry(1, nil)
	require.True(t, ok)

	_, ok = p.Retry(2, nil)
	require.True(t, ok)

	_, ok = p.Retry(3, nil)
	require.False(t, ok)
}
