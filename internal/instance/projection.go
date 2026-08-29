package instance

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
	var out []Token

	// A nil snapshot is not "no tokens": a restored instance may hold no
	// live track yet still project its incident tokens below.
	if snap := inst.tracksSnap.Load(); snap != nil {
		out = make([]Token, 0, len(*snap))

		for _, t := range *snap {
			tok := t.Token()
			if tok.State == TokenAlive || tok.State == TokenWaitForEvent {
				out = append(out, tok)
			}
		}
	}

	// Incident tokens project from the open incident RECORDS, not from the
	// (terminal, unpersisted) incident tracks (ADR-036 §2.2): the projection
	// then survives a restore, where the track no longer exists but the
	// incident does.
	views := inst.IncidentViews()
	for i := range views {
		if st, ok := incidentStateFromName[views[i].State]; !ok || !st.open() {
			continue
		}

		if node, ok := inst.nodeByID(views[i].NodeID); ok {
			out = append(out, Token{Node: node, State: TokenIncident})
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
