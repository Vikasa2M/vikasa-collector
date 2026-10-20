package model

import (
	"testing"
	"time"
)

func TestSnapshotFacetLookup(t *testing.T) {
	t.Parallel()
	s := &Snapshot{
		DeviceID:  "asc-1",
		SampledAt: time.Date(2026, 7, 12, 0, 0, 0, 0, time.UTC),
		Facets:    []Facet{SignalStatus{CabinetOutput: OutputNormal, ActivePlanID: 3}},
		Errors:    []FacetError{{Kind: Kind("detector-samples"), Err: "timeout"}},
	}

	f, ok := s.Facet(KindSignalStatus)
	if !ok {
		t.Fatal("expected signal-status facet present")
	}
	if got := f.(SignalStatus).ActivePlanID; got != 3 {
		t.Fatalf("ActivePlanID = %d, want 3", got)
	}
	if _, ok := s.Facet(Kind("dms-status")); ok {
		t.Fatal("unexpected facet found")
	}
	if !s.FacetFailed(Kind("detector-samples")) {
		t.Fatal("expected detector-samples marked failed")
	}
	if s.FacetFailed(KindSignalStatus) {
		t.Fatal("signal-status must not be marked failed")
	}
}

func TestControllerModeString(t *testing.T) {
	t.Parallel()
	cases := map[ControllerMode]string{
		ModeUnknown: "unknown", ModeCoordinated: "coordinated", ModeFree: "free", ControllerMode(99): "unknown",
	}
	for mode, want := range cases {
		if got := mode.String(); got != want {
			t.Errorf("%d.String() = %q, want %q", mode, got, want)
		}
	}
}

func TestCabinetOutputAndFlashCauseString(t *testing.T) {
	t.Parallel()
	for o, want := range map[CabinetOutput]string{
		OutputUnknown: "unknown", OutputNormal: "normal", OutputFlash: "flash", OutputDark: "dark", CabinetOutput(99): "unknown",
	} {
		if got := o.String(); got != want {
			t.Errorf("CabinetOutput(%d).String() = %q, want %q", o, got, want)
		}
	}
	for c, want := range map[FlashCause]string{
		FlashCauseUnknown: "unknown", FlashMonitor: "monitor", FlashControllerFault: "controller-fault",
		FlashAutomatic: "automatic", FlashManual: "manual", FlashStartup: "startup",
		FlashPreempt: "preempt", FlashScheduled: "scheduled", FlashOther: "other", FlashCause(99): "unknown",
	} {
		if got := c.String(); got != want {
			t.Errorf("FlashCause(%d).String() = %q, want %q", c, got, want)
		}
	}
}
