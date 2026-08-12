// Package convert is the format-agnostic converter seam: it defines the
// Importer/Exporter interfaces over io.Reader/io.Writer that produce and
// consume *process.Process, plus a register-by-format-key registry in the
// image.RegisterFormat idiom.
//
// The package is engine-independent and holds no serialization logic of its
// own: concrete converters (e.g. the BPMN 2.0 converter in the sibling
// package convert/bpmn) self-register from their init() functions, so a blank
// import turns a format on:
//
//	import (
//		"github.com/dr-dobermann/gobpm/pkg/convert"
//		_ "github.com/dr-dobermann/gobpm/pkg/convert/bpmn"
//	)
//
//	p, err := convert.Import(ctx, convert.BPMN, r)
//	err = convert.Export(ctx, convert.BPMN, w, p)
//
// Implements SRD-051 §FR-1..§FR-3 (ADR-024 §2.1–§2.3).
package convert
