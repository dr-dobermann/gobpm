package waiters_test

import (
	"strings"
	"testing"
	"time"

	"github.com/dr-dobermann/gobpm/generated/mockeventproc"
	"github.com/dr-dobermann/gobpm/internal/eventproc/eventhub/waiters"
	"github.com/dr-dobermann/gobpm/internal/enginert"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/expression/lite"
	"github.com/stretchr/testify/require"
)

// TestTimerWaiterWithoutADataSource pins that a timer whose event
// processor is not a data.Source still builds.
//
// An event processor is not required to be one, and a start event's is
// not: the waiter used to pass the failed type assertion's nil straight
// to the expression engine, which refuses a nil Source before reading the
// expression at all. The result was that a timer carrying a literal date
// failed with an error naming neither the timer nor the date.
//
// The existing timer tests never saw it because they use functor
// expressions, which evaluate themselves and ignore the source. Only a
// TEXT expression routes through the engine registry that validates it —
// which is what the BPMN importer mints for a <timeDate>.
func TestTimerWaiterWithoutADataSource(t *testing.T) {
	fireAt := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)

	expr, err := lite.Expr(
		"time('"+fireAt+"')", data.WithResultType("Time"))
	require.NoError(t, err)

	eDef, err := events.NewTimerEventDefinition(expr, nil, nil)
	require.NoError(t, err)

	ep := mockeventproc.NewMockEventProcessor(t)
	hub := mockeventproc.NewMockEventHub(t)

	w, err := waiters.NewTimeWaiter(hub, ep, eDef, "", enginert.Default())
	require.NoError(t, err,
		"a literal timer date reads no process data, so a processor that "+
			"carries none must not stop it")
	require.NotNil(t, w)
}

// TestTimerWaiterExpressionNeedingData pins the other half: an expression
// that DOES read process data still fails when the processor carries
// none — but it fails inside the lookup, naming the variable it wanted,
// rather than at a nil-Source guard that can name nothing.
func TestTimerWaiterExpressionNeedingData(t *testing.T) {
	expr, err := lite.Expr("time(fireAt)", data.WithResultType("Time"))
	require.NoError(t, err)

	eDef, err := events.NewTimerEventDefinition(expr, nil, nil)
	require.NoError(t, err)

	ep := mockeventproc.NewMockEventProcessor(t)
	hub := mockeventproc.NewMockEventHub(t)

	_, err = waiters.NewTimeWaiter(hub, ep, eDef, "", enginert.Default())
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "fireAt"),
		"the error must name the variable the expression wanted, got: %v", err)
	require.True(t, strings.Contains(err.Error(), "not a data source"),
		"the error must carry the instructional context — the reader has to "+
			"learn WHY the lookup failed, not just what failed; got: %v", err)
}
