package thresher_test

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/expression"
	"github.com/dr-dobermann/gobpm/pkg/observability"
	"github.com/dr-dobermann/gobpm/pkg/tasks"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
	"github.com/stretchr/testify/require"
)

// bindingDispatcher is a capDispatcher that also records the startup binder calls,
// so a test can assert the engine wires the dispatcher (SRD-038 §3.4, SRD-041
// §3.2): the completion sink (SinkBinder), the expression engine
// (ExpressionEngineBinder), and the observation sink (ReporterBinder).
type bindingDispatcher struct {
	capDispatcher
	sink      tasks.JobCompletionSink
	exprBound expression.Engine
	reporter   observability.Reporter
}

func (d *bindingDispatcher) BindSink(s tasks.JobCompletionSink) { d.sink = s }

func (d *bindingDispatcher) BindExpressionEngine(ee expression.Engine) {
	d.exprBound = ee
}

func (d *bindingDispatcher) BindReporter(s observability.Reporter) {
	d.reporter = s
}

// TestThresherBindsExpressionEngineToDispatcher covers §3.4: at startup the engine
// binds its expression engine onto the dispatcher (ExpressionEngineBinder), so the
// dispatcher can run a Job's ErrorMapper when it classifies a raw fault. It also
// covers SRD-041 §3.2: the engine binds its observation sink onto the dispatcher
// (ReporterBinder), so the dispatcher's job-lifecycle events land on the
// one seam.
func TestThresherBindsExpressionEngineToDispatcher(t *testing.T) {
	disp := &bindingDispatcher{}

	_, err := thresher.New("bind-test", thresher.WithWorkerDispatcher(disp))
	require.NoError(t, err)

	require.NotNil(t, disp.exprBound,
		"the dispatcher received the engine's expression engine")
	require.NotNil(t, disp.sink, "the dispatcher received the completion sink")
	require.NotNil(t, disp.reporter,
		"the dispatcher received the engine's observation sink")
}
