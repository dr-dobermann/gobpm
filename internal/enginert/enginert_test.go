package enginert

import (
	"log/slog"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/clock/clocktest"
	"github.com/dr-dobermann/gobpm/pkg/model/expression/goexpr"
)

func TestDefaultPopulatesEveryExtension(t *testing.T) {
	r := Default()

	if r.Logger() == nil || r.Tracer() == nil || r.MetricsRecorder() == nil ||
		r.Clock() == nil || r.Repository() == nil || r.MessageBroker() == nil ||
		r.ExpressionEngine() == nil || r.AuthorizationProvider() == nil ||
		r.WorkerDispatcher() == nil {
		t.Fatal("Default left an extension nil")
	}
}

func TestOverrides(t *testing.T) {
	c := clocktest.New(time.Unix(0, 0))
	e := goexpr.New()

	l := slog.Default()

	r := Default().WithClock(c).WithExpressionEngine(e).WithLogger(l)

	if r.Clock() != c {
		t.Fatal("WithClock was not applied")
	}

	if r.ExpressionEngine() != e {
		t.Fatal("WithExpressionEngine was not applied")
	}

	if r.Logger() != l {
		t.Fatal("WithLogger was not applied")
	}
}
