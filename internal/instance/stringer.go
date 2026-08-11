package instance

import "fmt"

// An Instance and a track are handed to collaborators as interface
// values — an eventproc.EventProcessor on every RegisterEvent, for
// instance. Anything that formats such a value with %v (a log line, an
// error message, a test double's argument matcher) would otherwise walk
// the struct field by field, reading the correlator's maps, the track
// table and the instance mutexes from whatever goroutine happens to be
// formatting. Those reads take no lock and cannot: fmt reaches the
// fields through reflection, behind the type's back.
//
// The engine's own writes to those fields ARE synchronized, so the
// result is not corruption — it is an unsynchronized reader, which the
// race detector reports as a data race with engine code on one side and
// a formatter on the other. Reading one is a small research project
// that ends at fmt, and gobpm spent one on exactly that (see the FIX
// accompanying this file).
//
// A Stringer stops the walk. fmt calls String() instead of reflecting,
// so the only state read is the element id, which is immutable after
// construction. This is also simply better output: a process instance
// renders as its identity rather than as a page of engine internals.

// String renders the instance as its id — immutable, so it is safe to
// call from any goroutine.
//
// The nil guard is for a DIRECT call. fmt does not need it: measured,
// it prints "<nil>" for a nil pointer without invoking String at all.
// A caller reaching for the method itself gets the same answer instead
// of a panic.
func (inst *Instance) String() string {
	if inst == nil {
		return "<nil>"
	}

	return fmt.Sprintf("instance %s", inst.ID())
}

// String renders the track as its id, on the same terms as Instance's.
func (t *track) String() string {
	if t == nil {
		return "<nil>"
	}

	return fmt.Sprintf("track %s", t.ID())
}
