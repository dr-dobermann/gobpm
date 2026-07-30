// Package routers ships ready-made Ad-Hoc Routers for the shapes that need no
// host decision code (ADR-035 §2.9).
//
// Every one of them is attached explicitly. The engine applies no Router by
// default and never infers routing — least of all from the order in which
// activities were added to the container — so naming a battery makes the
// container's behavior a stated property of the model rather than an artifact
// of how it was built.
package routers

const errorClass = "AD_HOC_ROUTERS_ERRORS"
