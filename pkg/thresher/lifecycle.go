package thresher

import (
	"context"
	"errors"
	"fmt"

	"github.com/dr-dobermann/gobpm/pkg/renv"
)

// seam is one wired extension point, paired with the name the engine reports it
// under. The name is the whole reason this type exists: "Stop failed" is not
// actionable, and an operator needs to know WHICH seam refused before deciding
// whether the shutdown was clean.
type seam struct {
	impl any
	name string
}

// lifecycleSeams lists the seams the engine starts and stops, in START order.
//
// The order is a correctness requirement, not tidiness (SRD-088 §3.2):
// telemetry first, so a failure in anything below it is observable; storage
// before the things that write to it; input last, so nothing arrives before the
// machinery that serves it exists. Shutdown walks the reverse — the message
// broker stops accepting before the repository closes, or in-flight state is
// lost, and telemetry flushes after everything it observes.
//
// A generic sweep over an abstract "plugin list" could not express that, which
// is why the engine names its seams here rather than iterating a registry
// (§1.1: the extension surface is a closed list of ports).
//
// Logger, Clock, ExpressionEngine, RuleEngine and ScriptEngine are deliberately
// absent: they hold nothing whose release is the engine's business. An
// implementation of one that does is free to implement Stopper — it simply will
// not be called here, and its owner stops it.
func (t *Thresher) lifecycleSeams() []seam {
	seams := []seam{
		{t.cfg.Tracer(), "Tracer"},
		{t.cfg.MetricsRecorder(), "MetricsRecorder"},
		{t.cfg.Repository(), "Repository"},
		{t.cfg.AuthorizationProvider(), "AuthorizationProvider"},
	}

	for _, st := range t.cfg.registeredStores {
		seams = append(seams, seam{st, "DataStore"})
	}

	return append(seams,
		seam{t.cfg.TaskDistributor(), "TaskDistributor"},
		seam{t.cfg.WorkerDispatcher(), "WorkerDispatcher"},
		seam{t.cfg.MessageBroker(), "MessageBroker"},
	)
}

// startSeams starts every wired seam implementing renv.Starter, in the order
// lifecycleSeams gives. A failure aborts the start and names the seam: an
// extension that cannot start is not a degraded mode.
func (t *Thresher) startSeams(ctx context.Context) error {
	for _, s := range t.lifecycleSeams() {
		st, ok := s.impl.(renv.Starter)
		if !ok {
			continue
		}

		if err := st.Start(ctx); err != nil {
			return fmt.Errorf("starting the %s extension: %w", s.name, err)
		}
	}

	return nil
}

// stopSeams stops every wired seam implementing renv.Stopper, in REVERSE start
// order, and joins the failures.
//
// Joining rather than returning at the first failure is deliberate: abandoning
// the remaining seams leaves live resources with no second chance, and the
// caller is shutting down precisely because it wants them released. The same
// reasoning FIX-038 §1.5 landed on for a failed registration rollback.
func (t *Thresher) stopSeams(ctx context.Context) error {
	seams := t.lifecycleSeams()

	var errs []error

	for i := len(seams) - 1; i >= 0; i-- {
		sp, ok := seams[i].impl.(renv.Stopper)
		if !ok {
			continue
		}

		if err := sp.Stop(ctx); err != nil {
			errs = append(errs,
				fmt.Errorf("stopping the %s extension: %w", seams[i].name, err))
		}
	}

	return errors.Join(errs...)
}
