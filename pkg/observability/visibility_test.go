package observability

import "testing"

type suppressingRedactor struct{}

func (suppressingRedactor) RedactLog(ev ObsEvent) (ObsEvent, bool) { return ev, false }

type denyingFilter struct{}

func (denyingFilter) FilterObservation(_ any, ev ObsEvent) (ObsEvent, bool) { return ev, false }

// plainAuthz implements neither capability — the pass-through default an
// authorizer like allowall exhibits (T-8 partial: absent ⇒ pass-through).
type plainAuthz struct{}

func TestVisibilityCapabilitiesAreOptional(t *testing.T) {
	var absent any = plainAuthz{}

	if _, ok := absent.(LogRedactor); ok {
		t.Error("plainAuthz unexpectedly implements LogRedactor")
	}

	if _, ok := absent.(ObservationFilter); ok {
		t.Error("plainAuthz unexpectedly implements ObservationFilter")
	}
}

func TestVisibilityCapabilitiesGovernVisibility(t *testing.T) {
	var present any = struct {
		suppressingRedactor
		denyingFilter
	}{}

	lr, ok := present.(LogRedactor)
	if !ok {
		t.Fatal("expected the type to implement LogRedactor")
	}

	if _, keep := lr.RedactLog(ObsEvent{}); keep {
		t.Error("suppressing redactor should return keep=false")
	}

	of, ok := present.(ObservationFilter)
	if !ok {
		t.Fatal("expected the type to implement ObservationFilter")
	}

	if _, keep := of.FilterObservation(nil, ObsEvent{}); keep {
		t.Error("denying filter should return keep=false")
	}
}
