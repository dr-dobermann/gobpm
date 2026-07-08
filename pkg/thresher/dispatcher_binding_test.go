package thresher_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/expression"
	"github.com/dr-dobermann/gobpm/pkg/tasks"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
	"github.com/stretchr/testify/require"
)

// bindingDispatcher is a capDispatcher that also records the startup binder calls,
// so a test can assert the engine wires the dispatcher (SRD-038 §3.4): the sink
// (SinkBinder) and the expression engine (ExpressionEngineBinder).
type bindingDispatcher struct {
	capDispatcher
	sink      tasks.JobCompletionSink
	exprBound expression.Engine
}

func (d *bindingDispatcher) BindSink(s tasks.JobCompletionSink) { d.sink = s }

func (d *bindingDispatcher) BindExpressionEngine(ee expression.Engine) {
	d.exprBound = ee
}

// TestThresherBindsExpressionEngineToDispatcher covers §3.4: at startup the engine
// binds its expression engine onto the dispatcher (ExpressionEngineBinder), so the
// dispatcher can run a Job's ErrorMapper when it classifies a raw fault.
func TestThresherBindsExpressionEngineToDispatcher(t *testing.T) {
	disp := &bindingDispatcher{}

	_, err := thresher.New("bind-test", thresher.WithWorkerDispatcher(disp))
	require.NoError(t, err)

	require.NotNil(t, disp.exprBound,
		"the dispatcher received the engine's expression engine")
	require.NotNil(t, disp.sink, "the dispatcher received the completion sink")
}
