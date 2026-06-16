package msgflow

import (
	"context"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/messaging"
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/renv"
)

// Send binds msg's item from the execution scope (service.BindInput) and
// publishes it to the runtime's MessageBroker as an Envelope keyed by the
// message name (ADR-014 v.1 §2.6). When key is non-nil, Send derives the
// composite correlation key from the payload (ADR-016 v.1 §2.2) and stamps it
// onto the Envelope so a keyed consumer can correlate; an underivable key is
// left empty (name-match only). A message that carries no item is published
// with a nil payload. Send is the producer choreography shared by SendTask and
// the throw message event; it names the BPMN intent and hides the broker hop.
func Send(
	ctx context.Context,
	re renv.RuntimeEnvironment,
	msg *bpmncommon.Message,
	key *bpmncommon.CorrelationKey,
) error {
	if re == nil {
		return errs.New(
			errs.M("msgflow.Send: a nil RuntimeEnvironment isn't allowed"),
			errs.C(errorClass, errs.InvalidParameter))
	}

	if msg == nil {
		return errs.New(
			errs.M("msgflow.Send: a nil Message isn't allowed"),
			errs.C(errorClass, errs.InvalidParameter))
	}

	item, err := service.BindInput(ctx, re, msg)
	if err != nil {
		return errs.New(
			errs.M("msgflow.Send: couldn't bind message %q", msg.Name()),
			errs.C(errorClass, errs.OperationFailed),
			errs.E(err))
	}

	var payload any
	if item != nil {
		payload = item.Structure().Get(ctx)
	}

	var corrKey string
	if key != nil {
		// An underivable key (ok=false) stays empty — name-match only — rather
		// than failing the send.
		if k, ok, derr := DeriveKey(
			ctx, re.ExpressionEngine(), key, msg, payload); derr != nil {
			return errs.New(
				errs.M("msgflow.Send: correlation key derivation failed for %q",
					msg.Name()),
				errs.C(errorClass, errs.OperationFailed),
				errs.E(derr))
		} else if ok {
			corrKey = k
		}
	}

	if err := re.MessageBroker().Publish(ctx, messaging.Envelope{
		Name:           msg.Name(),
		Payload:        payload,
		CorrelationKey: corrKey,
	}); err != nil {
		return errs.New(
			errs.M("msgflow.Send: broker rejected message %q", msg.Name()),
			errs.C(errorClass, errs.OperationFailed),
			errs.E(err))
	}

	return nil
}
