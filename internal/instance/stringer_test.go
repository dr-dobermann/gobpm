package instance

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/internal/enginert"
	"github.com/dr-dobermann/gobpm/internal/scope"
)

// TestStringerRendersIdentity pins what the Stringer is FOR: %v must
// produce the element's identity, not a walk of the struct. Asserting
// the absence of a field name is the actual property — a reflected
// render would carry "corr" and "tracks", and it is that walk, not the
// text, that reads engine state without synchronization (FIX-040).
func TestStringerRendersIdentity(t *testing.T) {
	s, _ := routedMIProcess(t, "stringer", make(chan string, 2), true)

	inst, err := New(s, scope.EmptyDataPath, enginert.Default(),
		&recordingProducer{}, nil)
	require.NoError(t, err)

	out := fmt.Sprintf("%v", inst)

	require.Equal(t, "instance "+inst.ID(), out)
	require.NotContains(t, out, "corr")
	require.NotContains(t, out, "tracks")
}

// TestStringerNilIsSafe covers the direct call. fmt itself never needs
// this — measured, it prints "<nil>" for a nil pointer without invoking
// String — but a caller reaching for the method gets an answer rather
// than a panic.
func TestStringerNilIsSafe(t *testing.T) {
	var (
		inst *Instance
		tr   *track
	)

	require.Equal(t, "<nil>", inst.String())
	require.Equal(t, "<nil>", tr.String())

	// and through fmt, which takes its own nil path
	require.Equal(t, "<nil>", fmt.Sprintf("%v", inst))
	require.Equal(t, "<nil>", fmt.Sprintf("%v", tr))
}
