package main

import (
	"context"
	"fmt"

	"github.com/dr-dobermann/gobpm/pkg/adhoc"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
)

// triageRouter decides what the ad-hoc container may run next. It replaces the
// sequence flows an ordinary Sub-Process would carry: the container's inner
// activities have no flows at all, and this is the only thing that says what
// follows what.
//
// The whole vocabulary is here — the answer is a list of activity names, and an
// empty answer means "nothing more from this track".
type triageRouter struct{}

func (triageRouter) Next(
	ctx context.Context, s adhoc.State,
) ([]string, error) {
	switch {
	// Nothing has happened yet: the container has just opened, and the first
	// answer is the standard's "initially enabled" set.
	case s.Last == "":
		return []string{"gather-logs"}, nil

	// The logs are in. Now the decision reads the case's own data — this is what
	// makes ad-hoc routing more than counting activities.
	case s.Last == "gather-logs":
		severity, err := readSeverity(ctx, s)
		if err != nil {
			return nil, err
		}

		if severity == "high" {
			// Two activities at once: under the default parallel ordering both
			// start and run concurrently.
			fmt.Println("  ▷ router: severity is high → notify AND escalate")

			return []string{"notify-customer", "escalate"}, nil
		}

		fmt.Println("  ▷ router: severity is low → notify only")

		return []string{"notify-customer"}, nil

	// One of the fork's branches finished. If its sibling is still working,
	// answer empty: that ends only this track, and the Router will be asked
	// again when the sibling settles. This is how an ad-hoc container joins
	// without a join gateway.
	case len(s.Running) > 0:
		fmt.Printf("  ▷ router: %s done, but a sibling is still running\n",
			s.Last)

		return nil, nil

	// Everything has drained and the incident has not been closed yet.
	case s.Completed["close-incident"] == 0:
		fmt.Println("  ▷ router: all work settled → close the incident")

		return []string{"close-incident"}, nil

	// Nothing left to do. The empty answer leaves no track behind, the scope
	// drains, and the Ad-Hoc Sub-Process completes — there is no separate
	// completion mechanism.
	default:
		fmt.Println("  ▷ router: nothing left → the container ends")

		return nil, nil
	}
}

// readSeverity reads the incident severity the enclosing process carries. The
// Router sees the ad-hoc scope and, through the ordinary walk-up, the process
// data around it — read-only: a Router never writes.
func readSeverity(ctx context.Context, s adhoc.State) (string, error) {
	d, err := s.Data.GetData("severity")
	if err != nil {
		return "", fmt.Errorf("read severity: %w", err)
	}

	severity, err := data.As[string](ctx, d.Value())
	if err != nil {
		return "", fmt.Errorf("severity is not a string: %w", err)
	}

	return severity, nil
}
