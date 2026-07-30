package routers

import (
	"context"

	"github.com/dr-dobermann/gobpm/pkg/adhoc"
)

// standard enables every inner activity until it has run once.
type standard struct{}

// Standard returns the BPMN-shaped Router: every inner activity is enabled
// until it has run, and the container ends once all of them have. This is the
// closest thing §13.3.5 describes to a normal ad-hoc container — the activities
// are available, in no particular order, each performed once.
//
// Under the default parallel ordering the whole remaining set starts at once,
// which is the standard's "initially all enabled"; under manual selection the
// same set is offered for a host to choose from. It is therefore NOT usable
// with sequential ordering, which permits one live activity and reports a
// multi-successor answer as a modeling error — use Sequence for that.
func Standard() adhoc.Router {
	return standard{}
}

func (standard) Next(_ context.Context, s adhoc.State) ([]string, error) {
	next := make([]string, 0, len(s.Activities))

	for _, id := range s.Activities {
		if s.Completed[id] == 0 && s.Running[id] == 0 {
			next = append(next, id)
		}
	}

	return next, nil
}
