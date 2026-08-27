package instance

import (
	"context"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/exec"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/observability"
)

// seedTrackID labels the frame the seed opens for a born start's data.
const seedTrackID = "seed"

// associationSource is the slice of a catch event the seed needs: its
// producer role and what is wired to it.
type associationSource interface {
	exec.NodeDataProducer
	OutputAssociations() []*data.Association
}

// runBornStartAssociations runs the born start's output associations over
// the root scope (SRD-094 FR-5): the fired definition's payload — already
// committed by item id — is staged as the frame's received item, the
// start's UploadData pushes it into whatever the associations target, and
// the frame commits. A data object target is already in the scope and is
// updated in place. A declared process input is not there yet — the
// contract binds it after this — so the seed stages a placeholder under the
// input's name for the copy to land in, then hands it to the contract as
// delivered data: the value the message filled binds through the
// declaration exactly as a host-supplied one, type check included (ADR-040
// v.2 §2.7). A start with no association changes nothing.
func (inst *Instance) runBornStartAssociations(
	bornStart flow.Node, cfg *newConfig,
) error {
	src, ok := bornStart.(associationSource)
	if !ok || len(src.OutputAssociations()) == 0 {
		return nil
	}

	// Staging clones declared parameters and commits Ready data to a fresh
	// root; opening a frame at the root of an open plane always succeeds;
	// committing a frame that ran to completion does too. Those three
	// returns are said in the form the coverage gate reads.
	staged, err := inst.stageTargetedInputs(src.OutputAssociations())
	if err != nil {
		return inst.seedErr(bornStart, "couldn't stage the targeted inputs", err)
	}

	f, err := inst.sc.openFrame(seedTrackID, bornStart.ID())
	if err != nil {
		return inst.seedErr(bornStart, "couldn't open the seed frame", err)
	}

	if items := cfg.bornEvent.GetItemsList(); len(items) > 0 {
		f.SetReceived(items[0])
	}

	if err := src.UploadData(context.Background(), f); err != nil {
		f.Discard()

		return inst.seedErr(bornStart,
			"the start's output associations couldn't run", err)
	}

	if _, err := f.Commit(); err != nil {
		return inst.seedErr(bornStart, "couldn't commit the seed frame", err)
	}

	// what the associations filled is what the message delivered
	cfg.rootData = append(cfg.rootData, staged...)

	return nil
}

// stageTargetedInputs commits, under each declared input's name that one
// of the associations targets, a placeholder over the declaration's item
// for the copy to land in, and returns them for the contract to bind.
func (inst *Instance) stageTargetedInputs(
	aa []*data.Association,
) ([]data.Data, error) {
	if inst.s.IOSpec == nil {
		return nil, nil
	}

	declared := map[string]*data.Parameter{}
	for _, in := range inst.s.IOSpec.InputSet() {
		declared[in.Name()] = in
	}

	staged := make([]data.Data, 0, len(aa))

	for _, a := range aa {
		in, ok := declared[a.TargetName()]
		if !ok {
			continue
		}

		// the placeholder carries a fresh copy of the declaration's item —
		// its zero value — and is Ready so the copy path treats it as a
		// datum to update. A declared parameter always clones and the Ready
		// element over its item always builds; the returns are said in the
		// form the coverage gate reads.
		iae, err := in.Clone()
		if err != nil {
			return nil, err
		}

		p, err := data.ReadyParameter(in.Name(), iae.ItemDefinition())
		if err != nil {
			return nil, err
		}

		staged = append(staged, p)
	}

	if len(staged) == 0 {
		return nil, nil
	}

	if _, err := inst.sc.plane.Commit(inst.sc.root, staged...); err != nil {
		return nil, err
	}

	return staged, nil
}

// seedErr classifies a born-start seed failure.
func (inst *Instance) seedErr(start flow.Node, msg string, err error) error {
	return errs.New(
		errs.M("process %q, born start %q: %s", inst.s.ProcessName,
			start.Name(), msg),
		errs.C(errorClass, errs.OperationFailed),
		errs.D(observability.AttrProcessID, inst.s.ProcessID),
		errs.E(err))
}
