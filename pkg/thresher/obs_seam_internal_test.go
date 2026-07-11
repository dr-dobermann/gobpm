package thresher

import (
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/observability"
)

// T-1: the public EventKind vocabulary equals the canonical observability.Kind
// values (EventKind is a type alias), so the delivered public event and the
// internal observable event share one vocabulary with no conversion.
func TestEventKindAliasesObservabilityKinds(t *testing.T) {
	pairs := map[EventKind]observability.Kind{
		EventInstanceState:    observability.KindInstanceState,
		EventNodeProgress:     observability.KindNodeProgress,
		EventEngineState:      observability.KindEngineState,
		EventHubState:         observability.KindHubState,
		EventProcessLifecycle: observability.KindProcessLifecycle,
		EventGatewayDecision:  observability.KindGatewayDecision,
		EventFlow:             observability.KindEventFlow,
		EventCorrelation:      observability.KindCorrelation,
		EventJobState:         observability.KindJobState,
		EventTaskState:        observability.KindTaskState,
		EventBoundary:         observability.KindBoundary,
		EventFault:            observability.KindFault,
		EventDataChange:       observability.KindDataChange,
	}

	for got, want := range pairs {
		if string(got) != string(want) {
			t.Errorf("EventKind %q != observability.Kind %q", got, want)
		}
	}
}

type countingSink struct{ n int }

func (c *countingSink) Emit(observability.ObsEvent) { c.n++ }

// The engine config's default ObservationSink is echo-only (never nil); an
// explicit sink overrides it.
func TestThresherConfigObservationSink(t *testing.T) {
	c := defaultConfig()
	if c.ObservationSink() == nil {
		t.Fatal("defaultConfig().ObservationSink() = nil, want echo-only")
	}

	cs := &countingSink{}
	c.obsSink = cs

	if got := c.ObservationSink(); got != cs {
		t.Errorf("ObservationSink() = %v, want the explicit sink %v", got, cs)
	}
}
