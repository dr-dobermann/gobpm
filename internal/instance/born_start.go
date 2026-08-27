package instance

import (
	"context"

	"github.com/dr-dobermann/gobpm/pkg/datastore"
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

// noFlush is the flush of a seed that wrote nothing outside the instance.
func noFlush() error { return nil }

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
// v.2 §2.7). A Data Store target is not written here: the store is
// engine-global and the contract may still refuse the launch, so its writes
// are deferred to the returned flush, which the caller runs once the
// contract accepted. A start with no association changes nothing.
func (inst *Instance) runBornStartAssociations(
	bornStart flow.Node, cfg *newConfig,
) (flush func() error, err error) {
	src, ok := bornStart.(associationSource)
	if !ok || len(src.OutputAssociations()) == 0 {
		return noFlush, nil
	}

	// Staging clones declared parameters and commits Ready data to a fresh
	// root; opening a frame at the root of an open plane always succeeds;
	// committing a frame that ran to completion does too. Those three
	// returns are said in the form the coverage gate reads.
	staged, err := inst.stageTargetedInputs(src.OutputAssociations())
	if err != nil {
		return nil, inst.seedErr(bornStart, "couldn't stage the targeted inputs", err)
	}

	f, err := inst.sc.openFrame(seedTrackID, bornStart.ID())
	if err != nil {
		return nil, inst.seedErr(bornStart, "couldn't open the seed frame", err)
	}

	deferred := &deferringStores{real: inst.sc.dataStores}
	f.SetDataStores(deferred)

	if items := cfg.bornEvent.GetItemsList(); len(items) > 0 {
		f.SetReceived(items[0])
	}

	ctx := context.Background()

	if err := src.UploadData(ctx, f); err != nil {
		f.Discard()

		return nil, inst.seedErr(bornStart,
			"the start's output associations couldn't run", err)
	}

	if _, err := f.Commit(); err != nil {
		return nil, inst.seedErr(bornStart, "couldn't commit the seed frame", err)
	}

	// what the associations filled is what the message delivered
	cfg.rootData = append(cfg.rootData, staged...)

	return func() error { return deferred.flush(ctx) }, nil
}

// stageTargetedInputs commits, under each declared input's name that one
// of the scope-targeted associations names, a placeholder over the
// declaration's item for the copy to land in, and returns them for the
// contract to bind. A Data Store association is not a scope target — its
// reference's name is the store's key, whatever it is — and two
// associations naming one input stage it once.
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
	seen := map[string]bool{}

	for _, a := range aa {
		if a.DataStoreRef() != "" {
			continue
		}

		in, ok := declared[a.TargetName()]
		if !ok || seen[in.Name()] {
			continue
		}

		seen[in.Name()] = true

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

// deferringStores is the Data Store registry the seed frame sees: reads
// go through to the real stores, writes are recorded and replayed by flush
// — after the contract accepted the launch, so a refused launch leaves the
// engine-global stores untouched.
type deferringStores struct {
	real datastore.Registry
	puts []deferredPut
}

// deferredPut is one recorded store write.
type deferredPut struct {
	store datastore.DataStore
	datum data.Data
	key   string
}

// Store resolves ref through the real registry and wraps the store so its
// Put is recorded. A nil registry stays nil: the copy path reports the
// missing registry itself.
func (d *deferringStores) Store(ref string) (datastore.DataStore, error) {
	if d.real == nil {
		return nil, errs.New(
			errs.M("no Data Store registry wired"),
			errs.C(errorClass, errs.ObjectNotFound))
	}

	store, err := d.real.Store(ref)
	if err != nil {
		return nil, err
	}

	return &deferringStore{DataStore: store, owner: d}, nil
}

// flush replays the recorded writes onto the real stores.
func (d *deferringStores) flush(ctx context.Context) error {
	for _, p := range d.puts {
		if err := p.store.Put(ctx, p.key, p.datum); err != nil {
			return err
		}
	}

	return nil
}

// deferringStore is one wrapped store: Get reads through, Put records.
type deferringStore struct {
	datastore.DataStore
	owner *deferringStores
}

// Put records the write for the flush instead of performing it.
func (s *deferringStore) Put(_ context.Context, key string, datum data.Data) error {
	s.owner.puts = append(s.owner.puts,
		deferredPut{store: s.DataStore, key: key, datum: datum})

	return nil
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
