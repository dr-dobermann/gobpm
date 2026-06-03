// Package main is the placeholder for the gobpm-server binary.
//
// This binary will host the standalone runtime layer per ADR-004
// (docs/design/ADR-004-runtime-environment-contract.md). At SRD-001
// landing time it is a stub: it prints a placeholder message and exits.
//
// Subsequent SRDs will fill in Phase 1 startup (Logger wiring),
// configuration loading, engine construction, HTTP / gRPC listeners,
// and so on — per ADR-004 §4.3.
package main

import (
	"fmt"
	"os"
)

const helpText = `gobpm-server — placeholder

This is the stub binary for the goBpm runtime layer. Real functionality
will be added per the SRD sequence implementing ADR-004
(see docs/design/ADR-004-runtime-environment-contract.md).

Usage:
  gobpm-server           print placeholder and exit
  gobpm-server --help    print this help (also -h)`

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "--help", "-h":
			fmt.Fprintln(os.Stderr, helpText)

			return
		default:
			fmt.Fprintf(os.Stderr, "unrecognized argument: %s\n\n", os.Args[1])
			fmt.Fprintln(os.Stderr, helpText)
			os.Exit(2)
		}
	}

	fmt.Fprintln(os.Stderr, "gobpm-server: placeholder build — runtime not yet implemented")
	fmt.Fprintln(os.Stderr, "see docs/design/ADR-004-runtime-environment-contract.md for the planned scope")
}
