package main

import (
	"fmt"

	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
)

// signalDef builds a payload-less signal event definition for the given name.
// Catcher and thrower each get their own definition (distinct nodes) — they
// meet by signal NAME, which is how a signal broadcast is matched.
func signalDef(name string) (*events.SignalEventDefinition, error) {
	sig, err := events.NewSignal(name, nil)
	if err != nil {
		return nil, fmt.Errorf("create signal: %w", err)
	}

	return events.NewSignalEventDefinition(sig)
}

// catcherProcess builds start -> catch(signal) -> end: an instance that parks on
// the intermediate catch until the signal is broadcast.
func catcherProcess(id, signal string) (*process.Process, error) {
	def, err := signalDef(signal)
	if err != nil {
		return nil, err
	}

	catch, err := events.NewIntermediateCatchEvent("await-"+signal, def)
	if err != nil {
		return nil, fmt.Errorf("create catch: %w", err)
	}

	return wire(id, catch)
}

// throwerProcess builds start -> throw(signal) -> end: an instance that
// broadcasts the signal to every catcher in reach.
func throwerProcess(id, signal string) (*process.Process, error) {
	def, err := signalDef(signal)
	if err != nil {
		return nil, err
	}

	throw, err := events.NewIntermediateThrowEvent("raise-"+signal, def)
	if err != nil {
		return nil, fmt.Errorf("create throw: %w", err)
	}

	return wire(id, throw)
}

// wire assembles start -> mid -> end into a process.
func wire(id string, mid flow.Element) (*process.Process, error) {
	start, err := events.NewStartEvent("start")
	if err != nil {
		return nil, fmt.Errorf("create start: %w", err)
	}

	end, err := events.NewEndEvent("end")
	if err != nil {
		return nil, fmt.Errorf("create end: %w", err)
	}

	proc, err := process.New(id)
	if err != nil {
		return nil, fmt.Errorf("create process: %w", err)
	}

	for _, e := range []flow.Element{start, mid, end} {
		if err := proc.Add(e); err != nil {
			return nil, fmt.Errorf("add element: %w", err)
		}
	}

	if _, err := flow.Link(start, mid.(flow.SequenceTarget)); err != nil {
		return nil, fmt.Errorf("link start->mid: %w", err)
	}

	if _, err := flow.Link(mid.(flow.SequenceSource), end); err != nil {
		return nil, fmt.Errorf("link mid->end: %w", err)
	}

	return proc, nil
}
