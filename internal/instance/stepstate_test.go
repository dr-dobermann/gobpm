package instance

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStepStateString(t *testing.T) {
	cases := map[stepState]string{
		StepCreated:       "Created",
		StepStarted:       "Started",
		StepExecuting:     "Executing",
		StepAwaitsResults: "AwaitsResults",
		StepEnded:         "Ended",
		StepFailed:        "Failed",
	}

	for st, want := range cases {
		require.Equal(t, want, st.String())
	}
}
