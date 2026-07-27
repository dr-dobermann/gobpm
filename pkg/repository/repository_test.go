package repository

import (
	"testing"
	"time"
)

func TestStatusIsTerminal(t *testing.T) {
	if StatusActive.IsTerminal() {
		t.Fatal("Active must not be terminal")
	}

	if !StatusCompleted.IsTerminal() || !StatusTerminated.IsTerminal() {
		t.Fatal("Completed and Terminated must be terminal")
	}
}

func TestStatusSuspendedNotTerminal(t *testing.T) {
	if StatusSuspended.IsTerminal() {
		t.Fatal("Suspended is in-flight, not terminal")
	}
}

func TestLeaseExpired(t *testing.T) {
	now := time.Now()

	unowned := Lease{}
	if !unowned.Expired(now) {
		t.Fatal("an unowned lease is expired by definition")
	}

	live := Lease{Owner: "e1", Incarnation: 1, Expiry: now.Add(time.Minute)}
	if live.Expired(now) {
		t.Fatal("a future-expiry lease must hold")
	}

	lapsed := Lease{Owner: "e1", Incarnation: 1, Expiry: now}
	if !lapsed.Expired(now) {
		t.Fatal("an at-expiry lease no longer holds")
	}
}
