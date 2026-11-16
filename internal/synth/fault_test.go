package synth

import (
	"testing"
	"time"

	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

func faultSnap(at time.Time, faults ...model.Fault) *model.Snapshot {
	return &model.Snapshot{DeviceID: "asc-1", SampledAt: at,
		Facets: []model.Facet{model.FaultSet{Faults: faults}}}
}

var (
	mmu      = model.Fault{ID: "mmu-fault", Severity: model.SeverityCritical, Category: model.CategoryConflict, Description: "MMU fault detected"}
	door     = model.Fault{ID: "cabinet-door", Severity: model.SeverityMinor, Category: model.CategoryCabinet, Description: "Cabinet door open"}
	lampOut  = model.Fault{ID: "lamp-out", Severity: model.SeverityMajor, Category: model.CategoryLamp, Description: "Signal head lamp failure"}
	powerOut = model.Fault{ID: "power-loss", Severity: model.SeverityMajor, Category: model.CategoryPower, Description: "Power loss / generator running"}
)

func TestFirstPollRaisesEverythingCurrentlyRaised(t *testing.T) {
	t.Parallel()
	e := NewEngine(NewFaultDiffer())
	evs := e.Apply(faultSnap(t0, mmu, door))
	if len(evs) != 2 {
		t.Fatalf("first poll events = %v, want 2 raises", kinds(evs))
	}
	for _, ev := range evs {
		if ev.EventKind() != "fault-raised" {
			t.Errorf("unexpected %q", ev.EventKind())
		}
	}
}

func TestRaiseAndClear(t *testing.T) {
	t.Parallel()
	e := NewEngine(NewFaultDiffer())
	e.Apply(faultSnap(t0, mmu))

	// door appears, mmu persists -> one raise, no clear.
	evs := e.Apply(faultSnap(t0.Add(time.Second), mmu, door))
	if len(evs) != 1 {
		t.Fatalf("events = %v, want 1 raise", kinds(evs))
	}
	r, ok := evs[0].(model.FaultRaised)
	if !ok || r.FaultID != "cabinet-door" || r.Severity != model.SeverityMinor {
		t.Fatalf("bad raise: %+v", evs[0])
	}

	// mmu disappears -> one clear.
	evs = e.Apply(faultSnap(t0.Add(2*time.Second), door))
	if len(evs) != 1 {
		t.Fatalf("events = %v, want 1 clear", kinds(evs))
	}
	c, ok := evs[0].(model.FaultCleared)
	if !ok || c.FaultID != "mmu-fault" {
		t.Fatalf("bad clear: %+v", evs[0])
	}
}

func TestUnchangedFaultSetEmitsNothing(t *testing.T) {
	t.Parallel()
	e := NewEngine(NewFaultDiffer())
	e.Apply(faultSnap(t0, mmu, door))
	if evs := e.Apply(faultSnap(t0.Add(time.Second), mmu, door)); len(evs) != 0 {
		t.Fatalf("unchanged set must emit nothing, got %v", kinds(evs))
	}
}

// A fault whose attributes change while its ID stays raised is not a raise.
// Nothing consumes an "amended fault" event; inventing one is YAGNI.
func TestAttributeChangeIsNotARaise(t *testing.T) {
	t.Parallel()
	e := NewEngine(NewFaultDiffer())
	e.Apply(faultSnap(t0, mmu))
	worse := mmu
	worse.Description = "MMU fault detected (escalated)"
	if evs := e.Apply(faultSnap(t0.Add(time.Second), worse)); len(evs) != 0 {
		t.Fatalf("attribute change must emit nothing, got %v", kinds(evs))
	}
}

// Determinism: gen-1 iterated a Go map here, so its raise/clear order was
// nondeterministic. This is a regression guard, not a style preference.
func TestEventOrderIsSortedByFaultID(t *testing.T) {
	t.Parallel()
	for range 20 { // map iteration order varies per run
		e := NewEngine(NewFaultDiffer())
		evs := e.Apply(faultSnap(t0, lampOut, mmu, door, powerOut))
		var ids []string
		for _, ev := range evs {
			ids = append(ids, ev.(model.FaultRaised).FaultID)
		}
		want := []string{"cabinet-door", "lamp-out", "mmu-fault", "power-loss"}
		if len(ids) != len(want) {
			t.Fatalf("ids = %v", ids)
		}
		for j := range want {
			if ids[j] != want[j] {
				t.Fatalf("raise order = %v, want sorted %v", ids, want)
			}
		}
	}
}

func TestClearOrderIsSortedByFaultID(t *testing.T) {
	t.Parallel()
	for range 20 {
		e := NewEngine(NewFaultDiffer())
		e.Apply(faultSnap(t0, lampOut, mmu, door, powerOut))
		evs := e.Apply(faultSnap(t0.Add(time.Second)))
		var ids []string
		for _, ev := range evs {
			ids = append(ids, ev.(model.FaultCleared).FaultID)
		}
		want := []string{"cabinet-door", "lamp-out", "mmu-fault", "power-loss"}
		for j := range want {
			if ids[j] != want[j] {
				t.Fatalf("clear order = %v, want sorted %v", ids, want)
			}
		}
	}
}

// THE IRON RULE. Gen-1's failure here was severe: on any SNMP error it
// returned a reading containing only a synthetic snmp-unreachable fault, and
// because diffFaults was a pure set-difference that CLEARED EVERY REAL FAULT,
// then re-raised them on recovery. A failed read must never clear a fault.
func TestFailedFaultReadNeverClears(t *testing.T) {
	t.Parallel()
	e := NewEngine(NewFaultDiffer())
	e.Apply(faultSnap(t0, mmu, door))

	failed := &model.Snapshot{DeviceID: "asc-1", SampledAt: t0.Add(time.Second),
		Errors: []model.FacetError{{Kind: model.KindFaultSet, Err: "alarm OID unanswered"}}}
	if evs := e.Apply(failed); len(evs) != 0 {
		t.Fatalf("failed read must emit nothing, got %v", kinds(evs))
	}

	// Recovery with the same faults still raised: no re-raise, because prev
	// survived the failed poll.
	if evs := e.Apply(faultSnap(t0.Add(2*time.Second), mmu, door)); len(evs) != 0 {
		t.Fatalf("post-recovery events = %v, want none", kinds(evs))
	}
}

// first-observed is mandatory on fault-raised and fault-cleared (openits-models
// v0.8.0). NTCIP alarms carry no time, so the engine remembers when it first
// saw each fault and carries that forward — to the clear, and across a
// restart in the stored facet (ADR 0024).
func TestFaultFirstObservedIsStampedCarriedAndCleared(t *testing.T) {
	t.Parallel()
	e := NewEngine(NewFaultDiffer())
	t1, t2, t3 := t0.Add(time.Minute), t0.Add(2*time.Minute), t0.Add(3*time.Minute)

	raised := e.Apply(faultSnap(t1, mmu))[0].(model.FaultRaised)
	if !raised.FirstObserved.Equal(t1) {
		t.Fatalf("raise first-observed = %v, want the first poll that saw it (%v)", raised.FirstObserved, t1)
	}
	if evs := e.Apply(faultSnap(t2, mmu)); len(evs) != 0 {
		t.Fatalf("still raised: events = %v, want none", kinds(evs))
	}
	cleared := e.Apply(faultSnap(t3))[0].(model.FaultCleared)
	if !cleared.FirstObserved.Equal(t1) {
		t.Fatalf("clear first-observed = %v, want when the fault began (%v), not when it ended", cleared.FirstObserved, t1)
	}
}

// A device that reports its own fault time is the better witness: the
// engine never overwrites it.
func TestFaultFirstObservedKeepsTheDevicesOwnTime(t *testing.T) {
	t.Parallel()
	e := NewEngine(NewFaultDiffer())
	deviceTime := t0.Add(-time.Hour)
	f := mmu
	f.FirstObserved = deviceTime
	raised := e.Apply(faultSnap(t0, f))[0].(model.FaultRaised)
	if !raised.FirstObserved.Equal(deviceTime) {
		t.Fatalf("first-observed = %v, want the device's %v", raised.FirstObserved, deviceTime)
	}
}

// After a restart the engine is seeded from the stored facet, so a fault
// standing across it clears with its original start, not the restart time.
func TestFaultFirstObservedSurvivesARestart(t *testing.T) {
	t.Parallel()
	before := NewEngine(NewFaultDiffer())
	var stored model.Facet
	before.WithChangeHook(func(_ string, _ model.Kind, f model.Facet) { stored = f })
	before.Apply(faultSnap(t0, mmu))

	after := NewEngine(NewFaultDiffer())
	after.HoldUntilSeeded()
	after.Seed("asc-1", []model.Facet{stored})
	cleared := after.Apply(faultSnap(t0.Add(time.Hour)))[0].(model.FaultCleared)
	if !cleared.FirstObserved.Equal(t0) {
		t.Fatalf("clear after restart first-observed = %v, want %v", cleared.FirstObserved, t0)
	}
}

// A clear names the same fault class as its raise: the differ carries the
// cleared fault's category from the remembered state, so the wire kind of
// both halves is the service's specific fault identity.
func TestFaultClearCarriesTheCategoryOfItsRaise(t *testing.T) {
	t.Parallel()
	e := NewEngine(NewFaultDiffer())
	e.Apply(faultSnap(t0, lampOut))
	cleared := e.Apply(faultSnap(t0.Add(time.Second)))[0].(model.FaultCleared)
	if cleared.Category != model.CategoryLamp {
		t.Fatalf("clear category = %v, want the raise's %v", cleared.Category, model.CategoryLamp)
	}
}

func TestFaultClearCarriesTypeAndGateID(t *testing.T) {
	t.Parallel()
	e := NewEngine(NewFaultDiffer())
	f := model.Fault{
		ID: "gate/WG-1/estop", Severity: model.SeverityCritical,
		Category: model.CategoryCabinet, Type: model.FaultTypeGateEmergencyStop, GateID: "WG-1",
	}
	raised := e.Apply(faultSnap(t0, f))[0].(model.FaultRaised)
	if raised.Type != model.FaultTypeGateEmergencyStop || raised.GateID != "WG-1" {
		t.Fatalf("raise type/gate = %v/%q", raised.Type, raised.GateID)
	}
	cleared := e.Apply(faultSnap(t0.Add(time.Second)))[0].(model.FaultCleared)
	if cleared.Type != model.FaultTypeGateEmergencyStop || cleared.GateID != "WG-1" {
		t.Fatalf("clear type/gate = %v/%q", cleared.Type, cleared.GateID)
	}
}
