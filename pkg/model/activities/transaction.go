package activities

import (
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/errs"
)

// TransactionMethod names the coordinator that aborts a Transaction
// Sub-Process (BPMN §10.7 `method`, ADR-028 §2.7). The set is open — the
// schema's tTransactionMethod admits any URI — so the model carries whatever
// a document names and registration decides whether this engine has a
// coordinator for it.
type TransactionMethod string

// TransactionCompensate is the one coordinator the engine provides and the
// default: abort undoes completed work by running compensation handlers.
const TransactionCompensate TransactionMethod = "compensate"

// compensateSpellings are the forms that denote the compensate method: the
// metamodel's own spelling and the schema token that is tTransactionMethod's
// default. The absent attribute is handled by ParseTransactionMethod itself.
var compensateSpellings = map[string]TransactionMethod{
	"compensate":   TransactionCompensate,
	"##Compensate": TransactionCompensate,
}

// ParseTransactionMethod reads a document's method attribute. An absent
// (blank) value and both compensate spellings yield TransactionCompensate;
// any other value is carried as is, trimmed, for registration to judge
// (ADR-028 §2.7).
func ParseTransactionMethod(s string) TransactionMethod {
	s = strings.TrimSpace(s)
	if s == "" {
		return TransactionCompensate
	}

	if m, ok := compensateSpellings[s]; ok {
		return m
	}

	return TransactionMethod(s)
}

// TransactionCharacteristics is what makes a Sub-Process a Transaction
// (ADR-028 §2.1): the abort method and the coordination protocol the
// document states. Immutable after construction and shared by clones, like
// the Ad-Hoc spec; execution reads the method when it binds the scope to its
// coordinator and never reads the protocol.
type TransactionCharacteristics struct {
	method   TransactionMethod
	protocol string
}

// Method returns the coordinator this transaction aborts through.
func (tc *TransactionCharacteristics) Method() TransactionMethod {
	return tc.method
}

// Protocol returns the coordination protocol the document stated, or "" when
// it stated none. Carried for loading, round-trip and a future coordinator;
// never interpreted by the engine.
func (tc *TransactionCharacteristics) Protocol() string {
	return tc.protocol
}

// TransactionOption configures the characteristics WithTransaction builds.
type TransactionOption func(*TransactionCharacteristics) error

// WithTransactionMethod sets the abort method. Any non-blank identifier is
// accepted here; whether this engine coordinates it is checked at process
// registration (ADR-028 §2.7).
func WithTransactionMethod(m TransactionMethod) TransactionOption {
	return func(tc *TransactionCharacteristics) error {
		if strings.TrimSpace(string(m)) == "" {
			return errs.New(
				errs.M("WithTransactionMethod: a blank method isn't allowed — "+
					"omit the option for the compensate default"),
				errs.C(errorClass, errs.InvalidParameter))
		}

		tc.method = m

		return nil
	}
}

// WithTransactionProtocol sets the coordination protocol the document
// stated. Opaque to the engine; a blank value states nothing and is refused.
func WithTransactionProtocol(p string) TransactionOption {
	return func(tc *TransactionCharacteristics) error {
		if strings.TrimSpace(p) == "" {
			return errs.New(
				errs.M("WithTransactionProtocol: a blank protocol isn't allowed — "+
					"omit the option when the document states none"),
				errs.C(errorClass, errs.InvalidParameter))
		}

		tc.protocol = p

		return nil
	}
}
