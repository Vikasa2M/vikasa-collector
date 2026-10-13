// Package app wires config → adapters → runners → synth → emitters →
// publisher: the collector spine.
package app

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/Vikasa2M/vikasa-collector/internal/cloudevents"
	"github.com/Vikasa2M/vikasa-collector/internal/config"
	"github.com/Vikasa2M/vikasa-collector/internal/publish"
	"github.com/Vikasa2M/vikasa-collector/internal/runner"
	"github.com/Vikasa2M/vikasa-collector/internal/subject"
	"github.com/Vikasa2M/vikasa-collector/internal/synth"
	"github.com/Vikasa2M/vikasa-collector/internal/wire"
	"github.com/Vikasa2M/vikasa-collector/internal/wire/health"
	"github.com/Vikasa2M/vikasa-collector/internal/wire/openits"
	"github.com/Vikasa2M/vikasa-collector/sdk/adapter"
	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

// drainTimeout bounds how long shutdown will wait for in-flight publishes to
// land before abandoning them. Local JetStream is same-box, so this only ever
// elapses when the broker is genuinely unreachable.
const drainTimeout = 5 * time.Second

// closeTimeout bounds how long shutdown waits for adapters to close. Close
// is third-party code; a supervisor waiting on the process to exit must not
// wait on an adapter bug. A var so tests can shorten it.
var closeTimeout = 2 * time.Second

// Options carries what Run needs beyond the validated config and registry.
type Options struct {
	// NATSURL is the cabinet-local broker.
	NATSURL string
	// Version is stamped into collector-started.
	Version string
	// Logger receives every line the collector writes. Nil means
	// slog.Default(); main always sets it, so its flags decide format and
	// level.
	Logger *slog.Logger
}

// Run starts the collector and blocks until ctx is cancelled.
func Run(ctx context.Context, cfg *config.Config, reg *adapter.Registry, opts Options) error {
	log := opts.Logger
	if log == nil {
		log = slog.Default()
	}
	tenant := cfg.Tenant()

	// Emitter chain: first claim wins. openits goes first so catalog events
	// are claimed there; health keeps the collector-owned schema (ADR 0007)
	// and is never claimed by openits.
	//
	// Events still reach the loud-drop path below — deliberately. The openits
	// emitter declines anything it cannot encode faithfully (a controller mode
	// with no upstream identity, a shared event on an unserved device kind),
	// so the drop is the visible outcome of a mapping gap rather than evidence
	// the chain is unwired.
	catalog, err := catalogEmitter(cfg.ModelVersion, cfg.CollectorID)
	if err != nil {
		return err
	}
	emitters := []wire.Emitter{catalog, health.NewHealthEmitter()}

	tmpl, err := subject.New(cfg.SubjectConfig(), cfg.SubjectIdentity())
	if err != nil {
		return err
	}
	// Exhaustive, not sampled: every ce-type any emitter can produce must
	// render to a legal subject inside its namespace's binding. A grammar
	// mistake fails here, at boot, rather than the first time a rare event
	// fires. The same list drives stream provisioning below, so the streams
	// and the validated ce-types cannot drift: a namespace with no stream
	// would publish into a subject space nothing captures, and the events
	// would vanish with no error at any layer.
	var ceTypes []string
	for _, em := range emitters {
		ceTypes = append(ceTypes, em.CETypes()...)
	}
	if err := tmpl.ValidateCETypes(ceTypes, cfg.DeviceIDs()); err != nil {
		return err
	}

	// Every adapter is built before anything is announced or started, so a
	// device that cannot be built fails boot cleanly: no runner is polling,
	// no collector-started has gone out, and the adapters already built are
	// closed. Building inside the runner-start loop left devices ahead of
	// the broken one polling against a publisher Run was about to close.
	readers, err := buildReaders(log, cfg.Devices, reg)
	if err != nil {
		return err
	}
	defer closeAll(log, readers)

	pub, err := publish.Connect(ctx, opts.NATSURL, tmpl, ceTypes)
	if err != nil {
		return err
	}
	defer pub.Close()

	// Publishing deliberately does NOT ride ctx: a poll that succeeded just as
	// shutdown arrived has already taken a real reading off a real device, and
	// cancelling its publish would throw that reading away. pubCtx outlives ctx
	// by a bounded drain (below) so those land.
	pubCtx, cancelPub := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelPub()

	sink := func(events []model.Event) {
		for _, ev := range events {
			encodeAndPublish(pubCtx, log, pub, tenant, emitters, ev)
		}
	}

	// Boot event.
	sink([]model.Event{model.CollectorStarted{
		Base: model.Base{OccurredAt: time.Now().UTC()}, Version: opts.Version,
	}})

	engine := synth.NewEngine(
		synth.NewSignalDiffer(),
		synth.NewFaultDiffer(),
		synth.NewDetectorDiffer(),
		synth.NewDMSDiffer(),
		synth.NewDMSEnvironmentDiffer(),
		synth.NewTrafficIntervalDiffer(),
		synth.NewZoneIncidentDiffer(),
		synth.NewZoneIntervalDiffer(),
		synth.NewCCTVDiffer(),
	)
	engine.WithRejectHook(func(deviceID string, kind model.Kind, got string) {
		// An adapter bug, not a device fault: the facet has the right kind but
		// not the type its differ diffs. Rejected rather than diffed, so the
		// poll loop survives and the previous state stands.
		log.Warn("facet rejected: wrong type for its kind",
			"device", deviceID, "kind", string(kind), "type", got)
	})

	var wg sync.WaitGroup
	for i, d := range cfg.Devices {
		r := runner.New(readers[i], d.ID, d.PollInterval, 0, engine, sink)
		wg.Go(func() { r.Run(ctx) })
	}

	<-ctx.Done()

	// Runners call sink synchronously, so wg.Wait returning means every event
	// they produced has been through publish. The timer bounds that wait: a
	// dead broker must not hang shutdown forever, so the drain window is the
	// most we will spend trying to save in-flight readings.
	drain := time.AfterFunc(drainTimeout, cancelPub)
	wg.Wait()
	drain.Stop()
	cancelPub()

	return nil
}

// catalogEmitter selects the catalog emitter for model_version (ADR 0005).
// config has already rejected anything outside wire.SupportedModelVersions;
// the error path is for a version listed there with no case here, which
// TestEverySupportedModelVersionSelectsAnEmitter catches first.
func catalogEmitter(modelVersion, collectorID string) (wire.Emitter, error) {
	switch modelVersion {
	case openits.ModelVersion:
		return openits.New(collectorID), nil
	default:
		return nil, fmt.Errorf("model_version %q has no catalog emitter in this build", modelVersion)
	}
}

// buildReaders builds one StateReader per device, in config order. On any
// failure it closes what it already built, so the caller owns either every
// reader or none.
func buildReaders(log *slog.Logger, devices []config.Device, reg *adapter.Registry) ([]adapter.StateReader, error) {
	readers := make([]adapter.StateReader, 0, len(devices))
	for _, d := range devices {
		a, err := reg.Build(d.Vendor, d.DeviceKind, d.ID, d.Connection)
		if err != nil {
			closeAll(log, readers)
			return nil, fmt.Errorf("device %s: %w", d.ID, err)
		}
		sr, ok := a.(adapter.StateReader)
		if !ok {
			_ = a.Close()
			closeAll(log, readers)
			return nil, fmt.Errorf("device %s: adapter %s lacks CapState", d.ID, a.Descriptor().Key())
		}
		readers = append(readers, sr)
	}
	return readers, nil
}

// closeAll closes every reader concurrently and waits at most
// closeTimeout. A Close still running then is abandoned and logged; the
// process is exiting, and the OS reclaims what it held.
func closeAll(log *slog.Logger, readers []adapter.StateReader) {
	var wg sync.WaitGroup
	for _, r := range readers {
		wg.Go(func() {
			if err := r.Close(); err != nil {
				log.Warn("adapter close", "adapter", r.Descriptor().Key(), "err", err)
			}
		})
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(closeTimeout):
		log.Warn("adapter close timed out; exiting without it", "timeout", closeTimeout)
	}
}

func encodeAndPublish(ctx context.Context, log *slog.Logger, pub *publish.Publisher, tenant cloudevents.Tenant,
	emitters []wire.Emitter, ev model.Event) {
	for _, em := range emitters {
		enc, ok, err := em.Encode(ev)
		if err != nil {
			log.Error("emit failed", "event", ev.EventKind(), "device", ev.EventDeviceID(), "err", err)
			return
		}
		if !ok {
			continue
		}
		source := cloudevents.SourceFor(tenant, ev.EventDeviceKind(), ev.EventDeviceID())
		if source == "" {
			// No entity-kind mapping for this device kind. Publishing would
			// mean inventing a URN shape upstream does not define, and ce-id
			// hashes that string, so the invention would be baked into every
			// id. Drop it as loudly as an unclaimed event.
			log.Warn("event dropped: no ce-source entity kind for device kind",
				"event", ev.EventKind(), "device_kind", ev.EventDeviceKind(), "device", ev.EventDeviceID())
			return
		}
		env := cloudevents.New(cloudevents.Event{
			CEType:      enc.CEType,
			Source:      source,
			ContentType: enc.ContentType,
			DataSchema:  enc.DataSchema,
			OccurredAt:  ev.EventOccurredAt(),
			Data:        enc.Data,
			Identity:    enc.Identity,
		})
		if err := pub.Publish(ctx, env, enc.CEType, ev.EventDeviceID()); err != nil {
			log.Error("publish failed", "type", enc.CEType, "device", ev.EventDeviceID(), "err", err)
		}
		return
	}
	// Loud drop: no emitter claims this event. README.md's "Events can still
	// be dropped with a warning, and that is the designed behaviour" is the
	// living statement of this rule. Reaching here now
	// means a real mapping gap — an event kind with no ce-type, or one the
	// openits emitter declined because it could not encode it faithfully —
	// rather than the chain simply being unwired.
	log.Warn("event dropped: no emitter for domain event",
		"event", ev.EventKind(), "device", ev.EventDeviceID())
}
