package enginert

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/observability"
)

type recordingSink struct{ n int }

func (r *recordingSink) Report(observability.Fact) { r.n++ }

func TestDefaultReporterIsEchoOnlyNeverNil(t *testing.T) {
	if got := Default().Reporter(); got == nil {
		t.Fatal("Default().Reporter() = nil, want an echo-only sink")
	}
}

func TestWithReporterOverridesDefault(t *testing.T) {
	rs := &recordingSink{}

	if got := Default().WithReporter(rs).Reporter(); got != rs {
		t.Errorf("Reporter() = %v, want the override %v", got, rs)
	}
}

func TestWithReporterNilKeepsEchoDefault(t *testing.T) {
	if got := Default().WithReporter(nil).Reporter(); got == nil {
		t.Fatal("a nil override erased the echo-only default")
	}
}
