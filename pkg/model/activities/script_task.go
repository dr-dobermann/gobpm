package activities

import (
	"context"
	"sort"
	"strconv"
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/exec"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/options"
	"github.com/dr-dobermann/gobpm/pkg/observability"
	"github.com/dr-dobermann/gobpm/pkg/renv"
	"github.com/dr-dobermann/gobpm/pkg/script"
)

// ScriptTask is a BPMN script task (the extract's §ScriptTask clause): on
// activation the associated script is invoked on the Script Engine claiming
// the task's scriptFormat (ADR-031 §2.1 — the format routes between the
// registered engines); on the script's completion the task completes,
// committing the script's named outputs to process data. The script body is
// opaque to the model — the wired interpreter (e.g. adapters/lua) executes
// it, so the same model runs under whichever engines the embedder
// registered.
type ScriptTask struct {
	scriptFormat string
	script       string

	task
}

// NewScriptTask creates a ScriptTask running script in the scriptFormat
// dialect, with name and foundation/activity options. All three are
// required: the metamodel carries scriptFormat and script as 0..1 for
// interchange, but a scriptless Script Task in a programmatic model is a
// bug — fail fast (SRD-064 §4.4).
func NewScriptTask(
	name, format, body string,
	opts ...options.Option,
) (*ScriptTask, error) {
	format = strings.TrimSpace(format)
	if format == "" {
		return nil, errs.New(
			errs.M("NewScriptTask: an empty script format isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	body = strings.TrimSpace(body)
	if body == "" {
		return nil, errs.New(
			errs.M("NewScriptTask: an empty script isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed))
	}

	t, err := newTask(strings.TrimSpace(name), opts...)
	if err != nil {
		return nil, errs.New(
			errs.M("script task building failed"),
			errs.C(errorClass, errs.BulidingFailed),
			errs.E(err))
	}

	return &ScriptTask{
		scriptFormat: format,
		script:       body,
		task:         *t,
	}, nil
}

// ScriptFormat returns the script's format MIME hint.
func (st *ScriptTask) ScriptFormat() string {
	return st.scriptFormat
}

// Script returns the script body.
func (st *ScriptTask) Script() string {
	return st.script
}

// ----------------------- flow.Node interface --------------------------------

// Node returns the ScriptTask as a flow node.
func (st *ScriptTask) Node() flow.Node {
	return st
}

// Clone returns a per-iteration copy of the ScriptTask (a fresh activity
// shell over the shared config).
func (st *ScriptTask) Clone() (flow.Node, error) {
	t, err := st.clone()
	if err != nil {
		return nil, err
	}

	return &ScriptTask{
		scriptFormat: st.scriptFormat,
		script:       st.script,
		task:         t,
	}, nil
}

// ------------------------ flow.Task interface -------------------------------

// TaskType returns the task type for ScriptTask.
func (st *ScriptTask) TaskType() flow.TaskType {
	return flow.ScriptTask
}

// ----------------------exec.NodeExecutor interface --------------------------

// Exec routes the script by its format to the registered Script Engine,
// runs it against the task's own read surface and commits the script's
// named outputs — each as its own Ready datum, in sorted name order
// (deterministic; ADR-031 §2.3). A routing miss or a script failure fails
// the task through the ordinary fault machinery.
func (st *ScriptTask) Exec(
	ctx context.Context,
	re renv.RuntimeEnvironment,
) ([]*flow.SequenceFlow, error) {
	if re == nil {
		return nil, errs.New(
			errs.M("ScriptTask.Exec: a nil RuntimeEnvironment isn't allowed"),
			errs.C(errorClass, errs.EmptyNotAllowed),
			errs.D(observability.AttrNodeName, st.Name()),
			errs.D(observability.AttrNodeID, st.ID()))
	}

	eng := re.ScriptEngine()

	st.reportScript(re, observability.PhaseInvoked, map[string]string{
		observability.AttrScriptFormat:   st.scriptFormat,
		observability.AttrImplementation: st.routedKind(eng),
	})

	outs, err := eng.Execute(ctx, st.scriptFormat, st.script, re)
	if err != nil {
		st.reportScript(re, observability.PhaseFailed, map[string]string{
			observability.AttrScriptFormat:   st.scriptFormat,
			observability.AttrImplementation: st.routedKind(eng),
			observability.AttrStage:          "engine",
			observability.AttrError:          err.Error(),
		})

		return nil, err
	}

	for _, name := range sortedNames(outs) {
		if err := st.commitOutput(name, outs[name], re); err != nil {
			// The pair still closes: the script ran, its output commit
			// did not (SRD-069 FR-2).
			st.reportScript(re, observability.PhaseFailed,
				map[string]string{
					observability.AttrScriptFormat:   st.scriptFormat,
					observability.AttrImplementation: st.routedKind(eng),
					observability.AttrStage:          "commit",
					observability.AttrError:          err.Error(),
				})

			return nil, err
		}
	}

	st.reportScript(re, observability.PhaseExecuted, map[string]string{
		observability.AttrScriptFormat:   st.scriptFormat,
		observability.AttrImplementation: st.routedKind(eng),
		observability.AttrOutputCount:    strconv.Itoa(len(outs)),
	})

	return st.selectOutgoing(ctx, re)
}

// routedKind names the engine that answers the task's format: the routed
// engine's kind when the surface is the core registry, else the engine's
// own kind (SRD-064 §4.1).
func (st *ScriptTask) routedKind(eng script.Engine) string {
	if reg, ok := eng.(*script.Registry); ok {
		if e, found := reg.EngineFor(st.scriptFormat); found {
			return e.Type()
		}
	}

	return eng.Type()
}

// commitOutput commits one named script output as a Ready datum.
func (st *ScriptTask) commitOutput(
	name string, value data.Value, re renv.RuntimeEnvironment,
) error {
	wrap := func(err error) error {
		return errs.New(
			errs.M("couldn't commit script output"),
			errs.C(errorClass),
			errs.E(err),
			errs.D(observability.AttrNodeName, st.Name()),
			errs.D(observability.AttrNodeID, st.ID()),
			errs.D("output", name))
	}

	res, err := data.ReadyValueParameter(name, value)
	if err != nil {
		return wrap(err)
	}

	if err := re.Put(res); err != nil {
		return wrap(err)
	}

	return nil
}

// sortedNames returns the output names in sorted order — Go map iteration
// would make commit order (and any failure) non-deterministic.
func sortedNames(outs script.Outputs) []string {
	names := make([]string, 0, len(outs))
	for n := range outs {
		names = append(names, n)
	}

	sort.Strings(names)

	return names
}

// reportScript announces a KindScript fact (SRD-064 FR-5): format, engine
// kind and output count only — never script source or output values (the
// masking rule).
func (st *ScriptTask) reportScript(
	re renv.RuntimeEnvironment,
	phase observability.Phase,
	details map[string]string,
) {
	re.Reporter().Report(observability.Fact{
		Kind:     observability.KindScript,
		Phase:    phase,
		NodeID:   st.ID(),
		NodeName: st.Name(),
		Details:  details,
	})
}

// ----------------------------------------------------------------------------

// interfaces check
var (
	_ flow.Node         = (*ScriptTask)(nil)
	_ flow.Task         = (*ScriptTask)(nil)
	_ exec.NodeExecutor = (*ScriptTask)(nil)
)
