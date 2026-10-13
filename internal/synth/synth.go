// Package synth turns consecutive state Snapshots into domain events.
// One registered Differ per facet kind; the engine never grows vendor or
// wire knowledge.
package synth

import (
	"fmt"
	"reflect"
	"sync"

	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

// Differ diffs one facet kind. prev is nil on first observation.
type Differ interface {
	// Facet returns the zero value of the one concrete facet type this
	// differ diffs. Its FacetKind is the kind the differ is registered
	// under, and its dynamic type is the only type Apply will hand to Diff,
	// so Diff may assert it unchecked.
	Facet() model.Facet
	Diff(prev, curr model.Facet, base model.Base) []model.Event
}

// RejectHook observes a facet Apply refused because its dynamic type is not
// the one its differ diffs. got is the facet's Go type, e.g.
// "*model.SignalStatus".
type RejectHook func(deviceID string, kind model.Kind, got string)

// Engine applies snapshots and remembers last-known facets per device.
type Engine struct {
	mu      sync.Mutex
	differs map[model.Kind]Differ
	types   map[model.Kind]reflect.Type
	reject  RejectHook
	// prev[deviceID][kind] = last successfully-read facet
	prev map[string]map[model.Kind]model.Facet
}

// NewEngine registers one differ per facet kind. Two differs for the same
// kind is a wiring bug and panics, as adapter.Registry does for a duplicate
// adapter.
func NewEngine(differs ...Differ) *Engine {
	e := &Engine{
		differs: make(map[model.Kind]Differ),
		types:   make(map[model.Kind]reflect.Type),
		prev:    make(map[string]map[model.Kind]model.Facet),
	}
	for _, d := range differs {
		zero := d.Facet()
		k := zero.FacetKind()
		if _, dup := e.differs[k]; dup {
			panic(fmt.Sprintf("synth.NewEngine: two differs registered for facet kind %q", k))
		}
		e.differs[k] = d
		e.types[k] = reflect.TypeOf(zero)
	}
	return e
}

// WithRejectHook sets the observer for wrong-typed facets and returns e.
// Call it before the first Apply.
func (e *Engine) WithRejectHook(h RejectHook) *Engine {
	e.reject = h
	return e
}

// Apply diffs snap against last-known state and returns domain events.
// Iron rule: a facet that failed (snap.Errors) or is simply absent emits
// nothing and keeps its previous value — absence of evidence is never a
// state change. A facet the snapshot ALSO reports as failed counts as
// failed, and a facet whose dynamic type is not its differ's is rejected
// (reported to the RejectHook) rather than diffed: neither is evidence.
func (e *Engine) Apply(snap *model.Snapshot) []model.Event {
	e.mu.Lock()
	defer e.mu.Unlock()

	dev := e.prev[snap.DeviceID]
	if dev == nil {
		dev = make(map[model.Kind]model.Facet)
		e.prev[snap.DeviceID] = dev
	}

	base := model.Base{DeviceID: snap.DeviceID, DeviceKind: snap.DeviceKind, OccurredAt: snap.SampledAt}
	var events []model.Event
	for _, f := range snap.Facets {
		k := f.FacetKind()
		d, ok := e.differs[k]
		if !ok {
			continue // facet kind with no differ registered: carried, not diffed
		}
		if snap.FacetFailed(k) {
			continue
		}
		if reflect.TypeOf(f) != e.types[k] {
			if e.reject != nil {
				e.reject(snap.DeviceID, k, fmt.Sprintf("%T", f))
			}
			continue
		}
		events = append(events, d.Diff(dev[k], f, base)...)
		dev[k] = f
	}
	return events
}
