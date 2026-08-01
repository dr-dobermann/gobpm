package activities

import (
	"errors"
	"testing"

	"github.com/dr-dobermann/gobpm/generated/mockexec"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// instantiateData sets up a task's per-execution data in the frame, and each of
// its three stages can fail independently. All three failures were uncovered:
// FIX-035 touched their error details in the vocabulary sweep and recorded them
// in §8.3 as needing a Frame fixture, which is what mockexec.MockFrame provides.
//
// They are worth covering rather than excluding because a task whose inputs,
// outputs or properties could not be instantiated must not execute — these
// branches are what stop it, and each names the task so the failure is
// attributable to a node rather than to the engine at large.
func TestInstantiateDataStageFailures(t *testing.T) {
	boom := errors.New("frame boom")

	for _, tc := range []struct {
		name  string
		frame func() *mockexec.MockFrame
		want  string
	}{
		{
			name: "inputs",
			frame: func() *mockexec.MockFrame {
				f := mockexec.NewMockFrame(t)
				f.EXPECT().InstantiateInputs(mock.Anything).Return(boom)

				return f
			},
			want: "couldn't instantiate task inputs",
		},
		{
			name: "outputs",
			frame: func() *mockexec.MockFrame {
				f := mockexec.NewMockFrame(t)
				f.EXPECT().InstantiateInputs(mock.Anything).Return(nil)
				f.EXPECT().InstantiateOutputs(mock.Anything).Return(boom)

				return f
			},
			want: "couldn't instantiate task outputs",
		},
		{
			name: "properties",
			frame: func() *mockexec.MockFrame {
				f := mockexec.NewMockFrame(t)
				f.EXPECT().InstantiateInputs(mock.Anything).Return(nil)
				f.EXPECT().InstantiateOutputs(mock.Anything).Return(nil)
				f.EXPECT().LoadProperties(mock.Anything).Return(boom)

				return f
			},
			want: "couldn't load task properties",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_ = data.CreateDefaultStates()

			tk, err := newTask("data-stage-" + tc.name)
			require.NoError(t, err)

			err = tk.instantiateData(tc.frame())

			require.Error(t, err)
			require.ErrorContains(t, err, tc.want)
			require.ErrorIs(t, err, boom,
				"the underlying frame error is wrapped, not replaced")
		})
	}
}
