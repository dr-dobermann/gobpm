package main

import (
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	datastores "github.com/dr-dobermann/gobpm/pkg/model/data_stores"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
)

// storeRef is the id of the engine-global Data Store both processes share.
const storeRef = "shared"

func idef() *data.ItemDefinition {
	return data.MustItemDefinition(
		values.NewVariable(0), foundation.WithID(itemID))
}

// buildWriter builds a process whose task stores 42 into the shared store, via
// a DataStoreReference output association (Node → DataStore).
func buildWriter() (*process.Process, error) {
	op, err := writeOp()
	if err != nil {
		return nil, err
	}

	writer, err := activities.NewServiceTask("writer", op,
		activities.WithParameters(data.Output, data.MustParameter("out",
			data.MustItemAwareElement(idef(), data.UnavailableDataState))))
	if err != nil {
		return nil, err
	}

	ref, err := datastores.New("counter", storeRef, idef(), data.ReadyDataState)
	if err != nil {
		return nil, err
	}

	if err := ref.AssociateSource(writer, []string{itemID}, nil); err != nil {
		return nil, err
	}

	return singleTaskProc("writer-proc", writer)
}

// buildReader builds a process whose task reads "counter" from the shared store
// into its input, via a DataStoreReference input association (DataStore → Node).
func buildReader(seen chan<- int) (*process.Process, error) {
	op, err := readOp(seen)
	if err != nil {
		return nil, err
	}

	reader, err := activities.NewServiceTask("reader", op,
		activities.WithParameters(data.Input, data.MustParameter("in",
			data.MustItemAwareElement(idef(), data.UnavailableDataState))))
	if err != nil {
		return nil, err
	}

	ref, err := datastores.New("counter", storeRef, idef(), data.ReadyDataState)
	if err != nil {
		return nil, err
	}

	if err := ref.AssociateTarget(reader, nil); err != nil {
		return nil, err
	}

	return singleTaskProc("reader-proc", reader)
}

// singleTaskProc wraps task as start → task → end.
func singleTaskProc(name string, task flow.Node) (*process.Process, error) {
	p, err := process.New(name)
	if err != nil {
		return nil, err
	}

	start, err := events.NewStartEvent("start-" + name)
	if err != nil {
		return nil, err
	}

	end, err := events.NewEndEvent("end-" + name)
	if err != nil {
		return nil, err
	}

	for _, e := range []flow.Element{start, task, end} {
		if err := p.Add(e); err != nil {
			return nil, err
		}
	}

	if _, err := flow.Link(start, task.(flow.SequenceTarget)); err != nil {
		return nil, err
	}

	if _, err := flow.Link(task.(flow.SequenceSource), end); err != nil {
		return nil, err
	}

	return p, nil
}
