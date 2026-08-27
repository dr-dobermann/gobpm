package instance

import (
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
)

// bindTransaction is the executing unit's bind step (ADR-028 §2.1, SRD-095
// FR-5): when a scope opens under node, it returns the transaction
// characteristics the scope is bound to — nil for every node that is not a
// Transaction Sub-Process. One helper for the three places a scope entry is
// created (an executor open and the two restore paths), so a rehydrated
// Transaction scope is bound exactly as a fresh one.
func bindTransaction(node flow.Node) *activities.TransactionCharacteristics {
	sp, ok := node.(*activities.SubProcess)
	if !ok {
		return nil
	}

	return sp.Transaction()
}

// boundMethod is the coordinator a Transaction scope aborts through. A scope
// entry without a binding is the compensate default: a Transaction that names
// no method is a compensate transaction (ADR-028 §2.7), and no other kind of
// scope reaches the abort — model validation confines Cancel to a Transaction.
func boundMethod(entry *scopeEntry) activities.TransactionMethod {
	if entry.tx == nil {
		return activities.TransactionCompensate
	}

	return entry.tx.Method()
}
