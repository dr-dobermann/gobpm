package thresher

import "github.com/dr-dobermann/gobpm/internal/instance/snapshot"

// ProcessRegistration is the receipt for one registered version of a process
// definition (ADR-019 §2.2). RegisterProcess returns it; it names the
// (key, version) that StartProcess and UnregisterProcess address. It is
// read-only — it exposes identity, never the engine-internal snapshot or
// starters it wraps — mirroring InstanceHandle.
type ProcessRegistration struct {
	key      string             // the versioning key = process id
	id       string             // opaque, unique registration id
	snapshot *snapshot.Snapshot // the frozen version of the definition
	starters []*instanceStarter // auto-start starters of this version (nil in manual mode)
	version  int                // 1-based, increments per key in registration order
	manual   bool               // registered WithManualStart
}

// Key returns the versioning key — the process id shared by every version of
// this definition.
func (r *ProcessRegistration) Key() string { return r.key }

// Version returns this registration's 1-based version number within its key.
func (r *ProcessRegistration) Version() int { return r.version }

// ID returns the opaque, unique registration id of this version.
func (r *ProcessRegistration) ID() string { return r.id }
