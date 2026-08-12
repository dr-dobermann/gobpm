package convert

import (
	"context"
	"io"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
)

// Dropped names one construct a converter recognized and deliberately did
// not map.
//
// It exists because "not mapped" and "not present" must not look alike to
// the host. A converter that understands a vendor dialect but has no model
// element for one of its constructs would otherwise return a definition
// that looks complete and quietly is not — the file's assignee, its retry
// policy or its listener simply gone (ADR-024 §2.14).
//
// Only a RECOGNIZED construct is reported. An unknown namespace stays
// silent: a converter cannot report on a vocabulary it does not know, and
// pretending otherwise would turn every foreign annotation into noise.
type Dropped struct {
	// Element is the owning element's id, where the source has one.
	Element string

	// Construct is what was dropped, in the source's own vocabulary —
	// "camunda:asyncBefore", not a paraphrase — so it can be grepped for
	// in the file it came from.
	Construct string

	// Reason says why, in terms a modeler can act on.
	Reason string
}

// Result is everything one source document yielded.
type Result struct {
	// Processes are the definitions the document carried, in document
	// order. Always at least one.
	Processes []*process.Process

	// Dropped names each recognized construct the converter did not map,
	// or is nil when the converter dropped nothing — or reports nothing.
	// The two are deliberately indistinguishable: a format with no
	// reporting cannot claim it dropped nothing.
	Dropped []Dropped
}

// DocumentImporter is the optional capability of an Importer whose format
// can carry more than one definition per document, or which can knowingly
// drop content and wants to say so.
//
// It is a capability rather than a widening of Importer so that no existing
// format has to implement it, and so Import keeps its meaning — the one
// process of the document. ImportDocument probes for it and falls back.
type DocumentImporter interface {
	ImportDocument(ctx context.Context, r io.Reader) (*Result, error)
}

// ImportDocument deserializes everything the source document holds, using
// the registered Importer's DocumentImporter capability when it has one.
//
// A format without the capability is not a failure: its single process is
// returned as a one-element Result with no report, which is what "this
// converter does not report" honestly looks like.
func ImportDocument(ctx context.Context, f Format, r io.Reader) (*Result, error) {
	const fn = "convert.ImportDocument"

	if err := checkImportArgs(ctx, fn, f, r); err != nil {
		return nil, err
	}

	mu.RLock()
	imp, ok := importers[f]
	mu.RUnlock()

	if !ok {
		return nil, unknownFormatError(fn, f)
	}

	di, hasCapability := imp.(DocumentImporter)
	if !hasCapability {
		return fallbackResult(ctx, fn, imp, r)
	}

	res, err := di.ImportDocument(ctx, r)
	if err != nil {
		return nil, err
	}

	// A capability that returns success with nothing in it is a converter
	// bug, and handing it to the caller would surface as a nil dereference
	// somewhere else entirely.
	if res == nil || len(res.Processes) == 0 {
		return nil, errs.New(
			errs.M("%s: format %q returned no process and no error", fn, f),
			errs.C(errorClass, errs.InvalidObject))
	}

	return res, nil
}

// fallbackResult wraps a plain Import for a format without the capability.
func fallbackResult(
	ctx context.Context, fn string, imp Importer, r io.Reader,
) (*Result, error) {
	p, err := imp.Import(ctx, r)
	if err != nil {
		return nil, err
	}

	if p == nil {
		return nil, errs.New(
			errs.M("%s: importer returned no process and no error", fn),
			errs.C(errorClass, errs.InvalidObject))
	}

	// Dropped stays nil: this format reported nothing, which is not the
	// same claim as "nothing was dropped".
	return &Result{Processes: []*process.Process{p}}, nil
}

// checkImportArgs validates the arguments every import entry point takes,
// naming the caller so the error identifies which one refused.
func checkImportArgs(ctx context.Context, fn string, f Format, r io.Reader) error {
	if ctx == nil {
		return errs.New(
			errs.M("%s: ctx is nil", fn),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if f == "" {
		return errs.New(
			errs.M("%s: f is empty", fn),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if r == nil {
		return errs.New(
			errs.M("%s: r is nil", fn),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if err := initFailure(f); err != nil {
		return errs.New(
			errs.M("%s: format %q failed to self-register", fn, f),
			errs.C(errorClass, errs.InvalidParameter),
			errs.E(err))
	}

	return nil
}
