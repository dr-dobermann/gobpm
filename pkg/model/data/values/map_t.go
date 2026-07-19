package values

import (
	"github.com/dr-dobermann/gobpm/pkg/errs"
)

// EntryT returns the typed value stored under key, or a classified
// ObjectNotFound error when the entry is absent — the typed twin of Entry
// (the array_t.go helper convention).
func (m *Map[T]) EntryT(key string) (T, error) {
	m.lock.Lock()
	defer m.lock.Unlock()

	v, ok := m.entries[key]
	if !ok {
		var zero T

		return zero, errs.New(
			errs.M("map has no entry %q", key),
			errs.C(errorClass, errs.ObjectNotFound))
	}

	return v, nil
}

// SetEntryT upserts the typed entry under key — the typed twin of SetEntry.
// An empty key is a classified error.
func (m *Map[T]) SetEntryT(key string, value T) error {
	if key == "" {
		return emptyKeyErr("SetEntryT")
	}

	m.lock.Lock()
	defer m.lock.Unlock()

	m.entries[key] = value

	return nil
}
