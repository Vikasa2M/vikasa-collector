package openits

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Vikasa2M/vikasa-collector/internal/synth"
	"github.com/Vikasa2M/vikasa-collector/internal/vendors/cameleon"
	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

// legacyWatchdogJSON is a fault set from before Type and GateID existed.
// omitzero drops those zero fields, and the test rejects a fixture that
// still names them.
func legacyWatchdogJSON(t *testing.T, seen time.Time) []byte {
	t.Helper()
	raw, err := json.Marshal(model.FaultSet{Faults: []model.Fault{{
		ID: watchdogFaultID, Severity: model.SeverityCritical, Category: model.CategoryCabinet,
		Description: watchdogDescription, FirstObserved: seen,
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(`"Type"`)) || bytes.Contains(raw, []byte(`"GateID"`)) {
		t.Fatalf("legacy fixture names a classification: %s", raw)
	}
	return raw
}

func decodeLegacy(t *testing.T, eng *synth.Engine, raw []byte) model.FaultSet {
	t.Helper()
	facet, err := eng.DecodeFacet(model.KindFaultSet, raw)
	if err != nil {
		t.Fatal(err)
	}
	fs := facet.(model.FaultSet)
	if len(fs.Faults) != 1 || fs.Faults[0].Type != 0 || fs.Faults[0].GateID != "" {
		t.Fatalf("decoded legacy fault = %+v", fs.Faults)
	}
	return fs
}

// TestLegacyTypelessStallTakesItsTypeFromTheLiveObservation applies an
// unchanged counter before any clear. The stored first_observed stays.
// The live observation supplies the type, and the later clear is classified.
func TestLegacyTypelessStallTakesItsTypeFromTheLiveObservation(t *testing.T) {
	seen := time.Date(2026, 11, 16, 11, 0, 0, 0, time.UTC)
	eng := synth.NewEngine(synth.NewFaultDiffer()).HoldUntilSeeded()
	fs := decodeLegacy(t, eng, legacyWatchdogJSON(t, seen))
	eng.Seed("acs-1", []model.Facet{fs})
	var carried model.Fault
	eng.WithChangeHook(func(_ string, k model.Kind, f model.Facet) {
		if k != model.KindFaultSet || carried.ID != "" {
			return
		}
		for _, fault := range f.(model.FaultSet).Faults {
			if fault.ID == watchdogFaultID {
				carried = fault
			}
		}
	})

	plc := cameleon.NewScriptedPLC(4, 5*time.Second, seen)
	plc.RestoreFaults(fs.Faults)
	if evs := eng.Apply(readScripted(t, plc)); len(evs) != 0 {
		t.Fatalf("unchanged counter = %v, want no clear and no second raise", evs)
	}
	if carried.Type != model.FaultTypePLCNotScanning || carried.GateID != "" || !carried.FirstObserved.Equal(seen) {
		t.Fatalf("stored fault = %+v, want the live type and the original first_observed", carried)
	}

	plc.SetClock(seen.Add(time.Second))
	plc.SetCounter(5)
	evs := eng.Apply(readScripted(t, plc))
	if len(evs) != 1 {
		t.Fatalf("counter progress = %v, want one clear", evs)
	}
	fc, ok := evs[0].(model.FaultCleared)
	if !ok || fc.Type != model.FaultTypePLCNotScanning || fc.GateID != "" || !fc.FirstObserved.Equal(seen) {
		t.Fatalf("clear = %#v", evs[0])
	}
	wire := decodeWatchdogCleared(t, New("cabinet-poller-1", "legacy-a"), fc)
	if wire.GetKind() != watchdogControllerKind || wire.GetComponent() != nil {
		t.Fatalf("wire clear = kind %q component %v", wire.GetKind(), wire.GetComponent())
	}
	requireFirstObserved(t, wire.GetFirstObserved(), seen, "legacy progress clear")
}

// TestLegacyTypelessStallOmittedByDisabledInferenceDeclinesOnTheWire is the
// compatibility limitation. watchdog_stall is 0, so the adapter does not
// report plc/not-scanning. That omitting snapshot is the first one applied,
// and the domain clear keeps the seeded zero type and empty gate id.
// The wire emitter declines it. This is not a published clear.
func TestLegacyTypelessStallOmittedByDisabledInferenceDeclinesOnTheWire(t *testing.T) {
	seen := time.Date(2026, 11, 16, 11, 0, 0, 0, time.UTC)
	eng := synth.NewEngine(synth.NewFaultDiffer()).HoldUntilSeeded()
	fs := decodeLegacy(t, eng, legacyWatchdogJSON(t, seen))
	eng.Seed("acs-1", []model.Facet{fs})

	plc := cameleon.NewScriptedPLC(4, 0, seen)
	plc.RestoreFaults(fs.Faults)
	if plc.WatchdogRestored() {
		t.Fatal("a zero stall window accepted a restored stall")
	}
	evs := eng.Apply(readScripted(t, plc))
	if len(evs) != 1 {
		t.Fatalf("disabled inference = %v, want one domain clear", evs)
	}
	fc, ok := evs[0].(model.FaultCleared)
	if !ok || fc.FaultID != watchdogFaultID || fc.Type != 0 || fc.GateID != "" || !fc.FirstObserved.Equal(seen) {
		t.Fatalf("clear invented a classification: %#v", evs[0])
	}
	enc, ok, err := New("cabinet-poller-1", "legacy-b").Encode(fc)
	if err != nil {
		t.Fatal(err)
	}
	if ok || enc != nil {
		t.Fatalf("typeless clear was encoded: ok=%v", ok)
	}
}

func readScripted(t *testing.T, plc *cameleon.ScriptedPLC) *model.Snapshot {
	t.Helper()
	snap, err := plc.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return snap
}
