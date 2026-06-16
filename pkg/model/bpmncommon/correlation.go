package bpmncommon

import (
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
)

// 1. Key-based correlation.
//
// Key-based correlation is a simple and efficient form of correlation,
// where one or more keys are used to identify a Conversation.
//
// Any incoming Message can be matched against the CorrelationKey by extracting
// the CorrelationProperties from the Message according to the corresponding
// CorrelationPropertyRetrievalExpression and comparing the resulting composite
// key with the CorrelationKey instance for this Conversation.
//
// The idea is to use a joint Conversation “token” which is used (passed to and
// received from) and outgoing and incoming Message. Messages are associated
// to a particular Conversation if the composite key extracted from their
// payload matches the CorrelationKey initialized for this Conversation.
//
// At runtime the first Send Task or Receive Task in a Conversation MUST
// populate at least one of the CorrelationKey instances by extracting the
// values of the CorrelationProperties according to the
// CorrelationPropertyRetrievalExpression from the initially sent or received
// Message. Later in the Conversation, the populated CorrelationKey instances
// are used for the described matching procedure where from incoming Messages
// a composite key is extracted and used to identify the associated
// Conversation. Where these noninitiating Messages derive values for
// CorrelationKeys, associated with the Conversation but not yet populated,
// then the derived value will be associated with the Conversation instance.
//
// 2. Context-based correlation.
//
// Context-based correlation is a more expressive form of correlation on top
// of key-based correlation. In addition to implicitly populating the
// CorrelationKey instance from the first sent or received Message, another
// mechanism relates the CorrelationKey to the Process context.
//
// That is, a Process MAY provide a CorrelationSubscription that acts as the
// Process-specific counterpart to a specific CorrelationKey. In this way, a
// Conversation MAY additionally refer to explicitly updateable Process context
// data to determine whether or not a Message needs to be received. At runtime,
// the CorrelationKey instance holds a composite key that is dynamically
// calculated from the Process context and automatically updated whenever the
// underlying Data Objects or Properties change.

type (
	// CorrelationSubscription represents a BPMN correlation subscription.
	CorrelationSubscription struct {
		foundation.BaseElement

		// The CorrelationKey this CorrelationSubscription refers to.
		Key *CorrelationKey

		// The bindings to specific CorrelationProperties and FormalExpressions
		// (extraction rules atop the Process context).
		PropertyBindings []*CorrelationPropertyBinding
	}

	// A CorrelationKey represents a composite key out of one or many
	// CorrelationProperties that essentially specify extraction Expressions
	// atop Messages. As a result, each CorrelationProperty acts as a partial
	// key for the correlation. For each Message that is exchanged as part of
	// a particular Conversation, the CorrelationProperties need to provide a
	// CorrelationPropertyRetrievalExpression which references a
	// FormalExpression to the Message payload. That is, for each Message
	// (that is used in a Conversation) there is an Expression, which extracts
	// portions of the respective Message’s payload.
	CorrelationKey struct {
		foundation.BaseElement

		// Specifies the name of the CorrelationKey.
		Name string

		// The CorrelationProperties, representing the partial keys of this
		// CorrelationKey.
		Properties []CorrelationProperty
	}

	// CorrelationProperty represents a BPMN correlation property.
	CorrelationProperty struct {
		foundation.BaseElement

		// Specifies the name of the CorrelationProperty.
		Name string

		// Specifies the type of the CorrelationProperty.
		Type string

		// The retrievalExpressions for this CorrelationProperty, representing
		// the associations of FormalExpressions (extraction paths) to specific
		// Messages occurring in this Conversation.
		Expressions []CorrelationPropertyRetrievalExpression
	}

	// CorrelationPropertyBinding represents the partial keys of a
	// CorrelationSubscription where each relates to a specific
	// CorrelationProperty in the associated CorrelationKey. A FormalExpression
	// defines how that CorrelationProperty instance is populated and updated
	// at runtime from the Process context (i.e., its Data Objects and
	// Properties).
	CorrelationPropertyBinding struct {
		DataPath data.FormalExpression
		Property *CorrelationProperty
		foundation.BaseElement
	}

	// CorrelationPropertyRetrievalExpression represents a BPMN correlation property retrieval expression.
	CorrelationPropertyRetrievalExpression struct {
		MessagePath data.FormalExpression
		MessageRef  *Message
		foundation.BaseElement
	}
)

// NewCorrelationKey builds a CorrelationKey out of one or more partial-key
// CorrelationProperties (SRD-015 §4.5). A blank name or an empty property set
// is rejected.
func NewCorrelationKey(
	name string,
	props []CorrelationProperty,
	baseOpts ...options.Option,
) (*CorrelationKey, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errs.New(
			errs.M("CorrelationKey should have a non-empty name"),
			errs.C(errorClass, errs.InvalidParameter))
	}

	if len(props) == 0 {
		return nil, errs.New(
			errs.M("CorrelationKey %q needs at least one CorrelationProperty",
				name),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	be, err := foundation.NewBaseElement(baseOpts...)
	if err != nil {
		return nil, err
	}

	return &CorrelationKey{
		BaseElement: *be,
		Name:        name,
		Properties:  append([]CorrelationProperty(nil), props...),
	}, nil
}

// NewCorrelationProperty builds a CorrelationProperty (a partial key) with the
// per-Message retrieval expressions that extract its value. A blank name or an
// empty expression set is rejected.
func NewCorrelationProperty(
	name, pType string,
	exprs []CorrelationPropertyRetrievalExpression,
	baseOpts ...options.Option,
) (*CorrelationProperty, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errs.New(
			errs.M("CorrelationProperty should have a non-empty name"),
			errs.C(errorClass, errs.InvalidParameter))
	}

	if len(exprs) == 0 {
		return nil, errs.New(
			errs.M("CorrelationProperty %q needs at least one retrieval "+
				"expression", name),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	be, err := foundation.NewBaseElement(baseOpts...)
	if err != nil {
		return nil, err
	}

	return &CorrelationProperty{
		BaseElement: *be,
		Name:        name,
		Type:        strings.TrimSpace(pType),
		Expressions: append([]CorrelationPropertyRetrievalExpression(nil), exprs...),
	}, nil
}

// NewCorrelationPropertyRetrievalExpression associates a FormalExpression
// (extraction path over messageRef's payload) with the Message it applies to. A
// nil messagePath or messageRef is rejected.
func NewCorrelationPropertyRetrievalExpression(
	messagePath data.FormalExpression,
	messageRef *Message,
	baseOpts ...options.Option,
) (*CorrelationPropertyRetrievalExpression, error) {
	if messagePath == nil {
		return nil, errs.New(
			errs.M("a nil MessagePath expression isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if messageRef == nil {
		return nil, errs.New(
			errs.M("a nil MessageRef isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	be, err := foundation.NewBaseElement(baseOpts...)
	if err != nil {
		return nil, err
	}

	return &CorrelationPropertyRetrievalExpression{
		MessagePath: messagePath,
		MessageRef:  messageRef,
		BaseElement: *be,
	}, nil
}
