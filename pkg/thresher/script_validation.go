package thresher

import (
	"fmt"
	"strings"

	"github.com/dr-dobermann/gobpm/internal/instance/snapshot"
	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
)

// validateScriptCoverage refuses a model whose Script Tasks no configured
// engine can execute.
//
// The check belongs here, not in the engine's zero-config defaults. A thresher
// built with no script engine is a legitimate engine — most processes have no
// Script Task, and forcing every host to wire one to satisfy a port they never
// use is the tax the closed-port model exists to avoid. What is NOT legitimate
// is accepting a model, reporting it registered, and only discovering at
// execution — asynchronously, inside a track, after the instance is already
// running — that the token has arrived at a task nothing can run. So the
// obligation is the model's, not the engine's: a process that demands a script
// format is refused at registration, when the caller is still holding an error
// return and has changed nothing.
//
// The walk is deep: a Script Task inside a Sub-Process demands its format just
// as much as one at the top level.
func (t *Thresher) validateScriptCoverage(s *snapshot.Snapshot) error {
	reg := t.cfg.scriptRegistry

	var unmet []string

	s.Walk(func(n flow.Node) bool {
		st, ok := n.(*activities.ScriptTask)
		if !ok {
			return true
		}

		format := st.ScriptFormat()
		if _, claimed := reg.EngineFor(format); !claimed {
			unmet = append(unmet,
				fmt.Sprintf("%q (format %q)", st.Name(), format))
		}

		return true
	})

	if len(unmet) == 0 {
		return nil
	}

	// Name what IS registered next to what is missing: the caller's next move
	// is to wire an engine claiming the format, and a message that reports only
	// the gap leaves them guessing whether the format is unclaimed or misspelled.
	have := "none"
	if f := reg.Formats(); len(f) != 0 {
		have = strings.Join(f, ", ")
	}

	return errs.New(
		errs.M("no script engine claims the format of %d script task(s): %s"+
			" — registered script formats: %s;"+
			" wire one with thresher.WithScriptEngine",
			len(unmet), strings.Join(unmet, "; "), have),
		errs.C(errorClass, errs.InvalidObject),
		errs.D("registered_script_formats", have),
		errs.D("script_engine_kind", reg.Type()))
}
