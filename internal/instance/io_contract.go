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
	iae, err := in.Clone()
	if err != nil {
		return nil, err
	}

	ctx := context.Background()

	if uerr := iae.Value().Update(ctx, d.Value().Get(ctx)); uerr != nil {
		return nil, uerr
	}

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

// paramNames lists parameter names for a message.
func paramNames(params []*data.Parameter) string {
	names := make([]string, 0, len(params))
	for _, p := range params {
		names = append(names, p.Name())
	}

	return strings.Join(names, ", ")
}
