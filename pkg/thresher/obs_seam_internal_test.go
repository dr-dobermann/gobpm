package thresher

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/observability"
)

type countingReporter struct{ n int }

func (c *countingReporter) Report(observability.Fact) { c.n++ }

// TestThresherConfigReporter: the engine config's default Reporter is echo-only
// (never nil); an explicit reporter overrides it.
func TestThresherConfigReporter(t *testing.T) {
	c := defaultConfig()
	if c.Reporter() == nil {
		t.Fatal("defaultConfig().Reporter() = nil, want echo-only")
	}

	cr := &countingReporter{}
	c.reporter = cr

	if got := c.Reporter(); got != cr {
		t.Errorf("Reporter() = %v, want the explicit reporter %v", got, cr)
	}
}
