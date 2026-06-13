package scope

import (
	"github.com/dr-dobermann/gobpm/pkg/model/data"
)

// RuntimeVarsSegment is the reserved path segment under the plane's root
// holding the engine's synthetic runtime variables (SRD-007 FR-9). The
// subtree it names is read-only: Commit and OpenScope reject it whether or
// not a RuntimeVarsSupplier is configured.
const RuntimeVarsSegment = "RUNTIME"

// RuntimeVarsSupplier serves the synthetic, read-only runtime variables of
// the reserved RUNTIME subtree. The Instance provides the supplier; values
// are synthesized on demand, so every read observes the live engine state.
type RuntimeVarsSupplier interface {
	// RuntimeVar returns the runtime variable named name or an error if no
	// such variable exists.
	RuntimeVar(name string) (data.Data, error)

	// RuntimeVarNames lists the runtime variable names the supplier exposes.
	RuntimeVarNames() []string
}

// runtimeSource adapts a RuntimeVarsSupplier to data.SourceProvider so the
// runtime variables are reachable as the RUNTIME source ("RUNTIME/STATE").
type runtimeSource struct {
	rt RuntimeVarsSupplier
}

// Get resolves the runtime variable addressed by addr (a flat variable name).
func (s runtimeSource) Get(addr string) (data.Data, error) {
	return s.rt.RuntimeVar(addr)
}

// Names lists the runtime variable names the supplier exposes.
func (s runtimeSource) Names() []string {
	return s.rt.RuntimeVarNames()
}
