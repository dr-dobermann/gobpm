package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sync/atomic"

	"github.com/dr-dobermann/gobpm/pkg/exec"
	"github.com/dr-dobermann/gobpm/pkg/model/activities"
	"github.com/dr-dobermann/gobpm/pkg/model/data"
	"github.com/dr-dobermann/gobpm/pkg/model/data/values"
	"github.com/dr-dobermann/gobpm/pkg/model/events"
	"github.com/dr-dobermann/gobpm/pkg/model/flow"
	"github.com/dr-dobermann/gobpm/pkg/model/foundation"
	"github.com/dr-dobermann/gobpm/pkg/model/process"
	"github.com/dr-dobermann/gobpm/pkg/model/service"
	"github.com/dr-dobermann/gobpm/pkg/model/service/gooper"
	"github.com/dr-dobermann/gobpm/pkg/rules"
	"github.com/dr-dobermann/gobpm/pkg/rules/gorules"
)

// What each callable did, counted where the engine cannot fake it: inside the
// host code the callable runs. Completion alone would not tell us WHICH
// definition a reference reached — these do.
var (
	taxRuns   atomic.Int64
	auditRuns atomic.Int64
)

// decisions registers the decision the imported <globalBusinessRuleTask>
// names. Its body runs in this program, so incrementing here is direct
// evidence that the imported callable — not some other process — executed.
func decisions() rules.Engine {
	reg := gorules.New()

	if err := reg.Register("tax",
		func(_ context.Context, _ service.DataReader) (rules.Row, error) {
			taxRuns.Add(1)

			return rules.Row{"rate": values.NewVariable(20)}, nil
		}); err != nil {
		panic(fmt.Sprintf("register decision: %v", err))
	}

	return reg
}

// reader wraps the embedded document for the convert façade.
func reader(b []byte) io.Reader { return bytes.NewReader(b) }

// sharedResolver maps a callable reference onto the key this host registered
// it under.
//
// This is the whole seam. The document says "audit, in namespace
// http://example.com/shared"; this program registered that callable as
// "shared.audit". Nothing in BPMN says how one becomes the other — the
// standard types calledElement a plain String and fixes no convention — so
// the engine refuses to guess and asks the host, which is the only party that
// knows what it registered.
//
// An unqualified reference falls through to its own key, which is what the
// engine's default resolver does and what keeps every existing call
// unchanged.
func sharedResolver() exec.CallableResolver {
	return exec.CallableResolverFunc(
		func(_ context.Context, ref exec.CallableRef) (string, error) {
			if ref.Namespace == sharedNS {
				return "shared." + ref.Key, nil
			}

			if ref.Namespace != "" {
				return "", fmt.Errorf(
					"this host serves no callables of namespace %q",
					ref.Namespace)
			}

			return ref.Key, nil
		})
}

// auditProcess builds the callable the QUALIFIED reference names, registered
// under the host's own key rather than the document's.
//
// It is built in Go on purpose: a callable reached across a document boundary
// need not have come from a document at all, which is the point of resolving
// the reference rather than following it.
func auditProcess() (*process.Process, error) {
	p, err := process.New("audit", foundation.WithID("shared.audit"))
	if err != nil {
		return nil, fmt.Errorf("new process: %w", err)
	}

	start, err := events.NewStartEvent("start")
	if err != nil {
		return nil, fmt.Errorf("start: %w", err)
	}

	op, err := gooper.New("record",
		func(_ context.Context, _ service.DataReader,
			_ *data.ItemDefinition) (*data.ItemDefinition, error) {
			auditRuns.Add(1)

			return nil, nil
		})
	if err != nil {
		return nil, fmt.Errorf("record operation: %w", err)
	}

	record, err := activities.NewServiceTask("record", op,
		activities.WithoutParams())
	if err != nil {
		return nil, fmt.Errorf("record: %w", err)
	}

	end, err := events.NewEndEvent("end")
	if err != nil {
		return nil, fmt.Errorf("end: %w", err)
	}

	for _, e := range []flow.Element{start, record, end} {
		if err = p.Add(e); err != nil {
			return nil, fmt.Errorf("add %q: %w", e.Name(), err)
		}
	}

	if _, err = flow.Link(start, record); err != nil {
		return nil, fmt.Errorf("link start: %w", err)
	}

	if _, err = flow.Link(record, end); err != nil {
		return nil, fmt.Errorf("link record: %w", err)
	}

	return p, nil
}
