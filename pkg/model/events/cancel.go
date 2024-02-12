package events

// Cancel Events are only used in the context of modeling Transaction
// Sub-Processes. There are two variations: a catch Intermediate Event and an
// End Event.
//   - The catch Cancel Intermediate Event MUST only be attached to the
//     boundary of a Transaction Sub-Process and, thus, MAY NOT be used in
//     normal flow.
//   - The Cancel End Event MUST only be used within a Transaction Sub-Process
//     and, thus, MAY NOT be used in any other type of Sub-Process or Process.
type CancelEventDefinition struct {
	Definition
}
