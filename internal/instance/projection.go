package instance

import ()

// addToSnap appends a track to the lock-free tracks snapshot (copy-on-write).
// Called only from loop() (the single writer); readers Load the snapshot.
func (inst *Instance) addToSnap(t *track) {
	old := inst.tracksSnap.Load()

	var base []*track
	if old != nil {
		base = *old
	}

	next := make([]*track, len(base), len(base)+1)
	copy(next, base)
	next = append(next, t)

	inst.tracksSnap.Store(&next)
}

// GetTokens returns the projected tokens of the instance's ACTIVE tracks
// (those whose token is Alive or WaitForEvent), derived lock-free from the
// tracks snapshot.
func (inst *Instance) GetTokens() []Token {
	snap := inst.tracksSnap.Load()
	if snap == nil {
		return nil
	}

	out := make([]Token, 0, len(*snap))
	for _, t := range *snap {
		tok := t.Token()
		if tok.State == TokenAlive || tok.State == TokenWaitForEvent {
			out = append(out, tok)
		}
	}

	return out
}

// TokenHistory returns the token-flow path history of the instance — one path
// per track (live and ended), stitched by track lineage — derived lock-free
// from the tracks snapshot and each track's recorded transitions.
func (inst *Instance) TokenHistory() []TokenPath {
	snap := inst.tracksSnap.Load()
	if snap == nil {
		return nil
	}

	out := make([]TokenPath, 0, len(*snap))
	for _, t := range *snap {
		out = append(out, t.path())
	}

	return out
}
