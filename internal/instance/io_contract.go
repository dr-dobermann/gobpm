package instance

import (
	"context"
	"slices"
	"strings"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/observability"
)

// bindContract binds the launch's delivered data through the process's
// declared input parameters (ADR-040 §2.2, SRD-093 FR-4/FR-5). It runs once,
// at construction, after the raw delivery was committed: for each declared
// input the delivered datum of its name is bound into an instance of the
// DECLARED parameter — the declaration's item as the template, so the value
// is type-checked here — and that parameter replaces the raw datum in the
// root scope. A required input with no datum refuses the launch, and so does
// a delivered datum naming no declared input: with a contract, the boundary
// is strict both ways. A contract-less process (nil IOSpec) is untouched —
// the permissive path of ADR-040 §2.5.
func (inst *Instance) bindContract(cfg *newConfig) error {
	ios := inst.s.IOSpec
	if ios == nil {
		return nil
	}

	inputs := ios.InputSet()

	declared := make(map[string]*data.Parameter, len(inputs))
	for _, in := range inputs {
		declared[in.Name()] = in
	}

	delivered := make(map[string]data.Data, len(cfg.rootData))
	for _, d := range cfg.rootData {
		delivered[d.Name()] = d
	}

	if err := inst.refuseUndeclared(delivered, declared, inputs); err != nil {
		return err
	}

	bound := make([]data.Data, 0, len(inputs))

	for _, in := range inputs {
		d, ok := delivered[in.Name()]
		if !ok {
			if in.IsOptional() {
				continue
			}

			return inst.unboundInput(in.Name(), cfg.bornStartID != "")
		}

		p, err := bindDeclared(in, d)
		if err != nil {
			return errs.New(
				errs.M("process %q: input %q rejects the delivered value",
					inst.s.ProcessName, in.Name()),
				errs.C(errorClass, errs.TypeCastingError),
				errs.D(observability.AttrProcessID, inst.s.ProcessID),
				errs.E(err))
		}

		bound = append(bound, p)
	}

	return inst.sc.bindRootData(bound)
}

// refuseUndeclared refuses a delivered datum the contract does not declare
// (SRD-093 FR-5): a misspelled input fails once, at the boundary, naming
// the stray datum and the declared set, instead of leaving a datum under
// the wrong name and a required input missing.
func (inst *Instance) refuseUndeclared(
	delivered map[string]data.Data,
	declared map[string]*data.Parameter,
	inputs []*data.Parameter,
) error {
	names := make([]string, 0, len(delivered))
	for name := range delivered {
		names = append(names, name)
	}

	slices.Sort(names)

	for _, name := range names {
		if _, ok := declared[name]; !ok {
			return errs.New(
				errs.M("process %q declares no input %q — delivered at launch "+
					"(declared inputs: %s)", inst.s.ProcessName, name,
					paramNames(inputs)),
				errs.C(errorClass, errs.InvalidParameter),
				errs.D(observability.AttrProcessID, inst.s.ProcessID))
		}
	}

	return nil
}

// unboundInput words the required-input refusal (ADR-040 §2.2). An
// event-born launch says why nothing could fill it: the start payload
// reaches a process input only with the event attachment capability.
func (inst *Instance) unboundInput(name string, eventBorn bool) error {
	msg := "process %q: required input %q is unbound at launch"
	if eventBorn {
		msg += " — an event-born launch cannot fill a process input until " +
			"the event data attachment capability lands (#329)"
	}

	return errs.New(
		errs.M(msg, inst.s.ProcessName, name),
		errs.C(errorClass, errs.EmptyNotAllowed),
		errs.D(observability.AttrProcessID, inst.s.ProcessID))
}

// bindDeclared instantiates the declared parameter and binds the delivered
// value into it: the declaration's item is the template (ADR-010 §2.3), so a
// value the declared type cannot hold is refused by the value itself, and
// every later read sees exactly the declared item — Ready, since a value
// arrived.
func bindDeclared(in *data.Parameter, d data.Data) (*data.Parameter, error) {
	// A declared parameter always clones — it was built by the constructor
	// that validates what Clone copies. Said in the form the coverage gate
	// reads.
	iae, err := in.Clone()
	if err != nil {
		return nil, err
	}

	ctx := context.Background()

	if uerr := iae.Value().Update(ctx, d.Value().Get(ctx)); uerr != nil {
		return nil, uerr
	}

	// The item was just accepted by the clone above, and the Ready state
	// exists whenever a value does; the constructor cannot refuse them. Said
	// in the form the coverage gate reads.
	ready, err := data.NewItemAwareElement(iae.ItemDefinition(),
		data.ReadyDataState)
	if err != nil {
		return nil, err
	}

	var opts []data.ParameterOption
	if in.IsOptional() {
		opts = append(opts, data.Optional())
	}

	return data.NewParameter(in.Name(), ready, opts...)
}

// collectOutputs reads the declared outputs from the root scope at normal
// completion and copies them into the instance's result (ADR-040 §2.3,
// SRD-093 FR-8) — the contract's committed values, available after the
// instance is reaped. A required output that is absent or not Ready is the
// fault the caller reports: the process claimed a result it did not produce.
// An optional output not produced is skipped. A contract-less process has
// no result surface and is untouched.
func (ls *loopState) collectOutputs() error {
	inst := ls.inst

	ios := inst.s.IOSpec
	if ios == nil {
		return nil
	}

	outputs := ios.OutputSet()
	result := make([]data.Data, 0, len(outputs))

	for _, out := range outputs {
		d, err := inst.sc.plane.GetData(inst.sc.root, out.Name())
		if err != nil || d.State().Name() != data.ReadyDataState.Name() {
			if out.IsOptional() {
				continue
			}

			return errs.New(
				errs.M("process %q: required output %q is unavailable at "+
					"completion", inst.s.ProcessName, out.Name()),
				errs.C(errorClass, errs.ConditionFailed),
				errs.D(observability.AttrProcessID, inst.s.ProcessID))
		}

		// bound through the DECLARED parameter, exactly as an input is at
		// launch: the declaration's item types the value, so a producer that
		// left the wrong kind of value under the declared name is the same
		// broken promise as a missing one
		bound, err := bindDeclared(out, d)
		if err != nil {
			return errs.New(
				errs.M("process %q: output %q holds a value its declaration "+
					"cannot carry", inst.s.ProcessName, out.Name()),
				errs.C(errorClass, errs.TypeCastingError),
				errs.D(observability.AttrProcessID, inst.s.ProcessID),
				errs.E(err))
		}

		result = append(result, bound)
	}

	inst.result.Store(&result)

	return nil
}

// Outputs returns the instance's declared result — the outputs read at its
// normal completion (SRD-093 FR-9). Empty before completion, after an
// abnormal end, and for a contract-less process. Every call hands out its
// own copy of each value: the result is a committed value, never a live
// view (ADR-040 §2.3a), and a reader mutating what it got must not reach the
// instance's record or another reader.
func (inst *Instance) Outputs() []data.Data {
	stored := inst.result.Load()
	if stored == nil {
		return nil
	}

	out := make([]data.Data, 0, len(*stored))

	for _, d := range *stored {
		cloned, err := cloneNamed(d.Name(), d)
		if err != nil {
			// A collected datum is a Ready clone already, so its value is
			// never nil — the one thing cloneNamed refuses. Said in the form
			// the coverage gate reads; the shared datum is the fallback.
			cloned = d
		}

		out = append(out, cloned)
	}

	return out
}

// paramNames lists parameter names for a message.
func paramNames(params []*data.Parameter) string {
	names := make([]string, 0, len(params))
	for _, p := range params {
		names = append(names, p.Name())
	}

	return strings.Join(names, ", ")
}
