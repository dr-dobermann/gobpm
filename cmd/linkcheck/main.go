// Command linkcheck verifies that every relative link in the repository's
// Markdown resolves to a file that exists (FIX-034 §1.3).
//
// It is a thin wrapper on purpose. The checker's logic lives in
// internal/linkcheck because the diff-coverage gate does not measure files
// under cmd/ — proven by experiment, and diagnosed in FIX-034 §8.3 — so code
// left here would ship ungated. Only this entry point, which cannot be called
// without exiting the process, stays in the blind spot.
package main

import (
	"os"

	"github.com/dr-dobermann/gobpm/internal/linkcheck"
)

func main() {
	os.Exit(linkcheck.Run(os.Args[1:], os.Stdout, os.Stderr))
}
