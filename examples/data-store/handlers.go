package main

import (
	"context"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
)

// itemID is the shared item definition id the writer produces and the reader
// consumes.
const itemID = "counter-id"

// writeOp produces the value the writer task stores into the shared DataStore.
func writeOp() (service.Operation, error) {
	return gooper.New("write-op",
		func(_ context.Context, _ service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			return data.MustItemDefinition(
				values.NewVariable(42), foundation.WithID(itemID)), nil
		})
}

// readOp reads the value the reader task received from the shared DataStore
// (through its DataStoreReference input) and reports it on seen.
func readOp(seen chan<- int) (service.Operation, error) {
	return gooper.New("read-op",
		func(ctx context.Context, r service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			d, err := r.GetDataByID(itemID)
			if err != nil {
				return nil, err
			}

			if v, ok := d.Value().Get(ctx).(int); ok {
				seen <- v
			}

			return nil, nil
		})
}
