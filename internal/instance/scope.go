package instance

import (
	"fmt"

	"github.com/dr-dobermann/gobpm/internal/scope"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
)

// instanceScope owns an instance's data-plane wiring: the scope tree rooted at
// the process scope (plane), the root container data path commits target (root),
// and the read-only observe reader host observation reads through (reader). It
// groups what the architecture audit (§2.3) called the Instance's "Scope role"
// into one value, so instance.go no longer carries the data-plane fields and
// their setup inline. The runtime-variable supplier stays the Instance itself
// (RuntimeVar / RuntimeVarNames), passed into load, so instanceScope holds no
// back-reference to the Instance.
type instanceScope struct {
	plane  *scope.Scope
	reader service.DataReader
	root   scope.DataPath
}

// load builds the data plane rooted under parentRoot and commits the process
// properties into the root container scope. Reads under the reserved RUNTIME
// subtree delegate to supplier (the Instance); processName names the instance's
// root scope segment; props are the process's declared properties.
func (sc *instanceScope) load(
	parentRoot scope.DataPath,
	processName string,
	props []*data.Property,
	supplier scope.RuntimeVarsSupplier,
) error {
	root := parentRoot
	if root == scope.EmptyDataPath {
		root = scope.RootDataPath
	}

	var err error

	sc.root, err = root.Append(processName)
	if err != nil {
		return fmt.Errorf("couldn't create instance's scope data path: %w", err)
	}

	sc.plane, err = scope.New(sc.root, supplier)
	if err != nil {
		return fmt.Errorf("couldn't create instance's data plane: %w", err)
	}

	// Build the read-only root data reader once (it backs host observation via
	// the InstanceHandle, SRD-018): an empty frame at the open root scope reads
	// live, so it sees the properties committed just below plus runtime vars.
	reader, ferr := sc.openFrame("observe", "observe")
	if ferr != nil {
		return fmt.Errorf("couldn't build instance data reader: %w", ferr)
	}

	sc.reader = reader

	dd := make([]data.Data, 0, len(props))
	for _, p := range props {
		dd = append(dd, p)
	}

	// A birth-init commit is initial state, not a change — its changed-path
	// set is dropped, no DataChange facts (SRD-044 §4.4).
	_, err = sc.plane.Commit(sc.root, dd...)

	return err
}

// openFrame opens a fresh execution frame at the data plane's open root scope for
// track trackID executing node nodeID. A root frame reads live (it sees committed
// process data plus runtime vars) — it backs the observe reader, the Complex
// gateway guard, and per-node execution. A transient frame is Discarded by its
// caller; the observe frame is kept for the instance's lifetime.
func (sc *instanceScope) openFrame(
	trackID, nodeID string,
) (*scope.Frame, error) {
	return sc.openFrameAt(trackID, nodeID, sc.plane.Root())
}

// openFrameAt opens an execution frame at a specific container scope — a
// track executing inside a sub-process resolves and commits at ITS scope
// (SRD-049 FR-7): reads walk child → parent (§10.4/§10.5.7), Put-produced
// locals land in the child scope and die with it at close.
func (sc *instanceScope) openFrameAt(
	trackID, nodeID string,
	at scope.DataPath,
) (*scope.Frame, error) {
	return scope.NewFrame(trackID, nodeID, at, sc.plane)
}

// bindEventPayload binds the payload carried by a born-from-event start into the
// instance root scope: each item the fired event definition carries is committed
// as a Ready datum keyed by its item id (the msgflow.Bind shape, at root), so a
// downstream node reading that item observes the message payload (SRD-015 §4.4).
func (sc *instanceScope) bindEventPayload(eDef flow.EventDefinition) error {
	items := eDef.GetItemsList()
	if len(items) == 0 {
		return nil
	}

	dd := make([]data.Data, 0, len(items))
	for _, item := range items {
		dd = append(dd, data.MustParameter(item.ID(),
			data.MustItemAwareElement(item, data.ReadyDataState)))
	}

	// Commit returns a self-classifying errs error (container/writable/name
	// checks); pass it through rather than re-wrapping at this internal seam.
	// A birth-init commit is initial state, not a change — its changed-path
	// set is dropped, no DataChange facts (SRD-044 §4.4).
	_, err := sc.plane.Commit(sc.root, dd...)

	return err
}
