package instance

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// fakeClock is a deterministic time source for tests — the injectable seam
// that the ADR-002 Clock extension will later replace (SRD-001 FR-11).
type fakeClock struct {
	t time.Time
}

func newFakeClock(t time.Time) *fakeClock { return &fakeClock{t: t} }

func (f *fakeClock) Now() time.Time { return f.t }

func (f *fakeClock) advance(d time.Duration) { f.t = f.t.Add(d) }

func TestFakeClock(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	fc := newFakeClock(base)

	require.Equal(t, base, fc.Now())

	fc.advance(5 * time.Second)
	require.Equal(t, base.Add(5*time.Second), fc.Now())
}

// TestInstanceNowSeam verifies the Instance reads time through its injectable
// `now` seam rather than calling time.Now directly, so later milestones can
// inject a deterministic clock.
func TestInstanceNowSeam(t *testing.T) {
	base := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	fc := newFakeClock(base)

	inst := &Instance{now: fc.Now}

	require.Equal(t, base, inst.now())

	fc.advance(time.Hour)
	require.Equal(t, base.Add(time.Hour), inst.now())
}
