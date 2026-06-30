package msgflow

import (
	"context"
	"fmt"
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/expression"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
)

// keySeparator joins the partial keys of a composite CorrelationKey into the
// single string carried by Envelope.CorrelationKey. It is opaque to the broker
// (which only compares keys for equality), so any value that can't appear
// inside a partial key works.
const keySeparator = "\x1f"

// payloadSource exposes a message payload as a data.Source so a
// CorrelationPropertyRetrievalExpression.MessagePath can read it. The payload is
// reconstructed as a single Ready datum keyed by the message item's id (the same
// shape Send publishes and the MessageWaiter rebuilds on fire), addressable by
// that id.
type payloadSource struct {
	datum data.Data
	name  string
}

// Find returns the payload datum when name addresses it.
func (s payloadSource) Find(_ context.Context, name string) (data.Data, error) {
	if name == s.name {
		return s.datum, nil
	}

	return nil, errs.New(
		errs.M("correlation payload source: %q isn't found", name),
		errs.C(errorClass, errs.ObjectNotFound),
		errs.D("requested", name),
		errs.D("available", s.name))
}

// DeriveKey composes the composite correlation key for msg's payload from key's
// CorrelationProperties (SRD-015 §4.5, BPMN §8.4.2): each property's retrieval
// expression for msg is evaluated over a payload-backed source and the results
// are joined. ok is false when the key cannot be fully populated — a property
// has no retrieval expression for msg, or one yields no value — because an
// unpopulated composite key must never match (all partial keys are required).
// No payload values are logged (NFR-1).
func DeriveKey(
	ctx context.Context,
	eng expression.Engine,
	key *bpmncommon.CorrelationKey,
	msg *bpmncommon.Message,
	payload any,
) (string, bool, error) {
	if eng == nil || key == nil || msg == nil {
		return "", false, errs.New(
			errs.M("DeriveKey: engine, key and message are all required"),
			errs.C(errorClass, errs.InvalidParameter))
	}

	item := msg.Item()
	if item == nil {
		return "", false, errs.New(
			errs.M("DeriveKey: message %q has no item to read", msg.Name()),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	src := payloadSource{
		name: item.ID(),
		datum: data.MustParameter(item.ID(),
			data.MustItemAwareElement(
				data.MustItemDefinition(values.NewVariable(payload),
					foundation.WithID(item.ID())),
				data.ReadyDataState)),
	}

	parts := make([]string, 0, len(key.Properties))
	for i := range key.Properties {
		expr := retrievalExprFor(key.Properties[i], msg)
		if expr == nil {
			return "", false, nil
		}

		val, err := eng.Evaluate(ctx, expr.MessagePath, src)
		if err != nil {
			return "", false, errs.New(
				errs.M("DeriveKey: property %q evaluation failed",
					key.Properties[i].Name),
				errs.C(errorClass, errs.OperationFailed),
				errs.E(err))
		}

		if val == nil {
			return "", false, nil
		}

		// A present Value may still carry no payload (an unset optional field);
		// an absent payload can't contribute a key part, so correlation fails
		// (ok=false) rather than stamping a "<nil>" part (ADR-016 v.1).
		raw := val.Get(ctx)
		if raw == nil {
			return "", false, nil
		}

		parts = append(parts, fmt.Sprintf("%v", raw))
	}

	return strings.Join(parts, keySeparator), true, nil
}

// retrievalExprFor returns prop's retrieval expression whose MessageRef matches
// msg (by name), or nil when prop declares none for msg.
func retrievalExprFor(
	prop bpmncommon.CorrelationProperty,
	msg *bpmncommon.Message,
) *bpmncommon.CorrelationPropertyRetrievalExpression {
	for i := range prop.Expressions {
		e := &prop.Expressions[i]
		if e.MessageRef != nil && e.MessageRef.Name() == msg.Name() {
			return e
		}
	}

	return nil
}
