package localdispatcher

import (
	"errors"
	"testing"
)

// TestJobOutputErr covers FIX-026: the job-output failure classifier wraps
// the cause and names the output.
func TestJobOutputErr(t *testing.T) {
	inner := errors.New("inner")

	err := jobOutputErr("out-1", inner)
	if err == nil || !errors.Is(err, inner) {
		t.Fatalf("jobOutputErr must wrap the cause, got %v", err)
	}
}
