package cameleon

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Vikasa2M/vikasa-collector/internal/synth"
	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

// TestPLCWatchdogRestartDoesNotFalseClear is the restart regression.
// A standing plc/not-scanning fault is carried through JSON, DecodeFacet
// and Seed. The reader after the restart is a new adapter.
func TestPLCWatchdogRestartDoesNotFalseClear(t *testing.T) {
	start := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	now := start
	words := make([]uint16, 110)
	const counter = uint16(4)
	words[0] = counter << 11
	clock := func() time.Time { return now }
	plc := plcCfg{watchdogStall: 5 * time.Second}

	var stored []byte
	eng := synth.NewEngine(synth.NewFaultDiffer()).WithChangeHook(func(_ string, k model.Kind, f model.Facet) {
		if k != model.KindFaultSet {
			return
		}
		raw, err := json.Marshal(f)
		if err != nil {
			t.Errorf("marshal fault set: %v", err)
			return
		}
		stored = raw
	})
	first := plcACS(words, plc, clock)
	if evs := eng.Apply(mustSnap(t, first)); len(evs) != 0 {
		t.Fatalf("baseline events = %v, want none", evs)
	}
	now = now.Add(5 * time.Second)
	raised := eng.Apply(mustSnap(t, first))
	if len(raised) != 1 {
		t.Fatalf("stall events = %v, want one raise", raised)
	}
	fr, ok := raised[0].(model.FaultRaised)
	if !ok || fr.FaultID != "plc/not-scanning" || fr.Type != model.FaultTypePLCNotScanning {
		t.Fatalf("raise = %#v", raised[0])
	}
	if fr.FirstObserved.IsZero() {
		t.Fatal("raise has no first_observed")
	}
	if len(stored) == 0 {
		t.Fatal("fault set was not serialized")
	}

	restored := synth.NewEngine(synth.NewFaultDiffer()).HoldUntilSeeded()
	facet, err := restored.DecodeFacet(model.KindFaultSet, stored)
	if err != nil {
		t.Fatal(err)
	}
	restored.Seed("acs-1", []model.Facet{facet})
	second := plcACS(append([]uint16(nil), words...), plc, clock)
	second.RestoreFaults(facet.(model.FaultSet).Faults)

	evs := restored.Apply(mustSnap(t, second))
	if len(evs) != 0 {
		t.Fatalf("first post-restart observation = %v, want no false clear", evs)
	}
	now = now.Add(5 * time.Second)
	wordsOf(t, second)[0] |= 1 << 10 // PLC fault bit; the watchdog field is unchanged
	evs = restored.Apply(mustSnap(t, second))
	if len(evs) != 1 {
		t.Fatalf("unrelated fault during a standing stall = %v", evs)
	}
	if fr, ok := evs[0].(model.FaultRaised); !ok || fr.FaultID != "plc/fault" {
		t.Fatalf("unrelated raise = %#v", evs[0])
	}
	wordsOf(t, second)[0] = (counter+1)<<11 | (1 << 10)
	now = now.Add(time.Second)
	evs = restored.Apply(mustSnap(t, second))
	if len(evs) != 1 {
		t.Fatalf("counter progress events = %v, want one clear", evs)
	}
	fc, ok := evs[0].(model.FaultCleared)
	if !ok || fc.FaultID != "plc/not-scanning" || fc.Type != model.FaultTypePLCNotScanning {
		t.Fatalf("clear = %#v", evs[0])
	}
	if !fc.FirstObserved.Equal(fr.FirstObserved) {
		t.Fatalf("clear first_observed = %s, want %s", fc.FirstObserved, fr.FirstObserved)
	}

	now = now.Add(5 * time.Second)
	evs = restored.Apply(mustSnap(t, second))
	if len(evs) != 1 {
		t.Fatalf("later stall = %v, want one new raise", evs)
	}
	again, ok := evs[0].(model.FaultRaised)
	if !ok || again.FaultID != "plc/not-scanning" {
		t.Fatalf("later raise = %#v", evs[0])
	}
	if !again.FirstObserved.After(fr.FirstObserved) {
		t.Fatalf("new stall reused first_observed %s", again.FirstObserved)
	}
}

// TestPLCWatchdogRestartWithoutStandingStall does not invent a stall when
// the restored fault set has none.
func TestPLCWatchdogRestartWithoutStandingStall(t *testing.T) {
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	words := make([]uint16, 110)
	words[0] = 4 << 11
	words[11] = 0x0021
	clock := func() time.Time { return now }
	plc := plcCfg{watchdogStall: 5 * time.Second}
	a := plcACS(words, plc, clock)
	eng := synth.NewEngine(synth.NewFaultDiffer(), synth.NewGateDiffer())
	if evs := eng.Apply(mustSnap(t, a)); len(evs) != 0 {
		t.Fatalf("baseline = %v", evs)
	}
	raw, err := json.Marshal(model.FaultSet{})
	if err != nil {
		t.Fatal(err)
	}
	next := synth.NewEngine(synth.NewFaultDiffer(), synth.NewGateDiffer()).HoldUntilSeeded()
	facet, err := next.DecodeFacet(model.KindFaultSet, raw)
	if err != nil {
		t.Fatal(err)
	}
	next.Seed("acs-1", []model.Facet{facet, gateBankOf(t, mustSnap(t, a))})
	b := plcACS(append([]uint16(nil), words...), plc, clock)
	if evs := next.Apply(mustSnap(t, b)); len(evs) != 0 {
		t.Fatalf("restart without a stall = %v", evs)
	}
	now = now.Add(5 * time.Second)
	evs := next.Apply(mustSnap(t, b))
	if len(evs) != 1 {
		t.Fatalf("new stall after a quiet restart = %v", evs)
	}
	if _, ok := evs[0].(model.FaultRaised); !ok {
		t.Fatalf("event = %T", evs[0])
	}
}

// TestPLCWatchdogStallDisabledIsNotRecovery clears a restored stall when
// inference is configured off, without treating the unchanged counter as
// controller recovery. Time passing does not raise a replacement stall.
func TestPLCWatchdogStallDisabledIsNotRecovery(t *testing.T) {
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	words := make([]uint16, 110)
	words[0] = 4 << 11
	seen := time.Date(2026, 11, 16, 11, 0, 0, 0, time.UTC)
	raw, err := json.Marshal(model.FaultSet{Faults: []model.Fault{{
		ID: "plc/not-scanning", Severity: model.SeverityCritical, Category: model.CategoryCabinet,
		Description: "PLC watchdog has not advanced", Type: model.FaultTypePLCNotScanning,
		FirstObserved: seen,
	}}})
	if err != nil {
		t.Fatal(err)
	}
	eng := synth.NewEngine(synth.NewFaultDiffer()).HoldUntilSeeded()
	facet, err := eng.DecodeFacet(model.KindFaultSet, raw)
	if err != nil {
		t.Fatal(err)
	}
	eng.Seed("acs-1", []model.Facet{facet})
	a := plcACS(words, plcCfg{watchdogStall: 0}, func() time.Time { return now })
	evs := eng.Apply(mustSnap(t, a))
	if len(evs) != 1 {
		t.Fatalf("disabled inference = %v, want one clear", evs)
	}
	fc, ok := evs[0].(model.FaultCleared)
	if !ok || !fc.FirstObserved.Equal(seen) || fc.Type != model.FaultTypePLCNotScanning {
		t.Fatalf("clear = %#v", evs[0])
	}
	if words[0] != 4<<11 {
		t.Fatal("counter changed; this case is configuration, not recovery")
	}
	now = now.Add(time.Minute)
	if evs = eng.Apply(mustSnap(t, a)); len(evs) != 0 {
		t.Fatalf("disabled inference later = %v", evs)
	}
}

func wordsOf(t *testing.T, a *acs) []uint16 {
	t.Helper()
	s, ok := a.client.(*staticWords)
	if !ok {
		t.Fatalf("client %T", a.client)
	}
	return s.words
}

func gateBankOf(t *testing.T, snap *model.Snapshot) model.GateBank {
	t.Helper()
	f, ok := snap.Facet(model.KindGateBank)
	if !ok {
		t.Fatal("missing gate bank")
	}
	return f.(model.GateBank)
}

// The failed-read cases around a restart use the same serialization path.
func TestPLCWatchdogFailedReadAroundRestart(t *testing.T) {
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	words := make([]uint16, 110)
	words[0] = 4 << 11
	clock := func() time.Time { return now }
	plc := plcCfg{watchdogStall: 5 * time.Second}
	a := plcACS(words, plc, clock)
	if evs := synth.NewEngine(synth.NewFaultDiffer()).Apply(mustSnap(t, a)); len(evs) != 0 {
		t.Fatalf("baseline = %v", evs)
	}
	a.client = &errClient{err: context.DeadlineExceeded}
	if _, err := a.Read(context.Background()); err == nil {
		t.Fatal("expected hard error before the stall")
	}
	if a.wdHave || a.wdStalled {
		t.Fatalf("pre-stall failure kept history: have=%v stalled=%v", a.wdHave, a.wdStalled)
	}

	seen := now
	raw, err := json.Marshal(model.FaultSet{Faults: []model.Fault{{
		ID: "plc/not-scanning", Severity: model.SeverityCritical, Category: model.CategoryCabinet,
		Type: model.FaultTypePLCNotScanning, FirstObserved: seen,
	}}})
	if err != nil {
		t.Fatal(err)
	}
	eng := synth.NewEngine(synth.NewFaultDiffer()).HoldUntilSeeded()
	facet, err := eng.DecodeFacet(model.KindFaultSet, raw)
	if err != nil {
		t.Fatal(err)
	}
	eng.Seed("acs-1", []model.Facet{facet})
	b := plcACS(words, plc, clock)
	b.RestoreFaults(facet.(model.FaultSet).Faults)
	b.client = &errClient{err: context.DeadlineExceeded}
	if _, err := b.Read(context.Background()); err == nil {
		t.Fatal("expected hard error after restart")
	}
	if !b.wdStalled || !b.wdRestored {
		t.Fatalf("failed read erased the restored stall: stalled=%v restored=%v", b.wdStalled, b.wdRestored)
	}
	b.client = &staticWords{start: 90, words: words}
	if evs := eng.Apply(mustSnap(t, b)); len(evs) != 0 {
		t.Fatalf("success after a failed read cleared the restored stall: %v", evs)
	}
}
