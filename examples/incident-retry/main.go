// Command incident-retry demonstrates the incident lifecycle (ADR-036): a
// payment call fails, the incident retry policy re-enters it once on its own,
// the second failure exhausts the policy — and the incident waits for an
// operator, who inspects its cause, attempts and failure-time data snapshot,
// then retries it to completion. The instance survives the whole ordeal: no
// failure ever terminates it.
package main

import (
	"fmt"
	"os"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	banner()

	h, cleanup, err := start()
	if err != nil {
		return err
	}
	defer cleanup()

	state, view, err := operate(h)
	if err != nil {
		return err
	}

	return check(state, view)
}

func banner() {
	fmt.Println("incident-retry — a technical failure becomes a durable,")
	fmt.Println("operable incident instead of an instance death (ADR-036):")
	fmt.Println("  charge fails → policy retries → fails again → operator")
	fmt.Println("  inspects the incident and retries it → the process completes")
	fmt.Println()
}
