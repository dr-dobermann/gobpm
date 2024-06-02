package logmon

import (
	"log/slog"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/monitor"
)

const errorClass = "MONITORING_ERRORS"

type lMon struct {
	log *slog.Logger
}

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

// ---------------------- monitor.Writer interface -----------------------------

func (lm *lMon) Write(e *monitor.Event) {
	lm.log.Info("MONITORING", "event", e)
}

//------------------------------------------------------------------------------
