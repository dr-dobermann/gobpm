package data_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
)

// TestReservedNameIsRefusedWhereItIsWritten (SRD-090.D T-3, FR-6): a model
// declaring a name the engine publishes is refused at construction, naming the
// element.
//
// This is what protects the iteration counts, which stay on the writable data
// plane because a completionCondition and a composite instance's body both
// read them by walk-up (ADR-025 §2.9.2). Without the refusal a declared output
// named numberOfCompletedInstances commits to the very scope the engine binds
// the count at, and the condition then stops on a number the model chose —
// silently, and with the wrong answer surfacing far from its cause. A located
// error beats that: it lands on the line that wrote the name.
func TestReservedNameIsRefusedWhereItIsWritten(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	item := func(t *testing.T) *data.ItemDefinition {
		t.Helper()

		return data.MustItemDefinition(values.NewVariable(1),
			foundation.WithID("v"))
	}

	require.Len(t, data.ReservedNames(), 10,
		"the set is CLOSED and stays so: ten engine-owned names, not a "+
			"namespace or a prefix match. A model reading the list gets the "+
			"whole vocabulary it may not declare")

	for _, name := range data.ReservedNames() {
		t.Run(name, func(t *testing.T) {
			_, err := data.NewProperty(name, item(t), data.ReadyDataState)
			require.Error(t, err,
				"a process property may not shadow an engine value")
			require.Contains(t, err.Error(), name,
				"the error names the element, so the modeler can find it")

			p, perr := data.NewParameter(name,
				data.MustItemAwareElement(item(t), data.ReadyDataState))
			require.NoError(t, perr,
				"building a parameter is how the ENGINE publishes its own "+
					"values, so the constructor itself must not refuse")

			ios, ierr := data.NewIOSpec()
			require.NoError(t, ierr)

			require.Error(t, ios.AddParameter(p, data.Output),
				"but DECLARING one on an activity is a model's name, and an "+
					"output commits to the enclosing scope — the exact site "+
					"the count occupies")
		})
	}
}

// TestAnOrdinaryNameIsUnaffected (SRD-090.D NFR-5): the refusal is narrow.
// A model that does not touch the engine's vocabulary builds exactly as before.
func TestAnOrdinaryNameIsUnaffected(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	for _, name := range []string{
		"loop_counter", "LoopCounter", "myLoopCounter", "iterations",
		"order", "numberOfThings",
	} {
		t.Run(name, func(t *testing.T) {
			require.NoError(t, data.CheckReservedName(name, "TEST"),
				"only the exact engine names are reserved — a refusal that "+
					"caught look-alikes would reject legitimate models")
		})
	}
}

// TestAStructuralFieldMayUseAReservedName (SRD-090.D FR-6): a field inside a
// value is reached through its owning name (`order.loopCounter`) and can
// shadow nothing, so refusing it would reject a legitimate model for no gain.
func TestAStructuralFieldMayUseAReservedName(t *testing.T) {
	require.NoError(t, data.CreateDefaultStates())

	rec, err := values.NewRecord(
		values.F(data.LoopCounterName, values.NewVariable(1)),
		values.F("other", values.NewVariable(2)))
	require.NoError(t, err,
		"a record field is not a top-level name and cannot shadow an "+
			"engine value")
	require.NotNil(t, rec)
}
