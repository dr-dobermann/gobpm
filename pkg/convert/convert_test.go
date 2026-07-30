package convert

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/model/process"
)

type stubImporter struct{ p *process.Process }

func (s stubImporter) Import(_ context.Context, _ io.Reader) (*process.Process, error) {
	return s.p, nil
}

type stubExporter struct{ called bool }

func (s *stubExporter) Export(_ context.Context, _ io.Writer, _ *process.Process) error {
	s.called = true

	return nil
}

// TestConvertRegistry covers SRD-051 §FR-2: register/lookup happy path,
// duplicate/nil/empty rejection, the enumerating unknown-format error and
// Formats() listing.
func TestConvertRegistry(t *testing.T) {
	ctx := context.Background()

	t.Run("register and use happy path", func(t *testing.T) {
		want := &process.Process{}

		if err := RegisterImporter("t-happy", stubImporter{p: want}); err != nil {
			t.Fatalf("RegisterImporter: %v", err)
		}

		exp := &stubExporter{}

		if err := RegisterExporter("t-happy", exp); err != nil {
			t.Fatalf("RegisterExporter: %v", err)
		}

		got, err := Import(ctx, "t-happy", strings.NewReader("x"))
		if err != nil {
			t.Fatalf("Import: %v", err)
		}

		if got != want {
			t.Errorf("Import returned %p, want %p", got, want)
		}

		if err := Export(ctx, "t-happy", io.Discard, want); err != nil {
			t.Fatalf("Export: %v", err)
		}

		if !exp.called {
			t.Error("registered exporter was not called")
		}
	})

	t.Run("duplicate registration rejected", func(t *testing.T) {
		if err := RegisterImporter("t-dup", stubImporter{}); err != nil {
			t.Fatalf("first RegisterImporter: %v", err)
		}

		err := RegisterImporter("t-dup", stubImporter{})
		if err == nil {
			t.Fatal("duplicate RegisterImporter: want error, got nil")
		}

		if !strings.Contains(err.Error(), "RegisterImporter") ||
			!strings.Contains(err.Error(), `"t-dup"`) {
			t.Errorf("error is not self-identifying: %v", err)
		}

		if err := RegisterExporter("t-dup", &stubExporter{}); err != nil {
			t.Fatalf("first RegisterExporter: %v", err)
		}

		if err := RegisterExporter("t-dup", &stubExporter{}); err == nil {
			t.Fatal("duplicate RegisterExporter: want error, got nil")
		}
	})

	t.Run("nil implementation rejected", func(t *testing.T) {
		if err := RegisterImporter("t-nil", nil); err == nil ||
			!strings.Contains(err.Error(), "imp is nil") {
			t.Errorf("RegisterImporter(nil): %v", err)
		}

		if err := RegisterExporter("t-nil", nil); err == nil ||
			!strings.Contains(err.Error(), "exp is nil") {
			t.Errorf("RegisterExporter(nil): %v", err)
		}
	})

	t.Run("empty format rejected", func(t *testing.T) {
		if err := RegisterImporter("", stubImporter{}); err == nil ||
			!strings.Contains(err.Error(), "f is empty") {
			t.Errorf("RegisterImporter empty format: %v", err)
		}

		if err := RegisterExporter("", &stubExporter{}); err == nil {
			t.Error("RegisterExporter empty format: want error, got nil")
		}

		if _, err := Import(ctx, "", strings.NewReader("x")); err == nil {
			t.Error("Import empty format: want error, got nil")
		}

		if err := Export(ctx, "", io.Discard, &process.Process{}); err == nil {
			t.Error("Export empty format: want error, got nil")
		}
	})

	t.Run("nil arguments rejected", func(t *testing.T) {
		var nilCtx context.Context
		if _, err := Import(nilCtx, "t-nil-args", strings.NewReader("x")); err == nil ||
			!strings.Contains(err.Error(), "ctx is nil") {
			t.Errorf("Import(nil ctx): %v", err)
		}

		if _, err := Import(ctx, "t-nil-args", nil); err == nil ||
			!strings.Contains(err.Error(), "r is nil") {
			t.Errorf("Import(nil reader): %v", err)
		}

		if err := Export(nilCtx, "t-nil-args", io.Discard, &process.Process{}); err == nil ||
			!strings.Contains(err.Error(), "ctx is nil") {
			t.Errorf("Export(nil ctx): %v", err)
		}

		if err := Export(ctx, "t-nil-args", nil, &process.Process{}); err == nil ||
			!strings.Contains(err.Error(), "w is nil") {
			t.Errorf("Export(nil writer): %v", err)
		}

		if err := Export(ctx, "t-nil-args", io.Discard, nil); err == nil ||
			!strings.Contains(err.Error(), "p is nil") {
			t.Errorf("Export(nil process): %v", err)
		}
	})

	t.Run("unknown format enumerates registered", func(t *testing.T) {
		if err := RegisterImporter("t-enum", stubImporter{}); err != nil {
			t.Fatalf("RegisterImporter: %v", err)
		}

		_, err := Import(ctx, "t-unknown", strings.NewReader("x"))
		if err == nil {
			t.Fatal("Import unknown format: want error, got nil")
		}

		if !strings.Contains(err.Error(), `unknown format "t-unknown"`) ||
			!strings.Contains(err.Error(), "t-enum") {
			t.Errorf("error does not enumerate registered formats: %v", err)
		}

		if err := Export(ctx, "t-unknown", io.Discard, &process.Process{}); err == nil ||
			!strings.Contains(err.Error(), `unknown format "t-unknown"`) {
			t.Errorf("Export unknown format: %v", err)
		}
	})

	t.Run("Formats lists registered", func(t *testing.T) {
		if err := RegisterImporter("t-formats", stubImporter{}); err != nil {
			t.Fatalf("RegisterImporter: %v", err)
		}

		found := false

		for _, f := range Formats() {
			if f == "t-formats" {
				found = true
			}
		}

		if !found {
			t.Errorf("Formats() = %v, want it to contain %q", Formats(), "t-formats")
		}
	})

	t.Run("Must variants panic on error", func(t *testing.T) {
		MustRegisterImporter("t-must", stubImporter{})
		MustRegisterExporter("t-must", &stubExporter{})

		defer func() {
			if recover() == nil {
				t.Error("MustRegisterImporter duplicate: want panic, got none")
			}
		}()

		MustRegisterImporter("t-must", stubImporter{})
	})

	t.Run("Must exporter panics on error", func(t *testing.T) {
		MustRegisterExporter("t-must-exporter", &stubExporter{})

		defer func() {
			if recover() == nil {
				t.Error("MustRegisterExporter duplicate: want panic, got none")
			}
		}()

		MustRegisterExporter("t-must-exporter", &stubExporter{})
	})
}

// TestUnsupportedElementError covers the SRD-051 §FR-3 diagnostic shape.
func TestUnsupportedElementError(t *testing.T) {
	tests := []struct {
		err  UnsupportedElementError
		want string
	}{
		{err: UnsupportedElementError{Tag: "custom"}, want: `unsupported element "custom"`},
		{err: UnsupportedElementError{Tag: "task", ID: "t1"}, want: `(id "t1")`},
		{err: UnsupportedElementError{Tag: "lane", Section: "§10.5"}, want: `spec §10.5`},
	}

	for _, tc := range tests {
		if got := tc.err.Error(); !strings.Contains(got, tc.want) {
			t.Errorf("Error() = %q, want substring %q", got, tc.want)
		}
	}
}
