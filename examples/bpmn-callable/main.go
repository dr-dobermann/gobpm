// Command bpmn-callable demonstrates reuse BY REFERENCE across a BPMN
// document boundary (SRD-096, #325):
//
//	go run ./examples/bpmn-callable
//
// The bundled quote.bpmn declares a <globalBusinessRuleTask> — a task defined
// once at definitions level, with no flow of its own — and a process that
// calls it three ways: by its bare key, through a QUALIFIED reference naming
// a callable in another document's namespace, and through a reference
// qualified by the document's OWN namespace.
//
// Every one of those was refused before this landing. What makes them work is
// one idea: a global task IS a callable process, so the registry that already
// serves called processes serves it too — and a reference the engine owns no
// convention for is mapped by the host rather than guessed.
package main

import (
	"context"
	_ "embed"
	"fmt"
	"log"
	"time"

	"github.com/dr-dobermann/gobpm/pkg/convert"
	_ "github.com/dr-dobermann/gobpm/pkg/convert/bpmn"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/thresher"
)

//go:embed quote.bpmn
var quoteBPMN []byte

// sharedNS is the namespace quote.bpmn's <import> declares — the other
// document its `ext:` prefix points into.
const sharedNS = "http://example.com/shared"

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	fmt.Print(`
  bpmn-callable (SRD-096 / #325):
    a <globalBusinessRuleTask> imports as a callable process,
    an unqualified call reaches it by key,
    a QUALIFIED call is mapped by the host's CallableResolver,
    and a self-qualified one collapses to the bare key

`)

	if err := data.CreateDefaultStates(); err != nil {
		return fmt.Errorf("init data states: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// The document yields TWO processes: the one it declares, and the one its
	// global task became. Import returns "the" process of a document and
	// would have to choose, so the document-level call is what reads both.
	res, err := convert.ImportDocument(ctx, convert.BPMN, reader(quoteBPMN))
	if err != nil {
		return fmt.Errorf("import quote.bpmn: %w", err)
	}

	fmt.Printf("  imported %d processes from one document:\n", len(res.Processes))

	for _, p := range res.Processes {
		fmt.Printf("    %-8s %q (%d nodes)\n", p.ID(), p.Name(), len(p.Nodes()))
	}

	// First WITHOUT a resolver, because what the engine does on its own is
	// half the story: the qualified reference has no answer it could invent,
	// and the fault says which namespace a host has to teach it about.
	if uerr := showUnresolvable(ctx, res.Processes); uerr != nil {
		return uerr
	}

	engine, err := thresher.New("bpmn-callable",
		thresher.WithoutBanner(),
		thresher.WithoutStartupConfig(),
		thresher.WithRuleEngine(decisions()),
		// The host's answer to a reference the engine owns no convention
		// for. Without it the qualified call fails, naming the namespace it
		// could not map — which is the honest outcome, not a guess.
		thresher.WithCallableResolver(sharedResolver()))
	if err != nil {
		return fmt.Errorf("create engine: %w", err)
	}

	for _, p := range res.Processes {
		if _, err = engine.RegisterProcess(p); err != nil {
			return fmt.Errorf("register %q: %w", p.ID(), err)
		}
	}

	// The callable the QUALIFIED reference names. It is registered under a key
	// of the host's choosing — "shared.audit", not "audit" — which is exactly
	// why the engine cannot resolve the reference itself.
	audit, err := auditProcess()
	if err != nil {
		return fmt.Errorf("build audit callable: %w", err)
	}

	if _, err = engine.RegisterProcess(audit); err != nil {
		return fmt.Errorf("register audit: %w", err)
	}

	if err = engine.Run(ctx); err != nil {
		return fmt.Errorf("run engine: %w", err)
	}

	return runQuote(ctx, engine)
}
