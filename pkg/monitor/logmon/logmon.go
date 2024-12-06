package logmon

import (
	"log/slog"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/monitor"
)

const errorClass = "MONITORING_ERRORS"

type (
	lMon struct {
		log *slog.Logger
	}

	evt struct {
		monitor.Event
	}
)

func New(l *slog.Logger) (monitor.Writer, error) {
	if l == nil {
		return nil,
			errs.New(
				errs.M("empty logger for monitor"),
				errs.C(errorClass, errs.EmptyNotAllowed))
	}

	return &lMon{
		log: l,
	}, nil
}

// ------------- slog.LogValuer interface --------------------------------------

func (e *evt) LogValue() slog.Value {
	details := []slog.Attr{}

	if e.Source != "" {
		details = append(details, slog.String("Source", e.Source))
	}

	if e.Type != "" {
		details = append(details, slog.String("Type", e.Type))
	}

	details = append(details, slog.Time("At", e.At))

	dd := []slog.Attr{}
	for n, v := range e.Details {
		dd = append(dd, slog.Any(n, v))
	}
	details = append(details, slog.Any("Details", dd))

	return slog.GroupValue(details...)
}

// ---------------------- monitor.Writer interface -----------------------------

func (lm *lMon) Write(e *monitor.Event) {
	lm.log.Info("MONITORING", "event", &evt{
		Event: *e,
	})
}

//------------------------------------------------------------------------------
