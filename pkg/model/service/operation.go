package service

import (
	"context"
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/dr-dobermann/gobpm/pkg/set"
)

// UnspecifiedImplementation represents an unspecified implementation type.
const UnspecifiedImplementation = "##unspecified"

// Implementor interface runs an Operation and returns its result
type Implementor interface {
	// Type returns type of the executor.
	Type() string

	// ErrorClasses returns errors classes list which may be
	// returned by the Execute call.
	//
	// Operation already has ObjectNotFound, EmptyNotAllowed and
	// OperationFailed error classes.
	ErrorClasses() []string

	// Execute runs an operation implementator with in parameter and
	// returns the output result (couldn be nil) and error status.
	Execute(
		ctx context.Context,
		in *data.ItemDefinition,
	) (*data.ItemDefinition, error)
}

// An Operation is a ServiceTask's executable contract. It is polymorphic
// (ADR-011 v.4 §2.6): a canonical message operation (BPMN inMessage→outMessage,
// decoupled — see messageOperation) and a gobpm-native Go operation (reads
// through a DataReader and returns its result — see package gooper) both
// implement it.
type Operation interface {
	foundation.Identifyer

	// Name returns the operation's name.
	Name() string

	// Type returns the operation's implementation mechanism.
	Type() string

	// Errors returns the error classes the operation may return.
	Errors() []string

	// Clone returns a per-instance copy of the operation, or an error when
	// a carried element cannot be rebuilt (FIX-026 — never panics).
	Clone() (Operation, error)

	// Execute runs the operation against the per-execution reader r and returns
	// the item to commit as the activity's output (nil if the operation
	// produces none).
	Execute(ctx context.Context, r DataReader) (*data.ItemDefinition, error)

	// BindInputOnly binds the operation's input message from r without executing
	// it, returning the bound input item (nil if the operation has no input
	// message). A worker-dispatched ServiceTask uses it to build the job payload:
	// the worker is the executor, so the operation contributes only its input
	// contract (ADR-021 §2.5, SRD-036).
	BindInputOnly(
		ctx context.Context, r DataReader,
	) (*data.ItemDefinition, error)
}

// A messageOperation is the canonical (BPMN) Operation kind: it defines
// Messages that are consumed and, optionally, produced when the Operation is
// called, plus zero or more error classes returned on failure. Data flows in
// through inMessage and out through outMessage; the Implementor sees only its
// message and is decoupled from process scope.
type messageOperation struct {
	implementation Implementor
	inMessage      *bpmncommon.Message
	outMessage     *bpmncommon.Message
	errors         *set.Set[string]
	name           string
	foundation.BaseElement
}

// NewOperation creates a new message Operation and returns it on success or an
// error on failure.
func NewOperation(
	name string,
	inMsg, outMsg *bpmncommon.Message,
	implementor Implementor,
	baseOpts ...options.Option,
) (Operation, error) {
	name = strings.Trim(name, " ")
	if name == "" {
		return nil,
			errs.New(
				errs.M("operation should have non-empty name"),
				errs.C(errorClass, errs.InvalidParameter))
	}

	be, err := foundation.NewBaseElement(baseOpts...)
	if err != nil {
		return nil, err
	}

	el := []string{
		errs.ObjectNotFound,
		errs.OperationFailed,
		errs.EmptyNotAllowed,
	}

	if implementor != nil {
		el = append(el, implementor.ErrorClasses()...)
	}

	return &messageOperation{
		BaseElement:    *be,
		name:           name,
		inMessage:      inMsg,
		outMessage:     outMsg,
		errors:         set.New(el...),
		implementation: implementor,
	}, nil
}

// MustOperation creates a new message Operation and returns it on success or
// panics on failure.
func MustOperation(
	name string,
	inMsg, outMsg *bpmncommon.Message,
	implementor Implementor,
	baseOpts ...options.Option,
) Operation {
	o, err := NewOperation(name, inMsg, outMsg, implementor, baseOpts...)
	if err != nil {
		errs.Panic(err)

		return nil
	}

	return o
}

// Clone returns a per-instance copy of the Operation. The immutable definition —
// the implementation, the error-class set, the name and the id — is shared by
// reference, while the incoming and outgoing messages get fresh per-instance
// carriers (via Message.Clone) so the exec-time mutation of their item values is
// not shared across concurrent instances. It errors instead of panicking when a
// carrier cannot be rebuilt (FIX-026).
func (o *messageOperation) Clone() (Operation, error) {
	var (
		inMsg, outMsg *bpmncommon.Message
		err           error
	)

	if o.inMessage != nil {
		if inMsg, err = o.inMessage.Clone(); err != nil {
			return nil, cloneErr("in", o.name, err)
		}
	}

	if o.outMessage != nil {
		if outMsg, err = o.outMessage.Clone(); err != nil {
			return nil, cloneErr("out", o.name, err)
		}
	}

	be, err := foundation.NewBaseElement(foundation.WithID(o.ID()))
	if err != nil {
		return nil, cloneErr("base", o.name, err)
	}

	return &messageOperation{
		BaseElement:    *be,
		name:           o.name,
		inMessage:      inMsg,
		outMessage:     outMsg,
		errors:         o.errors,
		implementation: o.implementation,
	}, nil
}

// cloneErr classifies an operation-clone rebuild failure.
func cloneErr(part, opName string, err error) error {
	return errs.New(
		errs.M("couldn't rebuild cloned operation (%s)", part),
		errs.C(errorClass, errs.OperationFailed),
		errs.E(err),
		errs.D("operation_name", opName))
}

// Name returns the name of the Operation.
func (o *messageOperation) Name() string {
	return o.name
}

// Errors returns a list of Errors which the Operation could return.
func (o *messageOperation) Errors() []string {
	return o.errors.All()
}

// Type returns the Operation's implementation type or
// unspecified on empyt implementation.
func (o *messageOperation) Type() string {
	if o.implementation != nil {
		return o.implementation.Type()
	}

	return UnspecifiedImplementation
}

// Execute binds the input message from process scope (via the reader),
// runs the implementation against that message, and returns the produced
// output item for the ServiceTask to commit. The implementation sees only its
// message — it is decoupled from process scope.
func (o *messageOperation) Execute(
	ctx context.Context,
	r DataReader,
) (*data.ItemDefinition, error) {
	if o.implementation == nil {
		return nil, errs.New(
			errs.M("no implementation"),
			errs.C(errorClass, errs.ObjectNotFound))
	}

	in, err := BindInput(ctx, r, o.inMessage)
	if err != nil {
		return nil, err
	}

	out, err := o.implementation.Execute(ctx, in)
	if err != nil {
		return nil, errs.New(
			errs.M("operation %q[%s] execution failed", o.name, o.ID()),
			errs.C(errorClass, errs.OperationFailed),
			errs.E(err))
	}

	return o.produceOutput(ctx, out)
}

// BindInputOnly binds the operation's input message from r without running the
// implementation — the worker-dispatched path where the worker is the executor.
func (o *messageOperation) BindInputOnly(
	ctx context.Context,
	r DataReader,
) (*data.ItemDefinition, error) {
	return BindInput(ctx, r, o.inMessage)
}

// produceOutput reconciles the implementation's result against the operation's
// outgoing message: a result with no output message (or vice versa) is an
// error; a matching pair updates the message and returns its item for the
// ServiceTask to commit.
func (o *messageOperation) produceOutput(
	ctx context.Context,
	out *data.ItemDefinition,
) (*data.ItemDefinition, error) {
	hasOut := o.outMessage != nil && o.outMessage.Item() != nil

	switch {
	case out != nil && !hasOut:
		return nil, errs.New(
			errs.M("no output for operation result"),
			errs.C(errorClass, errs.EmptyNotAllowed))

	case out == nil && hasOut:
		return nil, errs.New(
			errs.M("unexpected empty operation return"),
			errs.C(errorClass, errs.EmptyNotAllowed))

	case out != nil && hasOut:
		if err := o.outMessage.Item().
			Structure().Update(ctx, out.Structure().Get(ctx)); err != nil {
			return nil, err
		}

		return o.outMessage.Item(), nil
	}

	return nil, nil
}

// BindInput copies msg's item value from process scope (via r, by the item's
// id, Ready-state checked) into the message and returns the bound item, or
// (nil, nil) if msg carries no item. Operation implementors that accept an
// input message use it to bind the message before running their logic.
func BindInput(
	ctx context.Context,
	r DataReader,
	msg *bpmncommon.Message,
) (*data.ItemDefinition, error) {
	if msg == nil || msg.Item() == nil {
		return nil, nil
	}

	d, err := r.GetDataByID(msg.Item().ID())
	if err != nil {
		return nil, errs.New(
			errs.M("couldn't find item definition"),
			errs.C(errorClass, errs.ObjectNotFound),
			errs.E(err))
	}

	if d.State().Name() != data.ReadyDataState.Name() {
		return nil, errs.New(
			errs.M("data state isn't ready"),
			errs.C(errorClass, errs.ConditionFailed))
	}

	if err := msg.Item().
		Structure().Update(ctx, d.Value().Get(ctx)); err != nil {
		return nil, errs.New(
			errs.M("couldn't update operation's incoming message"),
			errs.C(errorClass, errs.OperationFailed),
			errs.E(err))
	}

	return msg.Item(), nil
}

var _ Operation = (*messageOperation)(nil)
