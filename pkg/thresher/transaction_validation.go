package thresher

import (
	"fmt"
	"sort"
	"strings"

	"github.com/dr-dobermann/gobpm/internal/instance/snapshot"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
)

// registeredTransactionMethods lists the abort coordinators this engine
// provides (ADR-028 §2.7). Compensate is built in; until a coordination seam
// exists (ADR-028 §2.8) it is the only entry.
var registeredTransactionMethods = map[activities.TransactionMethod]struct{}{
	activities.TransactionCompensate: {},
}

// validateTransactionCoverage refuses a model whose Transactions name an
// abort method no coordinator this engine has can perform.
//
// The model carries any method identifier — the schema's tTransactionMethod
// is open — and the executing unit binds a Transaction scope to its
// coordinator by that identifier when the scope opens (ADR-028 §2.1). A
// method nothing coordinates must therefore be caught here, at registration,
// while the caller still holds an error return and nothing has run: the same
// obligation validateScriptCoverage meets for a script format no engine
// claims. The walk is deep — a Transaction inside a plain Sub-Process names
// its method just as much as one at the top level.
func (t *Thresher) validateTransactionCoverage(s *snapshot.Snapshot) error {
	var unmet []string

	s.Walk(func(n flow.Node) bool {
		sp, ok := n.(*activities.SubProcess)
		if !ok {
			return true
		}

		tc := sp.Transaction()
		if tc == nil {
			return true
		}

		if _, known := registeredTransactionMethods[tc.Method()]; !known {
			unmet = append(unmet,
				fmt.Sprintf("%q (method %q)", sp.Name(), string(tc.Method())))
		}

		return true
	})

	if len(unmet) == 0 {
		return nil
	}

	// Walk iterates a map: sort so the same model reports the same message.
	sort.Strings(unmet)

	have := make([]string, 0, len(registeredTransactionMethods))
	for m := range registeredTransactionMethods {
		have = append(have, string(m))
	}

	sort.Strings(have)

	return errs.New(
		errs.M("no transaction coordinator is registered for %d "+
			"transaction(s): %s — this engine coordinates %s only "+
			"(ADR-028 §2.7); model the undo as compensation handlers",
			len(unmet), strings.Join(unmet, "; "), strings.Join(have, ", ")),
		errs.C(errorClass, errs.InvalidObject),
		errs.D("registered_transaction_methods", strings.Join(have, ", ")))
}
