package thresher

import (
	"context"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/interactor"
	"github.com/stretchr/testify/require"
)

// fakeDist is a test TaskDistributor.
type fakeDist struct{}

func (fakeDist) Distribute(context.Context, interactor.TaskInfo) error { return nil }
func (fakeDist) Withdraw(context.Context, string) error                { return nil }

func TestWithTaskDistributor(t *testing.T) {
	t.Run("nil rejected", func(t *testing.T) {
		_, err := New("thr", WithTaskDistributor(nil))
		require.Error(t, err)
	})

	t.Run("default is the non-nil no-op distributor", func(t *testing.T) {
		thr, err := New("thr")
		require.NoError(t, err)
		require.NotNil(t, thr.cfg.TaskDistributor())
	})

	t.Run("custom distributor is stored", func(t *testing.T) {
		custom := &fakeDist{}
		thr, err := New("thr", WithTaskDistributor(custom))
		require.NoError(t, err)

		got, ok := thr.cfg.TaskDistributor().(*fakeDist)
		require.True(t, ok)
		require.Same(t, custom, got)
	})
}
