// Package script defines the Script Engine seam (ADR-031): pluggable
// script interpreters the Script Task executes its body on, routed by the
// standard's own scriptFormat MIME hint. More than one engine registers in
// a single gobpm engine — the Registry folds their enumerable format
// claims into a routing map (claim conflicts are loud construction-time
// errors) and is itself an Engine, so the task talks to one interface.
//
// Interpreters live in adapter modules (the core stays stdlib-light); the
// in-core default is the empty Registry — "##None" — whose execution fails
// with a classified error telling the operator to register an engine
// (e.g. adapters/lua) via thresher.WithScriptEngine.
package script

import (
	"context"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
)

// Outputs is a script's named results — each entry commits as its own
// Ready datum (a script sets variables; ADR-031 §2.3).
type Outputs map[string]data.Value

// Engine executes script bodies for the Script Task. An implementation
// interprets one or more script formats (MIME hints, matched
// case-insensitively) and names its kind in the "##"-convention.
type Engine interface {
	// Type names the engine kind ("##Lua", ...) for the startup config
	// and the Script facts.
	Type() string

	// Formats returns the MIME hints this engine interprets — an
	// enumerable claim (never empty for a real engine), so the Registry
	// can detect conflicts and print the routing table.
	Formats() []string

	// Execute runs the script body against the read-only process-data
	// surface and returns the script's named outputs (nil or empty when
	// the script produces none). format is the task's scriptFormat — an
	// engine may interpret several dialects by it.
	Execute(
		ctx context.Context,
		format, script string,
		r service.DataReader,
	) (Outputs, error)
}
