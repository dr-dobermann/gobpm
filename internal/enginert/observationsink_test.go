package enginert

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/observability"
)

type recordingSink struct{ n int }

func (r *recordingSink) Emit(observability.ObsEvent) { r.n++ }

func TestDefaultObservationSinkIsEchoOnlyNeverNil(t *testing.T) {
	if got := Default().ObservationSink(); got == nil {
		t.Fatal("Default().ObservationSink() = nil, want an echo-only sink")
	}
}

func TestWithObservationSinkOverridesDefault(t *testing.T) {
	rs := &recordingSink{}

	if got := Default().WithObservationSink(rs).ObservationSink(); got != rs {
		t.Errorf("ObservationSink() = %v, want the override %v", got, rs)
	}
}

func TestWithObservationSinkNilKeepsEchoDefault(t *testing.T) {
	if got := Default().WithObservationSink(nil).ObservationSink(); got == nil {
		t.Fatal("a nil override erased the echo-only default")
	}
}
