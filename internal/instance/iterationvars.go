package instance

import (
	"strconv"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
)

// The engine's own iteration names, published beside BPMN's `loopCounter`
// (ADR-025 §2.9.2). They name what the standard has no word for, so they
// follow the engine's runtime-name convention rather than BPMN's spelling.
//
// All three are values of the EXECUTION: they differ per iteration of one
// activity, and N instances can read them at the same moment. That is why
// they are published where the asking execution is identified — frame-local
// where the path binds frame-local, at the activity's own scope where it
// binds there — and never at a flat RUNTIME name, which receives a name and
// nothing else and so could not say whose ordinal was asked for.
// The names themselves are declared in the model layer (pkg/model/data),
// which owns the data vocabulary and refuses a model that declares one of
// them. The runtime references those constants rather than repeating the
// strings, so the set that is PUBLISHED here and the set that is REFUSED
// there cannot drift apart.
const (
	// IterationNumber is the executing iteration's 0-based ordinal — the same
	// value as `loopCounter`, under the engine's own name.
	IterationNumber = data.IterationNumberName

	// IterationID is the executing iteration's derived identity (ADR-025
	// §2.9.3): the enclosing scope path, the activity id and the ordinal.
	IterationID = data.IterationIDName

	// IterationMode is the shape being iterated — the record's own `kind`,
	// not a second vocabulary (iter_mirror.go).
	IterationMode = data.IterationModeName
)

// iterationIDOf derives an instance's identity rather than minting one
// (ADR-025 §2.9.3). All three components already survive a checkpoint — the
// scope path is in the scope table, the activity id is in the graph, the
// ordinal is in the executor set — so the identity is stable across a
// restore with nothing stored for it, and a minted id would have added a
// field whose only job is to say what the other three already say.
func iterationIDOf(scopePath string, node flow.Node, ord int) string {
	id := ""
	if node != nil {
		id = node.ID()
	}

	return scopePath + "/" + id + "#" + strconv.Itoa(ord)
}

// iterationVars builds the three engine-named iteration values for iteration
// ord. One builder for every publication path — the parallel leaf's
// frame-local bind, the sequential Multi-Instance's and the Standard Loop's
// host-scope binds — so the three cannot drift apart in what they publish.
//
// kind is the record's iteration kind; an empty kind means the caller does
// not iterate and gets no values back.
func iterationVars(
	scopePath, kind string, node flow.Node, ord int,
) ([]data.Data, error) {
	if kind == "" {
		return nil, nil
	}

	vals := []struct {
		val  any
		name string
	}{
		{ord, IterationNumber},
		{iterationIDOf(scopePath, node, ord), IterationID},
		{kind, IterationMode},
	}

	out := make([]data.Data, 0, len(vals))

	for _, v := range vals {
		d, err := data.ReadyValueParameter(v.name, values.NewVariable(v.val))
		if err != nil {
			return nil, err
		}

		out = append(out, d)
	}

	return out, nil
}

// iterationBindings is iterationVars in the shape the host-scope binders take
// (name/value pairs rather than data.Data), so the sequential Multi-Instance
// and Standard Loop paths publish the identical set without building the
// parameters twice.
func iterationBindings(
	scopePath, kind string, node flow.Node, ord int,
) []miBinding {
	if kind == "" {
		return nil
	}

	return []miBinding{
		{name: IterationNumber, value: ord},
		{name: IterationID, value: iterationIDOf(scopePath, node, ord)},
		{name: IterationMode, value: kind},
	}
}
