package instance

import (
	"testing"

	"github.com/dr-dobermann/gobpm/generated/mockeventproc"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/stretchr/testify/require"
)

// TestInstanceUnregisterEvent covers Instance.UnregisterEvent's delegation
// and its idempotency seam (FIX-003 B): an ObjectNotFound from the parent
// (the waiter/processor already gone) is success; any other error
// propagates; the argument guards reject bad input before delegating.
func TestInstanceUnregisterEvent(t *testing.T) {
	proc := mockeventproc.NewMockEventProcessor(t)
	proc.EXPECT().ID().Return("p").Maybe()

	t.Run("ObjectNotFound from parent is idempotent success",
		func(t *testing.T) {
			ep := mockeventproc.NewMockEventProducer(t)
			ep.EXPECT().
				UnregisterEvent(proc, "gone-id").
				Return(errs.New(
					errs.M("waiter not found"),
					errs.C("ANY", errs.ObjectNotFound)))

			inst := &Instance{parentEventProducer: ep}
			require.NoError(t, inst.UnregisterEvent(proc, "gone-id"))
		})

	t.Run("success passes through", func(t *testing.T) {
		ep := mockeventproc.NewMockEventProducer(t)
		ep.EXPECT().UnregisterEvent(proc, "id").Return(nil)

		inst := &Instance{parentEventProducer: ep}
		require.NoError(t, inst.UnregisterEvent(proc, "id"))
	})

	t.Run("other errors propagate", func(t *testing.T) {
		ep := mockeventproc.NewMockEventProducer(t)
		ep.EXPECT().
			UnregisterEvent(proc, "id").
			Return(errs.New(
				errs.M("boom"),
				errs.C("ANY", errs.OperationFailed)))

		inst := &Instance{parentEventProducer: ep}
		require.Error(t, inst.UnregisterEvent(proc, "id"))
	})

	t.Run("nil processor rejected", func(t *testing.T) {
		inst := &Instance{
			parentEventProducer: mockeventproc.NewMockEventProducer(t),
		}
		require.Error(t, inst.UnregisterEvent(nil, "id"))
	})

	t.Run("empty event definition id rejected", func(t *testing.T) {
		inst := &Instance{
			parentEventProducer: mockeventproc.NewMockEventProducer(t),
		}
		require.Error(t, inst.UnregisterEvent(proc, "   "))
	})

	t.Run("nil parent producer rejected", func(t *testing.T) {
		inst := &Instance{}
		require.Error(t, inst.UnregisterEvent(proc, "id"))
	})
}
