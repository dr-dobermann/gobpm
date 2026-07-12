package observability

// LogRedactor is an optional capability an AuthorizationProvider MAY implement to
// transform or suppress the operator-log echo of an observable event (ADR-013
// v.2 §2.11). It is asserted once against the configured authorizer at engine
// start; an authorizer that does not implement it leaves the log echo
// pass-through (the ADR default), and no per-event assertion is paid. ok=false
// suppresses the log record entirely.
type LogRedactor interface {
	RedactLog(ev Fact) (Fact, bool)
}

// ObservationFilter is an optional capability an AuthorizationProvider MAY
// implement for per-recipient visibility of an observable event on the observer
// stream (ADR-013 v.2 §2.11). It is asserted once at observer registration;
// absent ⇒ pass-through. observer is the registering observer (opaque here — the
// policy decides what it means). ok=false denies delivery to that recipient: a
// policy denial, distinct from a counted buffer drop.
type ObservationFilter interface {
	FilterObservation(observer any, ev Fact) (Fact, bool)
}
