package convert_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/dr-dobermann/gobpm/pkg/convert"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
)

// plainImporter has no DocumentImporter capability — the fallback path.
type plainImporter struct {
	proc *process.Process
	err  error
}

func (p plainImporter) Import(context.Context, io.Reader) (*process.Process, error) {
	return p.proc, p.err
}

// reportingImporter has the capability.
type reportingImporter struct {
	res *convert.Result
	err error
}

func (r reportingImporter) Import(context.Context, io.Reader) (*process.Process, error) {
	return nil, errors.New("Import must not be called when the capability exists")
}

func (r reportingImporter) ImportDocument(
	context.Context, io.Reader,
) (*convert.Result, error) {
	return r.res, r.err
}

// registerImporter registers imp under a fresh format for one test.
func registerImporter(t *testing.T, name string, imp convert.Importer) convert.Format {
	t.Helper()

	f := convert.Format(name)
	if err := convert.RegisterImporter(f, imp); err != nil {
		t.Fatalf("RegisterImporter(%q): %v", f, err)
	}

	return f
}

// newProc builds a minimal process for a stub importer to return.
func newProc(t *testing.T, id string) *process.Process {
	t.Helper()

	p, err := process.New(id, foundation.WithID(id))
	if err != nil {
		t.Fatalf("process.New: %v", err)
	}

	return p
}

// TestImportDocumentFallsBack covers SRD-089.B §6 T-11: a format without
// the capability is not a failure — its single process comes back as a
// one-element Result.
func TestImportDocumentFallsBack(t *testing.T) {
	f := registerImporter(t, "test-plain", plainImporter{proc: newProc(t, "p1")})

	res, err := convert.ImportDocument(
		context.Background(), f, strings.NewReader("x"))
	if err != nil {
		t.Fatalf("ImportDocument: %v", err)
	}

	if len(res.Processes) != 1 || res.Processes[0].ID() != "p1" {
		t.Fatalf("Processes = %v, want the one process the importer returned",
			res.Processes)
	}

	// nil, not an empty slice. A caller asking "did this converter report?"
	// with `res.Dropped != nil` must not get a false yes from the fallback:
	// a format with no reporting cannot claim it dropped nothing.
	if res.Dropped != nil {
		t.Errorf("Dropped = %#v, want nil — the fallback reports nothing, "+
			"which is not the claim that nothing was dropped", res.Dropped)
	}
}

// TestImportDocumentUsesTheCapability covers the other half: when the
// importer implements it, its Result passes through whole.
func TestImportDocumentUsesTheCapability(t *testing.T) {
	want := &convert.Result{
		Processes: []*process.Process{newProc(t, "p2")},
		Dropped: []convert.Dropped{{
			Element:   "u1",
			Construct: "camunda:asyncBefore",
			Reason:    "the engine has no job executor",
		}},
	}

	f := registerImporter(t, "test-reporting", reportingImporter{res: want})

	res, err := convert.ImportDocument(
		context.Background(), f, strings.NewReader("x"))
	if err != nil {
		t.Fatalf("ImportDocument: %v", err)
	}

	if len(res.Dropped) != 1 || res.Dropped[0].Construct != "camunda:asyncBefore" {
		t.Fatalf("Dropped = %#v, want the importer's report", res.Dropped)
	}

	if res.Dropped[0].Reason == "" {
		t.Error("a dropped construct arrived with no reason")
	}
}

// TestImportDocumentRefusesAnEmptySuccess pins the converter-bug path.
// Success with nothing in it would surface as a nil dereference somewhere
// else entirely, so it is refused where it happens.
func TestImportDocumentRefusesAnEmptySuccess(t *testing.T) {
	cases := map[string]convert.Importer{
		"capability returns a nil Result":      reportingImporter{res: nil},
		"capability returns no processes":      reportingImporter{res: &convert.Result{}},
		"plain importer returns a nil process": plainImporter{},
	}

	i := 0

	for name, imp := range cases {
		i++

		t.Run(name, func(t *testing.T) {
			f := registerImporter(t, "test-empty-"+string(rune('a'+i)), imp)

			res, err := convert.ImportDocument(
				context.Background(), f, strings.NewReader("x"))
			if err == nil {
				t.Fatalf("ImportDocument = %v, want an error", res)
			}

			var ae *errs.ApplicationError
			if !errors.As(err, &ae) {
				t.Fatalf("error is %T, want *errs.ApplicationError", err)
			}

			if !strings.Contains(err.Error(), "no process and no error") {
				t.Errorf("error %q does not name the condition", err)
			}
		})
	}
}

// TestImportDocumentGuards covers the argument validation, which is shared
// with Import so the two entry points cannot drift into validating
// differently.
func TestImportDocumentGuards(t *testing.T) {
	f := registerImporter(t, "test-guards", plainImporter{proc: newProc(t, "p3")})

	tests := map[string]struct {
		ctx  context.Context //nolint:containedctx // the argument under test
		f    convert.Format
		r    io.Reader
		want string
	}{
		"nil ctx":        {nil, f, strings.NewReader("x"), "ctx is nil"},
		"empty format":   {context.Background(), "", strings.NewReader("x"), "f is empty"},
		"nil reader":     {context.Background(), f, nil, "r is nil"},
		"unknown format": {context.Background(), "test-nope", strings.NewReader("x"), "unknown"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := convert.ImportDocument(tc.ctx, tc.f, tc.r); err == nil ||
				!strings.Contains(strings.ToLower(err.Error()), tc.want) {
				t.Errorf("ImportDocument error = %v, want one mentioning %q", err, tc.want)
			}
		})
	}
}

// TestImportDocumentPropagatesImporterErrors covers the two paths where the
// importer itself fails. Both must reach the caller unchanged — wrapping a
// converter's own classified error here would bury the class a host
// branches on.
func TestImportDocumentPropagatesImporterErrors(t *testing.T) {
	boom := errors.New("the document is malformed")

	tests := map[string]convert.Importer{
		"through the capability": reportingImporter{err: boom},
		"through the fallback":   plainImporter{err: boom},
	}

	i := 0

	for name, imp := range tests {
		i++

		t.Run(name, func(t *testing.T) {
			f := registerImporter(t, "test-err-"+string(rune('a'+i)), imp)

			_, err := convert.ImportDocument(
				context.Background(), f, strings.NewReader("x"))

			if !errors.Is(err, boom) {
				t.Errorf("ImportDocument error = %v, want the importer's own error "+
					"unwrapped — a host branches on its class", err)
			}
		})
	}
}
