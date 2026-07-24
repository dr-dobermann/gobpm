package msgflow

import (
	"context"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/renv"
)

// MessageProducer is a node that sends a message — a SendTask or a throw
// message event (ADR-014 v.1 §2.2). Publish drives its choreography.
type MessageProducer interface {
	// MessageToSend returns the message the node publishes.
	MessageToSend() *bpmncommon.Message
}

// MessageConsumer is a node that waits for a message — a ReceiveTask or a catch
// message event (ADR-014 v.1 §2.2). It captures the arrived payload (via
// CaptureItem from ProcessEvent) and binds it into scope (via Bind from Exec).
type MessageConsumer interface {
	// ExpectedMessage returns the message the node waits for.
	ExpectedMessage() *bpmncommon.Message
}

// Publish binds the producer's message from scope and publishes it to the
// broker (the Send choreography, driven through the MessageProducer seam).
func Publish(
	ctx context.Context,
	re renv.RuntimeEnvironment,
	p MessageProducer,
) error {
	if p == nil {
		return errs.New(
			errs.M("msgflow.Publish: a nil MessageProducer isn't allowed"),
			errs.C(errorClass, errs.InvalidParameter))
	}

	// A producer may declare the CorrelationKey to stamp on the message
	// (ADR-016 v.1 §2.2); read it structurally so the seam needn't know the
	// concrete producer type.
	var key *bpmncommon.CorrelationKey
	if kp, ok := p.(interface {
		CorrelationKey() *bpmncommon.CorrelationKey
	}); ok {
		key = kp.CorrelationKey()
	}

	return Send(ctx, re, p.MessageToSend(), key)
}

// CaptureItem returns the payload item carried by a fired event definition, or
// nil when it carries none. A message consumer calls it from ProcessEvent to
// capture the arrived payload for binding on resume.
func CaptureItem(eDef flow.EventDefinition) *data.ItemDefinition {
	if eDef == nil {
		return nil
	}

	if items := eDef.GetItemsList(); len(items) != 0 {
		return items[0]
	}

	return nil
}

// Bind binds a captured message payload item into the execution scope as a
// Ready datum (re.Put); the node's UploadData then pushes it through the output
// associations. A nil item is a no-op (a payload-less trigger). A message
// consumer calls it from Exec.
func Bind(
	_ context.Context,
	re renv.RuntimeEnvironment,
	item *data.ItemDefinition,
) error {
	if re == nil {
		return errs.New(
			errs.M("msgflow.Bind: a nil RuntimeEnvironment isn't allowed"),
			errs.C(errorClass, errs.InvalidParameter))
	}

	if item == nil {
		return nil
	}

	iae, err := data.NewItemAwareElement(item, data.ReadyDataState)
	if err != nil {
		return errs.New(
			errs.M("msgflow.Bind: couldn't wrap payload item"),
			errs.C(errorClass, errs.OperationFailed),
			errs.E(err),
			errs.D("item_id", item.ID()))
	}

	res, err := data.NewParameter(item.ID(), iae)
	if err != nil {
		return errs.New(
			errs.M("msgflow.Bind: couldn't build payload datum"),
			errs.C(errorClass, errs.OperationFailed),
			errs.E(err),
			errs.D("item_id", item.ID()))
	}

	if err := re.Put(res); err != nil {
		return errs.New(
			errs.M("msgflow.Bind: couldn't bind message item %q into scope",
				item.ID()),
			errs.C(errorClass, errs.OperationFailed),
			errs.E(err))
	}

	return nil
}
