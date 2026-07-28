package thresher

import (
	gerrs "github.com/dr-dobermann/gobpm/pkg/errs"

	"github.com/dr-dobermann/gobpm/internal/instance"
)

// correlationDropClass marks a wake refused because its trigger belongs to
// another conversation (ADR-016). It is a BENIGN outcome, not an engine
// failure: the message simply was not this instance's, so the wake path logs it
// at Debug and leaves the instance dehydrated, exactly as a resident instance
// would drop it and stay parked.
const correlationDropClass = instance.CorrelationDropClass

// errNoHold builds the classified "the hold was not taken" error a caller
// treats as "keep this wait resident".
func errNoHold(msg string) error {
	return gerrs.New(
		gerrs.M(msg),
		gerrs.C(errorClass, gerrs.InvalidState))
}
