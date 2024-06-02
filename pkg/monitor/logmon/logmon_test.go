package logmon_test

import (
	"bytes"
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

	t.Run("text logger",
		func(t *testing.T) {
			testBuf := bytes.NewBuffer([]byte{})
			testLogger := slog.New(
				slog.NewTextHandler(
					testBuf,
					&slog.HandlerOptions{
						Level: slog.LevelDebug,
					}))

			logBuf := bytes.NewBuffer([]byte{})
			logger := slog.New(
				slog.NewTextHandler(logBuf, &slog.HandlerOptions{
					Level: slog.LevelDebug,
				}))

			lm, err := logmon.New(logger)
			require.NoError(t, err)

			testLogger.Info("MONITORING", "event", &event)
			lm.Write(&event)

			t.Log(string(testBuf.Bytes()))

			require.Equal(t, string(testBuf.Bytes()), string(logBuf.Bytes()))
		})

	t.Run("JSON logger",
		func(t *testing.T) {
			testBuf := bytes.NewBuffer([]byte{})
			testLogger := slog.New(
				slog.NewJSONHandler(
					testBuf,
					&slog.HandlerOptions{
						Level: slog.LevelDebug,
					}))

			logBuf := bytes.NewBuffer([]byte{})
			logger := slog.New(
				slog.NewJSONHandler(logBuf, &slog.HandlerOptions{
					Level: slog.LevelDebug,
				}))

			lm, err := logmon.New(logger)
			require.NoError(t, err)

			testLogger.Info("MONITORING", "event", &event)
			lm.Write(&event)

			t.Log(string(testBuf.Bytes()))

			require.Equal(t, string(testBuf.Bytes()), string(logBuf.Bytes()))
		})
}
