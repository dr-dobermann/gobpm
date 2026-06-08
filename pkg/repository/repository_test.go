package repository

import "testing"

func TestStatusIsTerminal(t *testing.T) {
	if StatusActive.IsTerminal() {
		t.Fatal("Active must not be terminal")
	}

	if !StatusCompleted.IsTerminal() || !StatusTerminated.IsTerminal() {
		t.Fatal("Completed and Terminated must be terminal")
	}
}
