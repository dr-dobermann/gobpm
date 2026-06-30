package thresher

import "sync"

// keyLockManager hands out one mutex per process key, so that the register and
// unregister operations of a given key serialize against each other
// (FIX-013 §1.4). It guards only its own map; the per-key mutexes it returns are
// held by callers across a whole RegisterProcess / UnregisterVersion /
// UnregisterProcess method — registry mutation AND the paired EventHub work — so
// the two cannot interleave in the window that would orphan a live starter
// (a registration removed from the registry while its starters are still being
// subscribed onto the hub).
//
// It is deliberately a lock distinct from Thresher.m: m is the brief registry-map
// lock taken inside the "…Locked" helpers, whereas a per-key lock spans the
// hub work too. Because State() never acquires a per-key lock, holding one adds
// no re-entrant self-deadlock vector (the FIX-002 RC2 class).
//
// Per-key mutexes are retained for the engine's lifetime (keys are process ids —
// a small, bounded set); they are never deleted, which keeps acquisition free of
// a delete-vs-acquire race.
type keyLockManager struct {
	locks map[string]*sync.Mutex
	mu    sync.Mutex
}

// newKeyLockManager builds an empty per-key lock manager.
func newKeyLockManager() *keyLockManager {
	return &keyLockManager{
		locks: map[string]*sync.Mutex{},
	}
}

// get returns the mutex for key, creating it on first use. The returned mutex is
// NOT locked — the caller locks and unlocks it.
func (m *keyLockManager) get(key string) *sync.Mutex {
	m.mu.Lock()
	defer m.mu.Unlock()

	l, ok := m.locks[key]
	if !ok {
		l = &sync.Mutex{}
		m.locks[key] = l
	}

	return l
}

// lockKey acquires the per-key serialization lock for key and returns its unlock
// function, so a caller serializes a whole key operation with
// `defer t.lockKey(key)()`. The lock is distinct from t.m and is never taken by
// State(), so it adds no RC2 re-entrancy vector (FIX-013 §1.4).
func (t *Thresher) lockKey(key string) func() {
	l := t.keyLocks.get(key)
	l.Lock()

	return l.Unlock
}
