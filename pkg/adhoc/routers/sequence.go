package routers

import (
	"context"
	"slices"

	"github.com/dr-dobermann/gobpm/pkg/adhoc"
	"github.com/dr-dobermann/gobpm/pkg/errs"
)

// sequence walks a fixed list of activities, one at a time.
type sequence struct {
	ids []string
}

// Sequence returns a Router that runs the named activities in the given order,
// one at a time, and ends the container when the list is exhausted. It is the
// crystallized end state of an ad-hoc container: work that began human-steered
// has hardened into a known order, and saying so in the model makes that order
// reviewable — which routing by construction order would not.
//
// The step is derived from how many activities have completed, so it holds
// under either ordering: only one activity is ever answered at a time.
func Sequence(ids ...string) (adhoc.Router, error) {
	if len(ids) == 0 {
		return nil, errs.New(
			errs.M("Sequence: at least one activity must be named"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	for i, id := range ids {
		if id == "" {
			return nil, errs.New(
				errs.M("Sequence: the activity at position %d is unnamed", i),
				errs.C(errorClass, errs.EmptyNotAllowed))
		}
	}

	return sequence{ids: slices.Clone(ids)}, nil
}

func (s sequence) Next(_ context.Context, st adhoc.State) ([]string, error) {
	done := 0
	for _, n := range st.Completed {
		done += n
	}

	if done >= len(s.ids) {
		return nil, nil
	}

	return []string{s.ids[done]}, nil
}
