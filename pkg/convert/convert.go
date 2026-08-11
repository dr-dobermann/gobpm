package convert

import (
	"context"
	"errors"
	"io"
	"maps"
	"reflect"
	"slices"
	"strings"
	"sync"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
)

const errorClass = "CONVERT_ERRORS"

// Format identifies a serialization format supported by the registry.
//
// Implements SRD-051 §FR-1.
type Format string

// BPMN is the format key of the BPMN 2.0 XML converter.
const BPMN Format = "bpmn"

// Importer deserializes a process definition from r.
//
// Implementations must preserve the source ids as foundation identity and
// report elements outside their supported subset with
// *UnsupportedElementError.
type Importer interface {
	Import(ctx context.Context, r io.Reader) (*process.Process, error)
}

// Exporter serializes p into w.
type Exporter interface {
	Export(ctx context.Context, w io.Writer, p *process.Process) error
}

// Registry state is package-global, keyed by Format and mutated only by the
// Register... functions (typically from a converter package's init) — the
// image.RegisterFormat idiom (ADR-024 §2.2), a deliberate deviation from the
// functional-options norm because convert is engine-independent.
var (
	mu        sync.RWMutex
	importers = map[Format]Importer{}
	exporters = map[Format]Exporter{}

	// initFailures records registrations that failed inside a converter
	// package's init(), which has no caller to return an error to. Import and
	// Export return the recorded failure for that format, so a botched
	// self-registration surfaces as a classified error at first use instead of
	// killing the program at load time.
	initFailures = map[Format]error{}
)

// RegisterImporter registers imp as the Importer for format f.
//
// Every argument is validated on entry: an empty f, a nil imp or a duplicate
// (f, import) registration yield a self-identifying error (SRD-051 §FR-2).
func RegisterImporter(f Format, imp Importer) error {
	return register(importers, "convert.RegisterImporter", "imp", "importer", f, imp)
}

// RegisterExporter registers exp as the Exporter for format f.
//
// Every argument is validated on entry: an empty f, a nil exp or a duplicate
// (f, export) registration yield a self-identifying error (SRD-051 §FR-2).
func RegisterExporter(f Format, exp Exporter) error {
	return register(exporters, "convert.RegisterExporter", "exp", "exporter", f, exp)
}

// register stores v in m under f after validating every argument on entry
// (SRD-051 §FR-2). fn is the public function's name, arg its parameter name
// and kind the direction noun — they keep the errors self-identifying while
// both Register... functions share one implementation.
func register[T any](m map[Format]T, fn, arg, kind string, f Format, v T) error {
	if f == "" {
		return errs.New(
			errs.M("%s: f is empty", fn),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if isNil(v) {
		return errs.New(
			errs.M("%s: %s is nil", fn, arg),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	mu.Lock()
	defer mu.Unlock()

	if _, dup := m[f]; dup {
		return errs.New(
			errs.M("%s: %s for format %q is already registered", fn, kind, f),
			errs.C(errorClass, errs.DuplicateObject))
	}

	m[f] = v

	return nil
}

// isNil reports both a nil interface and an interface containing a typed-nil
// implementation. The latter would otherwise be accepted by the registry and
// could panic when its Import or Export method is called.
func isNil(v any) bool {
	if v == nil {
		return true
	}

	rv := reflect.ValueOf(v)

	switch rv.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map,
		reflect.Pointer, reflect.Slice:
		return rv.IsNil()
	default:
		return false
	}
}

// RegisterImporterAtInit registers imp for f from a converter package's init(),
// the blank-import self-registration idiom (ADR-024 §2.2).
//
// An init() has no caller to return an error to, so a failure is recorded
// against f and returned by Import and Export for that format. The only
// reachable failure is a duplicate or malformed registration — a programming
// error — and surfacing it as a classified error at first use is strictly
// better than panicking at load time: an embedder that never touches f is not
// killed by a converter it does not use, and one that does gets the cause
// through the ordinary error path instead of a stack trace.
func RegisterImporterAtInit(f Format, imp Importer) {
	if err := RegisterImporter(f, imp); err != nil {
		recordInitFailure(f, err)
	}
}

// RegisterExporterAtInit registers exp for f from a converter package's init().
// It records a failure exactly as RegisterImporterAtInit does.
func RegisterExporterAtInit(f Format, exp Exporter) {
	if err := RegisterExporter(f, exp); err != nil {
		recordInitFailure(f, err)
	}
}

// recordInitFailure stores err against f so Import and Export can return it.
func recordInitFailure(f Format, err error) {
	mu.Lock()
	defer mu.Unlock()

	initFailures[f] = errors.Join(initFailures[f], err)
}

// initFailure returns the registration failure recorded for f, or nil.
func initFailure(f Format) error {
	mu.RLock()
	defer mu.RUnlock()

	return initFailures[f]
}

// Import deserializes a process of format f from r using the registered
// Importer. Importing an unregistered format returns an error enumerating
// Formats() (SRD-051 §FR-2).
func Import(ctx context.Context, f Format, r io.Reader) (*process.Process, error) {
	// The same guards ImportDocument applies, in the same order and with
	// the same messages — shared so the two entry points cannot drift into
	// validating differently.
	if err := checkImportArgs(ctx, "convert.Import", f, r); err != nil {
		return nil, err
	}

	mu.RLock()
	imp, ok := importers[f]
	mu.RUnlock()

	if !ok {
		return nil, unknownFormatError("convert.Import", f)
	}

	return imp.Import(ctx, r)
}

// Export serializes p into w as format f using the registered Exporter.
// Exporting an unregistered format returns an error enumerating Formats()
// (SRD-051 §FR-2).
func Export(ctx context.Context, f Format, w io.Writer, p *process.Process) error {
	if ctx == nil {
		return errs.New(
			errs.M("convert.Export: ctx is nil"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if f == "" {
		return errs.New(
			errs.M("convert.Export: f is empty"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if w == nil {
		return errs.New(
			errs.M("convert.Export: w is nil"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if p == nil {
		return errs.New(
			errs.M("convert.Export: p is nil"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if err := initFailure(f); err != nil {
		return errs.New(
			errs.M("convert.Export: format %q failed to self-register", f),
			errs.C(errorClass, errs.InvalidParameter),
			errs.E(err))
	}

	mu.RLock()
	exp, ok := exporters[f]
	mu.RUnlock()

	if !ok {
		return unknownFormatError("convert.Export", f)
	}

	return exp.Export(ctx, w, p)
}

// Formats returns the sorted list of formats with a registered Importer or
// Exporter (SRD-051 §FR-2).
func Formats() []Format {
	mu.RLock()
	defer mu.RUnlock()

	seen := make(map[Format]struct{}, len(importers)+len(exporters))
	for f := range importers {
		seen[f] = struct{}{}
	}

	for f := range exporters {
		seen[f] = struct{}{}
	}

	return slices.Sorted(maps.Keys(seen))
}

// unknownFormatError builds the "unknown format %q (registered: ...)" error
// required by SRD-051 §FR-2.
func unknownFormatError(fn string, f Format) error {
	registered := "-"

	if ff := Formats(); len(ff) > 0 {
		ss := make([]string, len(ff))
		for i, f := range ff {
			ss[i] = string(f)
		}

		registered = strings.Join(ss, ", ")
	}

	return errs.New(
		errs.M("%s: unknown format %q (registered: %s)", fn, f, registered),
		errs.C(errorClass, errs.ObjectNotFound))
}
