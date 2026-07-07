// Package gooper provides the gobpm-native Go operation: a ServiceTask
// Operation implemented by an in-process Go functor that reads through a
// public data reader and, optionally, consumes/produces messages
// (ADR-011 v.5 §2.6).
package gooper

import (
	"context"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/bpmncommon"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/set"
)

const (
	errorClass = "GOOPER"

	// GoOperType is the implementation mechanism of a Go operation.
	GoOperType = "##GoOper"
)

// OpFunctor is an in-process Go operation body. It receives the per-execution
// read-only data reader and its optional bound input message item (nil when no
// incoming message is declared), and returns its result (committed by the
// ServiceTask, filling the outgoing message when one is declared).
type OpFunctor func(
	ctx context.Context,
	r service.DataReader,
	in *data.ItemDefinition,
) (*data.ItemDefinition, error)

// goOperation is the gobpm-native Operation kind. It composes reader-based and
// (optional) message-based data access at the author's choice.
type goOperation struct {
	f          OpFunctor
	inMessage  *bpmncommon.Message
	outMessage *bpmncommon.Message
	errors     *set.Set[string]
	name       string
	foundation.BaseElement
}

// Option configures a Go operation at construction.
type Option func(*goOperation)

// WithInMessage declares an incoming message the functor receives (bound from
// process scope before the functor runs).
func WithInMessage(msg *bpmncommon.Message) Option {
	return func(g *goOperation) {
		g.inMessage = msg
	}
}

// WithOutMessage declares an outgoing message the functor's result fills.
func WithOutMessage(msg *bpmncommon.Message) Option {
	return func(g *goOperation) {
		g.outMessage = msg
	}
}

// WithErrors adds error classes the functor may return.
func WithErrors(ers ...string) Option {
	return func(g *goOperation) {
		g.errors.Add(ers...)
	}
}

// New creates a Go operation named name running f, returning it as a
// service.Operation. A nil functor or an empty name is rejected with a
// self-identifying error. Optional incoming/outgoing messages and error
// classes are supplied through options.
func New(
	name string,
	f OpFunctor,
	opts ...Option,
) (service.Operation, error) {
	if name == "" {
		return nil,
			errs.New(
				errs.M("New: a Go operation needs a non-empty name"),
				errs.C(errorClass, errs.InvalidParameter))
	}

	if f == nil {
		return nil,
			errs.New(
				errs.M("New: a nil functor isn't allowed for %q", name),
				errs.C(errorClass, errs.EmptyNotAllowed))
	}

	// no foundation options here, so construction cannot fail.
	g := &goOperation{
		BaseElement: *foundation.MustBaseElement(),
		name:        name,
		f:           f,
		errors: set.New(
			errs.ObjectNotFound,
			errs.OperationFailed,
			errs.EmptyNotAllowed),
	}

	for _, opt := range opts {
		opt(g)
	}

	return g, nil
}

// Name returns the operation's name.
func (g *goOperation) Name() string {
	return g.name
}

// Type returns the Go operation mechanism marker.
func (g *goOperation) Type() string {
	return GoOperType
}

// Errors returns the error classes the operation may return.
func (g *goOperation) Errors() []string {
	return g.errors.All()
}

// Clone returns a per-instance copy: the functor and error set are shared by
// reference, the id is preserved, and the messages get fresh carriers so
// exec-time mutation isn't shared across concurrent instances.
func (g *goOperation) Clone() service.Operation {
	var inMsg, outMsg *bpmncommon.Message

	if g.inMessage != nil {
		inMsg = g.inMessage.Clone()
	}

	if g.outMessage != nil {
		outMsg = g.outMessage.Clone()
	}

	return &goOperation{
		BaseElement: *foundation.MustBaseElement(foundation.WithID(g.ID())),
		name:        g.name,
		f:           g.f,
		inMessage:   inMsg,
		outMessage:  outMsg,
		errors:      g.errors,
	}
}

// Execute binds the optional input message from scope, runs the functor with
// the reader and that item, and returns its result — filling the outgoing
// message when one is declared.
func (g *goOperation) Execute(
	ctx context.Context,
	r service.DataReader,
) (*data.ItemDefinition, error) {
	in, err := service.BindInput(ctx, r, g.inMessage)
	if err != nil {
		return nil, err
	}

	out, err := g.f(ctx, r, in)
	if err != nil {
		return nil, errs.New(
			errs.M("go operation %q[%s] failed", g.name, g.ID()),
			errs.C(errorClass, errs.OperationFailed),
			errs.E(err))
	}

	if out != nil && g.outMessage != nil && g.outMessage.Item() != nil {
		if err := g.outMessage.Item().
			Structure().Update(ctx, out.Structure().Get(ctx)); err != nil {
			return nil, errs.New(
				errs.M("go operation %q: couldn't fill the outgoing message",
					g.name),
				errs.C(errorClass, errs.OperationFailed),
				errs.E(err))
		}

		return g.outMessage.Item(), nil
	}

	return out, nil
}

// BindInputOnly binds the optional input message from scope without running the
// functor. A goOperation is an in-process closure and can't be worker-dispatched
// (SRD-036 §2.3 build guard), so this exists only to satisfy the Operation
// contract; it binds the input consistently with a message operation.
func (g *goOperation) BindInputOnly(
	ctx context.Context,
	r service.DataReader,
) (*data.ItemDefinition, error) {
	return service.BindInput(ctx, r, g.inMessage)
}

var _ service.Operation = (*goOperation)(nil)
