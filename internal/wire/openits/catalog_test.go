package openits

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// modelsModule is the catalog this emitter adapts to (ADR 0016).
const modelsModule = "github.com/Vikasa2M/openits-models"

// pinnedCatalog returns every ce-type the PINNED openits-models release
// declares, read from that release's own bindings/nats/asyncapi.yaml in the
// module cache. go.mod picks the release; this test never names a version.
func pinnedCatalog(t *testing.T) []string {
	t.Helper()
	out, err := exec.CommandContext(t.Context(), "go", "list", "-m", "-f", "{{.Dir}}", modelsModule).Output()
	if err != nil {
		t.Fatalf("locate %s: %v", modelsModule, err)
	}
	dir := strings.TrimSpace(string(out))
	if dir == "" {
		t.Fatalf("%s is not in the module cache; run `go mod download`", modelsModule)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "bindings", "nats", "asyncapi.yaml"))
	if err != nil {
		t.Fatalf("read pinned catalog: %v", err)
	}
	var doc struct {
		Channels map[string]any `yaml:"channels"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse pinned catalog: %v", err)
	}
	if len(doc.Channels) == 0 {
		t.Fatal("pinned catalog declares no channels; the asyncapi layout changed under this test")
	}
	cat := make([]string, 0, len(doc.Channels))
	for ceType := range doc.Channels {
		cat = append(cat, ceType)
	}
	slices.Sort(cat)
	return cat
}

// Every ce-type the emitter can produce must exist in the pinned catalog. A
// type outside it is one no consumer written against the catalog can
// subscribe to by name — the collector inventing wire identity (ADR 0016).
func TestEmittedCETypesAreInPinnedCatalog(t *testing.T) {
	cat := pinnedCatalog(t)
	for _, ceType := range New("x", testEpoch).CETypes() {
		if _, found := slices.BinarySearch(cat, ceType); !found {
			t.Errorf("emitter produces %q, which the pinned openits-models release does not declare", ceType)
		}
	}
}

// Every catalog ce-type is either mapped or consciously unmapped, with a
// reason. This is what makes a pin bump's new ce-types visible: before it,
// a type the release added and the emitter never mapped was invisible to
// every check in this repo.
func TestEveryCatalogCETypeIsMappedOrAllowlisted(t *testing.T) {
	emitted := New("x", testEpoch).CETypes()
	for _, ceType := range pinnedCatalog(t) {
		if slices.Contains(emitted, ceType) {
			continue
		}
		if _, ok := unmapped[ceType]; !ok {
			t.Errorf("pinned catalog declares %q and the emitter does not map it: map it (wire-emitter skill), or add it to `unmapped` with a reason", ceType)
		}
	}
}

// The allowlist must not outlive its reasons: an entry that is now mapped,
// or that the pinned release no longer declares, is a stale claim.
func TestUnmappedAllowlistIsCurrent(t *testing.T) {
	cat := pinnedCatalog(t)
	emitted := New("x", testEpoch).CETypes()
	for ceType := range unmapped {
		if slices.Contains(emitted, ceType) {
			t.Errorf("%q is mapped now; delete it from `unmapped`", ceType)
		}
		if _, found := slices.BinarySearch(cat, ceType); !found {
			t.Errorf("%q is in `unmapped` but the pinned catalog no longer declares it; delete it", ceType)
		}
	}
}

// unmappedReason is why a catalog ce-type has no mapping. Every value is a
// claim a reviewer can check against ADR 0016, not a free-text excuse.
type unmappedReason string

const (
	// The collector has no domain facet, differ or adapter for this service
	// at all. Blocked on modelling the device domain, not on a mapping.
	serviceNotModeled unmappedReason = "service not modelled: no facet, differ or adapter"

	// The event reports a command. The collector commands nothing (there is
	// no command path in the SDK, ADR 0021), so it never observes one.
	describesACommand unmappedReason = "describes a command; the collector issues none"

	// The service is wired, but the event reports a fact no facet holds and
	// no adapter reads: a sensor's operational status, its queue state, a
	// zone's live occupancy. Owed here (ADR 0016), but with the first adapter
	// that can read the fact, not modelled ahead of a device (2026-10-22).
	owedNeedsADeviceFact unmappedReason = "owed: needs a fact no adapter reads yet"

	// The event is defined as a transition in the controller's
	// high-resolution event log (the Indiana hi-res codes), carrying the
	// log's own timestamp and wire identifier. Most are sub-second (a phase
	// interval, a detector actuation, a pedestrian call), so a one-second
	// poll would miss some and back-date none: it would publish an
	// inference with the log's precision claimed. They belong to a
	// controller that emits natively, the end state ADR 0016 expects.
	needsHighResLog unmappedReason = "needs the controller's high-resolution event log; polling cannot see it"

	// The event belongs to an adaptive signal control system (TSAM), not to
	// the controller: its engagement state and the recommendations it
	// applies live in that system, which publishes them itself.
	reportedByAdaptiveSystem unmappedReason = "reported by the adaptive control system, not read from the controller"
)

// unmapped lists every ce-type the pinned catalog declares and this emitter
// does not produce. Shrinking it is the work; growing it needs a reason.
var unmapped = map[string]unmappedReason{
	// ess
	"openits.ess.fault-cleared.v1":       serviceNotModeled,
	"openits.ess.fault-raised.v1":        serviceNotModeled,
	"openits.ess.sensor-recalibrated.v1": serviceNotModeled,
	"openits.ess.weather-alert.v1":       serviceNotModeled,
	// ramp-metering
	"openits.ramp-metering.fault-cleared.v1":            serviceNotModeled,
	"openits.ramp-metering.fault-raised.v1":             serviceNotModeled,
	"openits.ramp-metering.mode-changed.v1":             serviceNotModeled,
	"openits.ramp-metering.queue-override-activated.v1": serviceNotModeled,
	"openits.ramp-metering.queue-override-cleared.v1":   serviceNotModeled,
	"openits.ramp-metering.release-rate-changed.v1":     serviceNotModeled,
	// reversible-lane
	"openits.reversible-lane.fault-cleared.v1":         serviceNotModeled,
	"openits.reversible-lane.fault-raised.v1":          serviceNotModeled,
	"openits.reversible-lane.gate-status-changed.v1":   serviceNotModeled,
	"openits.reversible-lane.lane-state-changed.v1":    serviceNotModeled,
	"openits.reversible-lane.lcs-conflict-detected.v1": serviceNotModeled,
	"openits.reversible-lane.transition-timeout.v1":    serviceNotModeled,
	// rsu
	"openits.rsu.fault-cleared.v1":                   serviceNotModeled,
	"openits.rsu.fault-raised.v1":                    serviceNotModeled,
	"openits.rsu.mode-changed.v1":                    serviceNotModeled,
	"openits.rsu.rsu-broadcast-sample.v1":            serviceNotModeled,
	"openits.rsu.rsu-certificate-expiring.v1":        serviceNotModeled,
	"openits.rsu.rsu-channel-fault.v1":               serviceNotModeled,
	"openits.rsu.rsu-gps-status-change.v1":           serviceNotModeled,
	"openits.rsu.rsu-security-event.v1":              serviceNotModeled,
	"openits.rsu.rsu-srm-received.v1":                serviceNotModeled,
	"openits.rsu.rsu-srm-status-change.v1":           serviceNotModeled,
	"openits.rsu.rsu-tim-broadcast.v1":               serviceNotModeled,
	"openits.rsu.rsu-tim-broadcast-state-changed.v1": serviceNotModeled,
	"openits.rsu.rsu-tim-cleared.v1":                 serviceNotModeled,
	"openits.rsu.rsu-tim-loaded.v1":                  serviceNotModeled,
	// work-zone
	"openits.work-zone.comm-health-event.v1":  serviceNotModeled,
	"openits.work-zone.zone-state-changed.v1": serviceNotModeled,
	// cctv
	"openits.cctv.lockout-denied.v1":      describesACommand,
	"openits.cctv.ptz-move-commanded.v1":  describesACommand,
	"openits.cctv.ptz-preset-recalled.v1": describesACommand,
	// traffic-sensor
	"openits.traffic-sensor.queue-state-changed.v1":          owedNeedsADeviceFact,
	"openits.traffic-sensor.traffic-sensor-status-report.v1": owedNeedsADeviceFact,
	// zone-occupancy
	"openits.zone-occupancy.zone-occupancy-changed.v1": owedNeedsADeviceFact,
	// signal-control (assessed 2026-10-23)
	//
	// controller-fault-event is fault-raised's hi-res-log counterpart; the
	// polled shortAlarmStatus already publishes as fault-raised/cleared.
	// detector-transition's faults are covered the same way; its on/off
	// actuations are sub-second. tsp-event is check-in/out messages and
	// NTCIP 1211 priority outcomes, not NTCIP 1202 state. unmapped-event
	// types a hi-res row the decoder could not; a poller has no rows.
	//
	// Two are owed, each from one more NTCIP 1202 object (needs hardware to
	// confirm what a controller reports):
	//   - preemption-event: each preempt's stage (preemptState: entry,
	//     track service, dwell, exit), which lasts seconds, not
	//     milliseconds; ntcip-asc already polls the aggregate preempt status.
	//   - coordination-change: the cycle length, offset and splits of the
	//     running pattern (patternTable, splitTable). Its pattern-change kind
	//     is plan-applied, already mapped; its MAX-in-effect kinds are
	//     per-cycle and stay out of reach.
	"openits.signal-control.controller-fault-event.v1":      needsHighResLog,
	"openits.signal-control.coordination-change.v1":         owedNeedsADeviceFact,
	"openits.signal-control.detector-transition.v1":         needsHighResLog,
	"openits.signal-control.overlap-change.v1":              needsHighResLog,
	"openits.signal-control.pedestrian-event.v1":            needsHighResLog,
	"openits.signal-control.phase-state-change.v1":          needsHighResLog,
	"openits.signal-control.preemption-event.v1":            owedNeedsADeviceFact,
	"openits.signal-control.tsam-mode-changed.v1":           reportedByAdaptiveSystem,
	"openits.signal-control.tsam-recommendation-applied.v1": reportedByAdaptiveSystem,
	"openits.signal-control.tsp-event.v1":                   needsHighResLog,
	"openits.signal-control.unmapped-event.v1":              needsHighResLog,
}
