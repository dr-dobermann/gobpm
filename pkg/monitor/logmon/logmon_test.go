package logmon_test

import (
	"bytes"
	"io"
	"log/slog"
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
		At:     time.Date(1973, 02, 23, 05, 15, 0, 0, time.Local),
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
		name           string
		handlerBuilder func(io.Writer, *slog.HandlerOptions) slog.Handler
	}{
		{
			name: "text logger",
			handlerBuilder: func(
				w io.Writer,
				opts *slog.HandlerOptions,
			) slog.Handler {
				return slog.NewTextHandler(w, opts)
			},
		},
		{

			name: "JSON logger",
			handlerBuilder: func(
				w io.Writer, opts *slog.HandlerOptions) slog.Handler {
				return slog.NewJSONHandler(w, opts)
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
							Level: slog.LevelDebug}))

				logBuf := bytes.NewBuffer([]byte{})
				logger := slog.New(
					tst.handlerBuilder(
						testBuf,
						&slog.HandlerOptions{
							Level: slog.LevelDebug}))

				lm, err := logmon.New(logger)
				require.NoError(t, err)

				testLogger.Info("MONITORING", "event", &event)
				lm.Write(&event)

				t.Log(string(testBuf.Bytes()))

				require.Equal(t,
					string(testBuf.Bytes()),
					string(logBuf.Bytes()))

			})
	}
}
