// Command covercheck is the diff-coverage gate (SRD-002): it fails when the
// source lines a change adds or modifies are covered below a threshold, judging
// only changed lines so the untouched-code coverage backlog never blocks it.
//
// Usage:
//
//	covercheck -min 70 -base origin/master -profiles "coverage.txt"
//
// All logic lives in internal/covercheck (unit-tested to 100%); this is a thin
// flag-parsing shell.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/dr-dobermann/gobpm/internal/covercheck"
)

func main() {
	minPct := flag.Float64("min", 70, "minimum patch coverage percent")
	base := flag.String("base", "origin/master", "base ref to diff against")
	profiles := flag.String("profiles", "coverage.txt",
		"comma-separated coverage profile paths")
	flag.Parse()

	code, err := covercheck.RunGate(os.Stdout, *minPct, *base, *profiles)
	if err != nil {
		fmt.Fprintln(os.Stderr, "covercheck:", err)
		os.Exit(2)
	}

	os.Exit(code)
}
