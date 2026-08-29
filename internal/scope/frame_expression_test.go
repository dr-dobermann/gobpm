package scope

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
)

// stubEngine is an expression.Engine that evaluates nothing — the frame
// wiring under test never calls it, it only carries it.
type stubEngine struct{}

func (stubEngine) Type() string        { return "##Stub" }
func (stubEngine) Languages() []string { return []string{"stub"} }
func (stubEngine) Evaluate(
	_ context.Context, _ data.FormalExpression, _ data.Source,
) (data.Value, error) {
	return nil, nil
}

// TestFrameExpressionEngine covers the expression-engine wiring on a frame
// (SRD-097 FR-2): nil until set — the transient-frame case the interface
// documents, where an expression-bearing association fails fast rather than
// dereferencing it — then the engine SetExpressionEngine wired.
func TestFrameExpressionEngine(t *testing.T) {
	pl, err := New(RootDataPath, nil)
	require.NoError(t, err)

	f, err := NewFrame("track", "node", pl.Root(), pl)
	require.NoError(t, err)

	require.Nil(t, f.ExpressionEngine(),
		"a frame with nothing wired evaluates nothing")

	ee := stubEngine{}

	f.SetExpressionEngine(ee)
	require.Equal(t, ee, f.ExpressionEngine())
}
