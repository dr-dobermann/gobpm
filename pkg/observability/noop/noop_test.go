package noop

import (
	"context"
	"errors"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/observability"
)

func TestTracerIsInert(t *testing.T) {
	tr := NewTracer()

	ctx, sp := tr.Start(context.Background(), "op", observability.Attr{Key: "k", Value: 1})
	if ctx == nil {
		t.Fatal("Start returned a nil context")
	}

	// None of these must panic.
	sp.SetAttributes(observability.Attr{Key: "a", Value: "b"})
	sp.RecordError(errors.New("boom"))
	sp.SetStatus(observability.StatusError, "failed")
	sp.End()
}

func TestMetricsAreInert(t *testing.T) {
	m := NewMetricsRecorder()
	ctx := context.Background()

	m.Counter("c").Add(ctx, 1, observability.Attr{Key: "k", Value: 1})
	m.Histogram("h").Record(ctx, 2.5)
	m.Gauge("g").Set(ctx, 3)
}
