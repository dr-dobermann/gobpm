package scope

import (
	"sort"
	"strings"
	"sync"

	"github.com/dr-dobermann/gobpm/pkg/errs"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
)

// dataFinder reports whether a data.Data item matches a lookup criterion.
type dataFinder func(data.Data) bool

// Scope is the per-instance data plane: the container-scope tree and the
// single authority for persistent process data (ADR-010 §2.2, SRD-007 FR-1).
// Every operation runs atomically under the plane's own mutex — no compound
// operation spans lock acquisitions, so concurrent tracks cannot interleave
// inside one logical operation.
//
// The name resolution (GetData / GetDataByID) walks parent-ward from the
// requested container scope up to the plane's root — the structural
// visibility of BPMN §10.4.
//
// The name is transitional: when the legacy Scope interface retires
// (SRD-007 M4), Scope is renamed to Scope.
type Scope struct {
	scopes  map[DataPath]map[string]data.Data
	sources map[string]data.SourceProvider
	rt      RuntimeVarsSupplier
	root    DataPath
	rtPath  DataPath
	m       sync.Mutex
}

// New creates a data plane rooted at root with the root container scope
// already open. rt serves the reserved read-only RUNTIME subtree (SRD-007
// FR-9); a nil rt is allowed and disables the subtree — the reserved path
// stays write-protected either way.
func New(root DataPath, rt RuntimeVarsSupplier) (*Scope, error) {
	if err := root.Validate(); err != nil {
		return nil,
			errs.New(
				errs.M("New: invalid root path %q", root),
				errs.C(errorClass, errs.InvalidParameter),
				errs.E(err))
	}

	rtPath, err := root.Append(RuntimeVarsSegment)
	if err != nil {
		// unreachable: Append fails only on a blank tail, and the segment
		// is a non-blank constant — a failure here is a programming error.
		errs.Panic(err.Error())
	}

	sources := map[string]data.SourceProvider{}
	if rt != nil {
		sources[RuntimeVarsSegment] = runtimeSource{rt: rt}
	}

	return &Scope{
		scopes: map[DataPath]map[string]data.Data{
			root: {},
		},
		sources: sources,
		rt:      rt,
		root:    root,
		rtPath:  rtPath,
	}, nil
}

// Root returns the root container-scope path of the plane.
func (p *Scope) Root() DataPath {
	return p.root
}

// GetData returns the data named name, resolving from the container scope
// `from` parent-ward to the root. A read addressed exactly at the reserved
// RUNTIME path is served by the RuntimeVarsSupplier (if configured).
func (p *Scope) GetData(from DataPath, name string) (data.Data, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil,
			errs.New(
				errs.M("GetData: an empty data name isn't allowed"),
				errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if err := p.checkContained("GetData", from); err != nil {
		return nil, err
	}

	p.m.Lock()
	defer p.m.Unlock()

	if p.rt != nil && from == p.rtPath {
		return p.rt.RuntimeVar(name)
	}

	return p.getData(
		from, name,
		func(d data.Data) bool {
			return d.Name() == name
		})
}

// SnapshotAt returns value-copies of every scope datum visible from `from`
// (the walk-up surface), for the compensation completion ledger (ADR-026 §2.5,
// SRD-059 FR-4): a handler later reads the world as the completed activity saw
// it, immune to subsequent scope mutation. Runtime-source variables are not
// snapshotted — they are engine-owned, not scope state.
func (p *Scope) SnapshotAt(from DataPath) ([]data.Data, error) {
	if err := p.checkContained("SnapshotAt", from); err != nil {
		return nil, err
	}

	names := p.namesFrom(from)
	snap := make([]data.Data, 0, len(names))

	for _, n := range names {
		d, err := p.GetData(from, n)
		if err != nil {
			return nil, errs.New(
				errs.M("SnapshotAt: couldn't read %q at %q", n, string(from)),
				errs.C(errorClass, errs.OperationFailed),
				errs.E(err))
		}

		c, ok := d.(interface {
			Clone() (*data.ItemAwareElement, error)
		})
		if !ok {
			return nil, errs.New(
				errs.M("SnapshotAt: datum %q at %q isn't clonable",
					n, string(from)),
				errs.C(errorClass, errs.InvalidObject))
		}

		iae, err := c.Clone()
		if err != nil {
			return nil, errs.New(
				errs.M("SnapshotAt: couldn't clone %q at %q", n, string(from)),
				errs.C(errorClass, errs.OperationFailed),
				errs.E(err))
		}

		cp, err := data.NewParameter(d.Name(), iae)
		if err != nil {
			return nil, errs.New(
				errs.M("SnapshotAt: couldn't wrap %q at %q", n, string(from)),
				errs.C(errorClass, errs.OperationFailed),
				errs.E(err))
		}

		snap = append(snap, cp)
	}

	return snap, nil
}

// GetSource resolves addr at the named source, dispatching addr verbatim to
// the provider (its own address space). It never traverses the container tree
// — a source owns its names (ADR-010 v.2 §2.7). An unknown source is an error.
// The registry is built at New and never mutated, so no lock is taken.
func (p *Scope) GetSource(source, addr string) (data.Data, error) {
	prov, ok := p.sources[source]
	if !ok {
		return nil,
			errs.New(
				errs.M("GetSource: unknown data source %q", source),
				errs.C(errorClass, errs.ObjectNotFound))
	}

	return prov.Get(addr)
}

// GetSources lists the registered named-source segments (sorted). The default
// scope is not a named source and is not listed. Lock-free — the registry is
// immutable after New.
func (p *Scope) GetSources() []string {
	ss := make([]string, 0, len(p.sources))
	for s := range p.sources {
		ss = append(ss, s)
	}
	sort.Strings(ss)

	return ss
}

// List enumerates variable names. An empty path lists the default-scope names
// visible from the root (process-level data); a source segment returns that
// provider's Names(); an unknown source is an error.
func (p *Scope) List(path string) ([]string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return p.namesFrom(p.root), nil
	}

	prov, ok := p.sources[path]
	if !ok {
		return nil,
			errs.New(
				errs.M("List: unknown data source %q", path),
				errs.C(errorClass, errs.ObjectNotFound))
	}

	return prov.Names(), nil
}

// namesFrom collects the default-scope data names visible from `from` — the
// data in `from` itself and in every ancestor container scope up to the root
// — deduplicated and sorted.
func (p *Scope) namesFrom(from DataPath) []string {
	p.m.Lock()
	defer p.m.Unlock()

	seen := map[string]struct{}{}
	prefix := from.String() + PathSeparator

	for path, vv := range p.scopes {
		// `path` is visible from `from` when it is `from` itself, an ancestor of
		// `from` (its separator-terminated form prefixes `from`'s), or the root
		// container. The root is special-cased because its string already IS the
		// separator ("/"), so `path + sep` would be "//" and never prefix a
		// child path — without this, a root-keyed scope is dropped (FIX-014 1.4).
		if path == from || path.String() == PathSeparator ||
			strings.HasPrefix(prefix, path.String()+PathSeparator) {
			for n := range vv {
				seen[n] = struct{}{}
			}
		}
	}

	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	sort.Strings(names)

	return names
}

// GetDataByID returns the data whose ItemDefinition id is id, resolving from
// the container scope `from` parent-ward to the root.
func (p *Scope) GetDataByID(from DataPath, id string) (data.Data, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil,
			errs.New(
				errs.M("GetDataByID: an empty ItemDefinition id isn't allowed"),
				errs.C(errorClass, errs.EmptyNotAllowed))
	}

	if err := p.checkContained("GetDataByID", from); err != nil {
		return nil, err
	}

	p.m.Lock()
	defer p.m.Unlock()

	return p.getData(
		from, id,
		func(d data.Data) bool {
			idef := d.ItemDefinition()

			return idef != nil && idef.ID() == id
		})
}

// Commit atomically stores the batch dd into the open container scope at.
// The whole batch is validated before anything is applied, and the
// application happens under one critical section — other plane users observe
// either none or all of the batch (ADR-010 §2.3). An empty batch is a no-op.
//
// Commit returns the committed changed-path set (ADR-011 v.6 §2.9.4, SRD-044):
// per committed name, the prior committed value graph is diffed against the
// incoming one into (path, ChangeType) entries — a first commit of a name is
// one Value_Added at its root. An unchanged re-commit contributes nothing; the
// scope produces the set, its consumers decide what it means.
func (p *Scope) Commit(at DataPath, dd ...data.Data) ([]data.Change, error) {
	if err := p.checkContained("Commit", at); err != nil {
		return nil, err
	}

	if err := p.checkWritable("Commit", at); err != nil {
		return nil, err
	}

	names, err := batchNames("Commit", dd)
	if err != nil {
		return nil, err
	}

	if len(dd) == 0 {
		return nil, nil
	}

	p.m.Lock()
	defer p.m.Unlock()

	vv, ok := p.scopes[at]
	if !ok {
		return nil, errs.New(
			errs.M("Commit: container scope %q isn't open", at),
			errs.C(errorClass, errs.ObjectNotFound))
	}

	var changes []data.Change

	for i, d := range dd {
		var prior data.Value
		if pd, ok := vv[names[i]]; ok {
			prior = pd.Value()
		}

		vv[names[i]] = d

		changes = append(changes,
			data.DiffValues(names[i], prior, d.Value())...)
	}

	return changes, nil
}

// OpenScope opens a child container scope at path. The parent scope must
// already be open (container scopes form a tree rooted at Root).
func (p *Scope) OpenScope(path DataPath) error {
	if err := p.checkContained("OpenScope", path); err != nil {
		return err
	}

	if err := p.checkWritable("OpenScope", path); err != nil {
		return err
	}

	// DropTail can't fail on a path checkContained validated; if it ever
	// did, parent would be empty and fail the parent-is-open check below.
	parent, _ := path.DropTail()

	p.m.Lock()
	defer p.m.Unlock()

	if _, ok := p.scopes[path]; ok {
		return errs.New(
			errs.M("OpenScope: container scope %q is already open", path),
			errs.C(errorClass, errs.DuplicateObject))
	}

	if _, ok := p.scopes[parent]; !ok {
		return errs.New(
			errs.M("OpenScope: parent scope %q of %q isn't open",
				parent, path),
			errs.C(errorClass, errs.ObjectNotFound))
	}

	p.scopes[path] = map[string]data.Data{}

	return nil
}

// CloseScope closes the container scope at path and drops its data. The
// root scope can't be closed, and a scope with open children must have them
// closed first.
func (p *Scope) CloseScope(path DataPath) error {
	if err := p.checkContained("CloseScope", path); err != nil {
		return err
	}

	if path == p.root {
		return errs.New(
			errs.M("CloseScope: the root scope %q can't be closed", path),
			errs.C(errorClass, errs.InvalidParameter))
	}

	p.m.Lock()
	defer p.m.Unlock()

	if _, ok := p.scopes[path]; !ok {
		return errs.New(
			errs.M("CloseScope: container scope %q isn't open", path),
			errs.C(errorClass, errs.ObjectNotFound))
	}

	prefix := path.String() + PathSeparator
	for open := range p.scopes {
		if strings.HasPrefix(open.String(), prefix) {
			return errs.New(
				errs.M("CloseScope: scope %q has an open child %q",
					path, open),
				errs.C(errorClass, errs.ConditionFailed))
		}
	}

	delete(p.scopes, path)

	return nil
}

// opened reports whether the container scope at path is open.
func (p *Scope) opened(path DataPath) bool {
	p.m.Lock()
	defer p.m.Unlock()

	_, ok := p.scopes[path]

	return ok
}

// getData walks from `from` parent-ward to the root, returning the first
// item the finder matches. The caller holds the plane's mutex.
func (p *Scope) getData(
	from DataPath,
	what string,
	finder dataFinder,
) (data.Data, error) {
	path := from

	for {
		if vv, ok := p.scopes[path]; ok {
			for _, d := range vv {
				if finder(d) {
					return d, nil
				}
			}
		}

		if path == p.root {
			break
		}

		var err error

		path, err = path.DropTail()
		if err != nil {
			return nil,
				errs.New(
					errs.M("couldn't walk up from scope %q", path),
					errs.C(errorClass, errs.OperationFailed),
					errs.E(err))
		}
	}

	return nil,
		errs.New(
			errs.M("data %q isn't found from scope %q up to root", what, from),
			errs.C(errorClass, errs.ObjectNotFound))
}

// checkContained validates the path and ensures it addresses the plane's
// container tree (the root itself or a descendant of it).
func (p *Scope) checkContained(op string, path DataPath) error {
	if err := path.Validate(); err != nil {
		return errs.New(
			errs.M("%s: invalid container-scope path %q", op, path),
			errs.C(errorClass, errs.InvalidParameter),
			errs.E(err))
	}

	if path == p.root ||
		strings.HasPrefix(path.String(), p.root.String()+PathSeparator) ||
		p.root == RootDataPath {
		return nil
	}

	return errs.New(
		errs.M("%s: path %q is outside the plane rooted at %q",
			op, path, p.root),
		errs.C(errorClass, errs.InvalidParameter))
}

// checkWritable rejects mutating operations addressed at or under the
// reserved read-only RUNTIME subtree. The namespace is reserved even when no
// RuntimeVarsSupplier is configured.
func (p *Scope) checkWritable(op string, path DataPath) error {
	if path == p.rtPath ||
		strings.HasPrefix(path.String(), p.rtPath.String()+PathSeparator) {
		return errs.New(
			errs.M("%s: %q is the reserved read-only runtime subtree",
				op, path),
			errs.C(errorClass, errs.ConditionFailed))
	}

	return nil
}

// batchNames validates a commit batch — no nil items, no unnamed items —
// and returns the trimmed names, index-aligned with the batch.
func batchNames(op string, dd []data.Data) ([]string, error) {
	names := make([]string, len(dd))

	for i, d := range dd {
		if d == nil {
			return nil,
				errs.New(
					errs.M("%s: a nil data item isn't allowed (index %d)",
						op, i),
					errs.C(errorClass, errs.EmptyNotAllowed))
		}

		name := strings.TrimSpace(d.Name())
		if name == "" {
			return nil,
				errs.New(
					errs.M("%s: a data item with an empty name isn't "+
						"allowed (index %d)", op, i),
					errs.C(errorClass, errs.EmptyNotAllowed))
		}

		names[i] = name
	}

	return names, nil
}
