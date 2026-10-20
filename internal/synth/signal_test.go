package synth

import (
	"slices"
	"testing"
	"time"

	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

var t0 = time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC)

func snapAt(at time.Time, st model.SignalStatus) *model.Snapshot {
	return &model.Snapshot{DeviceID: "asc-1", SampledAt: at, Facets: []model.Facet{st}}
}

func kinds(evs []model.Event) []string {
	out := make([]string, len(evs))
	for i, e := range evs {
		out[i] = e.EventKind()
	}
	return out
}

func TestFirstPollEmitsOnlyStatusReport(t *testing.T) {
	t.Parallel()
	e := NewEngine(NewSignalDiffer())
	evs := e.Apply(snapAt(t0, model.SignalStatus{Mode: model.ModeCoordinated, CabinetOutput: model.OutputNormal, ActivePlanID: 3}))
	if got := kinds(evs); len(got) != 1 || got[0] != "operational-status-report" {
		t.Fatalf("first poll events = %v, want [operational-status-report]", got)
	}
	rep := evs[0].(model.OperationalStatusReport)
	if rep.ActivePlanID != 3 || rep.Mode != model.ModeCoordinated || rep.CabinetOutput != model.OutputNormal || !rep.OccurredAt.Equal(t0) {
		t.Fatalf("bad report: %+v", rep)
	}
}

func TestTransitionsEmitChangeEvents(t *testing.T) {
	t.Parallel()
	e := NewEngine(NewSignalDiffer())
	e.Apply(snapAt(t0, model.SignalStatus{Mode: model.ModeCoordinated, CabinetOutput: model.OutputNormal, ActivePlanID: 3}))

	t1 := t0.Add(time.Second)
	evs := e.Apply(snapAt(t1, model.SignalStatus{
		Mode: model.ModeFree, CabinetOutput: model.OutputFlash, FlashCause: model.FlashMonitor, ActivePlanID: 7,
		PreemptionActive: true, PreemptionSource: "preempt-2",
	}))
	got := map[string]bool{}
	for _, k := range kinds(evs) {
		got[k] = true
	}
	for _, want := range []string{"operational-status-report", "mode-changed", "cabinet-output-changed", "plan-changed", "preemption-activated"} {
		if !got[want] {
			t.Fatalf("missing %q in %v", want, kinds(evs))
		}
	}

	t2 := t1.Add(time.Second)
	evs = e.Apply(snapAt(t2, model.SignalStatus{Mode: model.ModeFree, CabinetOutput: model.OutputFlash, FlashCause: model.FlashMonitor, ActivePlanID: 7}))
	found := false
	for _, ev := range evs {
		if ev.EventKind() == "preemption-cleared" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected preemption-cleared in %v", kinds(evs))
	}
}

func TestFailedFacetSuspendsDiffing(t *testing.T) {
	t.Parallel()
	e := NewEngine(NewSignalDiffer())
	e.Apply(snapAt(t0, model.SignalStatus{Mode: model.ModeCoordinated, CabinetOutput: model.OutputNormal, ActivePlanID: 3}))

	// Poll 2: facet failed. No events at all for this facet.
	failed := &model.Snapshot{DeviceID: "asc-1", SampledAt: t0.Add(time.Second),
		Errors: []model.FacetError{{Kind: model.KindSignalStatus, Err: "timeout"}}}
	if evs := e.Apply(failed); len(evs) != 0 {
		t.Fatalf("failed facet must emit nothing, got %v", kinds(evs))
	}

	// Poll 3: recovered with same state. Must NOT re-emit transitions
	// against a zero value — prev survived the failed poll.
	evs := e.Apply(snapAt(t0.Add(2*time.Second), model.SignalStatus{Mode: model.ModeCoordinated, CabinetOutput: model.OutputNormal, ActivePlanID: 3}))
	if got := kinds(evs); len(got) != 1 || got[0] != "operational-status-report" {
		t.Fatalf("post-recovery events = %v, want [operational-status-report]", got)
	}
}

// The Engine must propagate the snapshot's device kind onto every event it
// synthesizes; a differ never sets it.
func TestEngineCopiesDeviceKindOntoEvents(t *testing.T) {
	t.Parallel()
	e := NewEngine(NewSignalDiffer())
	snap := &model.Snapshot{
		DeviceID: "asc-1", DeviceKind: "asc", SampledAt: t0,
		Facets: []model.Facet{model.SignalStatus{Mode: model.ModeCoordinated, CabinetOutput: model.OutputNormal, ActivePlanID: 3}},
	}
	evs := e.Apply(snap)
	if len(evs) == 0 {
		t.Fatal("expected at least one event")
	}
	for _, ev := range evs {
		if got := ev.EventDeviceKind(); got != "asc" {
			t.Errorf("%s: EventDeviceKind() = %q, want %q", ev.EventKind(), got, "asc")
		}
	}
}

// Each axis — mode, plan, preemption — moves alone and must produce exactly
// its own transition beside the every-poll status report.
// TestTransitionsEmitChangeEvents moves all three at once and only checks
// presence, so it cannot catch one axis leaking an event into another.
func TestSignalAxesChangeIndependently(t *testing.T) {
	t.Parallel()
	e := NewEngine(NewSignalDiffer())
	st := model.SignalStatus{Mode: model.ModeCoordinated, CabinetOutput: model.OutputNormal, ActivePlanID: 3}
	e.Apply(snapAt(t0, st))

	steps := []struct {
		name   string
		change func(*model.SignalStatus)
		want   string
	}{
		{"coordination only", func(s *model.SignalStatus) { s.Mode = model.ModeFree }, "mode-changed"},
		{"cabinet output only", func(s *model.SignalStatus) { s.CabinetOutput, s.FlashCause = model.OutputFlash, model.FlashMonitor }, "cabinet-output-changed"},
		{"plan only", func(s *model.SignalStatus) { s.ActivePlanID = 4 }, "plan-changed"},
		{"preemption on", func(s *model.SignalStatus) { s.PreemptionActive, s.PreemptionSource = true, "rail" }, "preemption-activated"},
		{"preemption off", func(s *model.SignalStatus) { s.PreemptionActive, s.PreemptionSource = false, "" }, "preemption-cleared"},
	}
	for i, step := range steps {
		step.change(&st)
		got := kinds(e.Apply(snapAt(t0.Add(time.Duration(i+1)*time.Second), st)))
		want := []string{"operational-status-report", step.want}
		if !slices.Equal(got, want) {
			t.Errorf("%s: events = %v, want exactly %v", step.name, got, want)
		}
	}
}

// A reading that goes TO unknown is not evidence of a change (ADR 0013,
// ADR 0024): neither axis reports it, and the next known value is compared
// with the last known one, not with unknown.
func TestSignalMoveToUnknownIsNotAChange(t *testing.T) {
	t.Parallel()
	e := NewEngine(NewSignalDiffer())
	known := model.SignalStatus{Mode: model.ModeCoordinated, CabinetOutput: model.OutputNormal, ActivePlanID: 3}
	e.Apply(snapAt(t0, known))
	got := kinds(e.Apply(snapAt(t0.Add(time.Second), model.SignalStatus{ActivePlanID: 3})))
	if want := []string{"operational-status-report"}; !slices.Equal(got, want) {
		t.Fatalf("move to unknown: events = %v, want exactly %v", got, want)
	}
}

// A move FROM unknown to a known value is evidence, and reports with an
// unknown From; flash carries its cause.
func TestCabinetOutputFromUnknownToFlashCarriesCause(t *testing.T) {
	t.Parallel()
	e := NewEngine(NewSignalDiffer())
	e.Apply(snapAt(t0, model.SignalStatus{ActivePlanID: 3}))
	evs := e.Apply(snapAt(t0.Add(time.Second), model.SignalStatus{
		CabinetOutput: model.OutputFlash, FlashCause: model.FlashMonitor, ActivePlanID: 3,
	}))
	var got *model.CabinetOutputChanged
	for _, ev := range evs {
		if c, ok := ev.(model.CabinetOutputChanged); ok {
			got = &c
		}
	}
	if got == nil {
		t.Fatalf("no cabinet-output-changed in %v", kinds(evs))
	}
	if got.From != model.OutputUnknown || got.To != model.OutputFlash || got.Cause != model.FlashMonitor {
		t.Fatalf("event = %+v, want unknown → flash, cause monitor", *got)
	}
}

// Leaving flash carries no cause: Cause describes the flash being entered.
func TestCabinetOutputLeavingFlashCarriesNoCause(t *testing.T) {
	t.Parallel()
	e := NewEngine(NewSignalDiffer())
	e.Apply(snapAt(t0, model.SignalStatus{CabinetOutput: model.OutputFlash, FlashCause: model.FlashMonitor}))
	for _, ev := range e.Apply(snapAt(t0.Add(time.Second), model.SignalStatus{CabinetOutput: model.OutputNormal, FlashCause: model.FlashMonitor})) {
		if c, ok := ev.(model.CabinetOutputChanged); ok && c.Cause != model.FlashCauseUnknown {
			t.Fatalf("flash → normal carried cause %v", c.Cause)
		}
	}
}
