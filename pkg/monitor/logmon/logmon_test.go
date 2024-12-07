package logmon_test

import (
	"bytes"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/monitor"
	"github.com/dr-dobermann/gobpm/pkg/monitor/logmon"
	"github.com/stretchr/testify/require"
)

func TestLogMon(t *testing.T) {
	event := monitor.Event{
		Source: "calendar",
		Type:   "birth_date",
		At:     time.Date(1973, time.February, 23, 5, 15, 0, 0, time.UTC),
		Details: map[string]any{
			"name": "dr-dobermann",
		},
	}

	t.Run("invlaid logger",
		func(t *testing.T) {
			_, err := logmon.New(nil)
			require.Error(t, err)
		})

	logTests := []struct {
		// test name
		name string

		// build log handler
		handlerBuilder func(io.Writer, *slog.HandlerOptions) slog.Handler

		// preparation of test values
		testGen func(l *slog.Logger)
	}{
		{
			name: "text logger",
			handlerBuilder: func(
				w io.Writer,
				opts *slog.HandlerOptions,
			) slog.Handler {
				return slog.NewTextHandler(w, opts)
			},

			testGen: func(l *slog.Logger) {
				l.Info("MONITORING",
					"event.Source", event.Source,
					"event.Type", event.Type,
					"event.At", event.At,
					"event.Details.name", event.Details["name"])
			},
		},
		{
			name: "JSON logger",
			handlerBuilder: func(
				w io.Writer,
				opts *slog.HandlerOptions,
			) slog.Handler {
				return slog.NewJSONHandler(w, opts)
			},
			testGen: func(l *slog.Logger) {
				l.Info("MONITORING", "event", event)
			},
		},
	}

	for _, tst := range logTests {
		t.Run(tst.name,
			func(t *testing.T) {
				testBuf := bytes.NewBuffer([]byte{})
				testLogger := slog.New(
					tst.handlerBuilder(
						testBuf,
						&slog.HandlerOptions{
							Level: slog.LevelDebug,
						}))

				logBuf := bytes.NewBuffer([]byte{})
				logger := slog.New(
					tst.handlerBuilder(
						logBuf,
						&slog.HandlerOptions{
							Level: slog.LevelDebug,
						}))

				lm, err := logmon.New(logger)
				require.NoError(t, err)

				tst.testGen(testLogger)
				lm.Write(&event)

				// t.Log(testBuf.String())
				// t.Log(logBuf.String())

				require.Equal(t,
					// omit event time in comparison
					testBuf.String()[strings.Index(testBuf.String(), "level"):],
					logBuf.String()[strings.Index(logBuf.String(), "level"):])
			})
	}
}
