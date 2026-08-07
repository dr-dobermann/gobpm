package observability

// LogRedactor is an optional capability an AuthorizationProvider MAY implement to
// transform or suppress the operator-log echo of an observable event (ADR-013
// v.2 §2.11). It is asserted once against the configured authorizer at engine
// start; an authorizer that does not implement it leaves the log echo
// pass-through (the ADR default), and no per-event assertion is paid. ok=false
// suppresses the log record entirely.
//
// RedactLog is called ON THE REPORTING GOROUTINE — usually an instance's
// execution loop — once per observable event, so it must be cheap and MUST NOT
// block: whatever it waits for, the process being reported on waits for too.
//
// ev is SHARED. Fact is a struct, but its Details map is a reference, so
// redacting in place — the allocation-free reflex — mutates the very event the
// observer stream is about to fan out, and every observer sees the redaction
// the log was supposed to get. Return a modified COPY (clone Details before
// writing to it); treat the argument as read-only.
//
// A panic is contained and the record is SUPPRESSED, as though the redactor had
// returned ok=false: a redactor exists to keep detail out of the log, so one
// that fails to run cannot be read as having permitted the record. The engine
// logs the first panic at Warn with a stack and later ones at Debug, and keeps
// running.
type LogRedactor interface {
	RedactLog(ev Fact) (Fact, bool)
}

// ObservationFilter is an optional capability an AuthorizationProvider MAY
// implement for per-recipient visibility of an observable event on the observer
// stream (ADR-013 v.2 §2.11). It is asserted once at observer registration;
// absent ⇒ pass-through. observer is the registering observer (opaque here — the
// policy decides what it means). ok=false denies delivery to that recipient: a
// policy denial, distinct from a counted buffer drop.
//
// FilterObservation is called ON THE REPORTING GOROUTINE, once per event PER
// REGISTERED OBSERVER, under the producer's dispatch lock — the one every
// Report in the engine passes through. It must therefore be cheap and MUST NOT
// block or call back into the engine: a slow filter serializes every reporter
// behind it, and a blocking one stalls them all. Decide from the arguments;
// do the expensive part in the observer.
//
// ev is SHARED, and here the sharing is per-recipient: the same Fact is passed
// to every registered observer in turn, and its Details map is a reference.
// Filtering in place therefore leaks one recipient's view into all the others
// and into the log echo — the exact opposite of what a per-recipient filter is
// for. Return a modified COPY (clone Details before writing to it); treat the
// argument as read-only.
//
// A panic is contained and the event is DENIED to that recipient, as though the
// filter had returned ok=false — a policy that failed to run cannot be read as
// having permitted delivery. The engine logs the first panic at Warn with a
// stack and later ones at Debug, and keeps running.
type ObservationFilter interface {
	FilterObservation(observer any, ev Fact) (Fact, bool)
}
