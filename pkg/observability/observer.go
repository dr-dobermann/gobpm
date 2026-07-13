package observability

// Observer receives observation Facts (ADR-013 v.2 §2.8). It is the ONE
// interface a host implements to watch the engine — an instance's stream (via a
// handle) or the engine-wide stream — and registers with the engine; a host
// never constructs the Reporter that feeds it. OnFact is called from a
// per-observer drain goroutine, never on the engine's execution path; it MAY
// block without stalling the engine (the engine drops Facts past its buffer
// instead), and a panic in it is recovered.
type Observer interface {
	OnFact(Fact)
}
