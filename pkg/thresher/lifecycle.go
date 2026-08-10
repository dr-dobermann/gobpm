package thresher

import (
	"context"
	"errors"
	"fmt"

	"github.com/dr-dobermann/gobpm/pkg/renv"
	"github.com/dr-dobermann/gobpm/pkg/rules"
	"github.com/dr-dobermann/gobpm/pkg/tasks"
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

// shareRuntime hands the engine's resolved EngineRuntime to every wired seam
// implementing renv.RuntimeAware. It runs once, at the end of New.
func (t *Thresher) shareRuntime() {
	for _, s := range t.lifecycleSeams() {
		if ra, ok := s.impl.(renv.RuntimeAware); ok {
			ra.UseRuntime(&t.cfg)
		}
	}
}

// HealthCheck asks every wired seam implementing renv.HealthChecker whether it
// is presently usable, and joins what they report.
//
// It exists so a host has something to expose: ADR-002 v.2 §8.3 gives the
// readiness answer to the runtime layer, and a capability the engine never
// consults would be another catalog entry nothing honors. Every seam is
// asked even after one fails — a probe reporting the first problem and hiding
// the rest tells an operator to fix one thing at a time.
//
// A seam that does not implement it is not asked, and its silence is not a
// failure: the capability is optional, like the rest of §8.3.
func (t *Thresher) HealthCheck(ctx context.Context) error {
	var errs []error

	for _, s := range t.lifecycleSeams() {
		hc, ok := s.impl.(renv.HealthChecker)
		if !ok {
			continue
		}

		if err := hc.HealthCheck(ctx); err != nil {
			errs = append(errs,
				fmt.Errorf("the %s extension is unhealthy: %w", s.name, err))
		}
	}

	return errors.Join(errs...)
}

// bindCapabilities hands the dispatcher and rule engine the engine-side objects
// they accept, each detected by type assertion — an implementation that does not
// accept one simply is not offered it.
//
// These five are the ad-hoc ancestors of renv.RuntimeAware: each names one
// dependency and one setter, where RuntimeAware hands over the whole resolved
// EngineRuntime and lets the adapter take what it needs. They are kept as they
// are because they are a public contract adapters already implement; a new
// adapter should prefer RuntimeAware.
func (t *Thresher) bindCapabilities() {
	// Bind the producer to the dispatcher (when it accepts one) so the
	// dispatcher's job-lifecycle events land on the same seam (SRD-041 §3.2). A
	// dispatcher without the binder simply does not emit.
	if ob, ok := t.cfg.WorkerDispatcher().(tasks.ReporterBinder); ok {
		ob.BindReporter(t.producer)
	}

	// Bind the engine as the worker dispatcher's completion sink (when it accepts
	// one) so a worker's Complete/Fail routes back to the owning instance by the id
	// embedded in the job id (SRD-036 §4.5). A dispatcher that reaches the engine
	// another way (a remote adapter) need not implement SinkBinder.
	if binder, ok := t.cfg.WorkerDispatcher().(tasks.SinkBinder); ok {
		binder.BindSink(t)
	}

	// Bind the engine's configured logger so the dispatcher's own lifecycle logging
	// uses the embedder's logger rather than its private default (SRD-037). Done
	// after all options are applied, so a WithLogger override is honored.
	if lb, ok := t.cfg.WorkerDispatcher().(tasks.LoggerBinder); ok {
		lb.BindLogger(t.cfg.Logger())
	}

	// Bind the engine's expression engine so the dispatcher can run a Job's
	// ErrorMapper when it classifies a raw fault engine-side (EngineAuthoritative,
	// SRD-038). A dispatcher that never classifies engine-side need not implement
	// ExpressionEngineBinder.
	if eb, ok := t.cfg.WorkerDispatcher().(tasks.ExpressionEngineBinder); ok {
		eb.BindExpressionEngine(t.cfg.ExpressionEngine())
	}

	// Bind the sink into the rule engine's registrar surfaces (SRD-069
	// FR-3): once bound, register/deploy calls on the live engine emit
	// their KindRules audit facts. An engine without the capability
	// simply doesn't emit.
	if rb, ok := t.cfg.RuleEngine().(rules.ReporterBinder); ok {
		rb.BindReporter(t.producer)
	}
}
