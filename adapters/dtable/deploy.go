package dtable

import (
	"context"
	"strconv"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/observability"
	"github.com/dr-dobermann/gobpm/pkg/rules"
)

// Decoder is the pluggable definition-format seam (ADR-029 §2.6): it
// translates a serialized decision artifact into an executable Table. The
// engine owns the deployment mechanics; the format lives here — the
// batteries JSONDecoder wires named Go functors, a future DMN-XML decoder
// plugs in without touching the engine.
type Decoder interface {
	Decode(definition []byte) (*Table, error)
}

// the engine implements the deploy half of the component contract.
var _ rules.Deployer = (*Engine)(nil)

// WithDecoder configures the definition decoder Deploy delegates to.
func WithDecoder(d Decoder) Option {
	return func(e *Engine) error {
		if d == nil {
			return errs.New(
				errs.M("WithDecoder: a nil Decoder isn't allowed"),
				errs.C(errorClass, errs.EmptyNotAllowed))
		}

		e.decoder = d

		return nil
	}
}

// Deploy ingests a serialized decision definition through the configured
// decoder and installs the resulting table under its decision name.
// Deployment is a lifecycle operation: a redeploy of an existing decision
// REPLACES its table (programmatic Register keeps rejecting duplicates —
// ADR-029 §2.6). The swap is a map-entry replacement under the write lock:
// an in-flight evaluation that already resolved the old table finishes on
// it.
func (e *Engine) Deploy(_ context.Context, definition []byte) error {
	if e.decoder == nil {
		return errs.New(
			errs.M("Deploy: no definition decoder is configured "+
				"(construct the engine with WithDecoder)"),
			errs.C(errorClass, errs.InvalidState))
	}

	if len(definition) == 0 {
		return errs.New(
			errs.M("Deploy: an empty definition isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	t, err := e.decoder.Decode(definition)
	if err != nil {
		return errs.New(
			errs.M("Deploy: definition decoding failed"),
			errs.C(errorClass, errs.OperationFailed),
			errs.E(err))
	}

	e.mu.Lock()
	_, replaced := e.tables[t.name]
	e.tables[t.name] = t
	// The deploy audit (SRD-069 FR-5): a definition landed on a LIVE
	// engine — the runtime governance milestone, flagged when it
	// overwrote a served decision.
	e.reportTable(observability.PhaseDeployed, t, map[string]string{
		"replaced": strconv.FormatBool(replaced),
	})
	e.mu.Unlock()

	return nil
}
