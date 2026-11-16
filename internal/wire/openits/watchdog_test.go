package openits

import (
	"context"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	commonv1 "github.com/Vikasa2M/openits-models/pkg/proto/openits/common/v1"

	"github.com/Vikasa2M/vikasa-collector/internal/synth"
	"github.com/Vikasa2M/vikasa-collector/internal/vendors/cameleon"
	"github.com/Vikasa2M/vikasa-collector/internal/wire"
	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

const (
	watchdogControllerKind = "openits-reversible-lane-types:reversible-lane-fault-controller"
	watchdogFaultID        = "plc/not-scanning"
	watchdogDescription    = "PLC watchdog has not advanced"
)

// TestWatchdogStallEncodesControllerKind drives a scripted ACS counter
// through the fault differ and the openits emitter. A stall is a successful
// observation of a counter that stopped, so the wire kind is the controller
// leaf. A failed read is not that observation.
func TestWatchdogStallEncodesControllerKind(t *testing.T) {
	start := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	firstStall := start.Add(5 * time.Second)
	secondStall := start.Add(13 * time.Second)
	plc := cameleon.NewScriptedPLC(29, 5*time.Second, start)
	engine := synth.NewEngine(synth.NewFaultDiffer())
	em := New("cabinet-poller-1", testEpoch)

	if evs := applyRead(t, engine, plc); len(evs) != 0 {
		t.Fatalf("initial sample at counter 29 events = %v; a first sample must not raise a stall", evs)
	}
	plc.SetClock(start.Add(4 * time.Second))
	if evs := applyRead(t, engine, plc); len(evs) != 0 {
		t.Fatalf("counter 29 before the stall window events = %v", evs)
	}

	plc.SetClock(firstStall)
	raisedEv := oneFault(t, applyRead(t, engine, plc), "counter 29 stalled at the configured threshold")
	raisedDomain, ok := raisedEv.(model.FaultRaised)
	if !ok {
		t.Fatalf("stall event %T, want FaultRaised", raisedEv)
	}
	if raisedDomain.FaultID != watchdogFaultID || raisedDomain.Type != model.FaultTypePLCNotScanning ||
		raisedDomain.Severity != model.SeverityCritical || raisedDomain.Description != watchdogDescription ||
		raisedDomain.GateID != "" {
		t.Fatalf("stall domain event = %+v", raisedDomain)
	}
	raised := decodeWatchdogRaised(t, em, raisedEv)
	assertControllerFault(t, raised.GetKind(), raised.GetFaultId(), raised.GetDescription(), raised.GetComponent() != nil, raised.GetSeverity())
	requireFirstObserved(t, raised.GetFirstObserved(), firstStall, "first raise")

	plc.FailReads(context.DeadlineExceeded)
	_, err := plc.Read(context.Background())
	if err == nil {
		t.Fatal("failed read returned no error")
	}
	// This test manually supplies a reachability event to the catalog emitter.
	// It verifies that the emitter declines it. It does not execute or prove
	// runner or health publication.
	enc, ok, encErr := em.Encode(model.DeviceStatusChanged{
		Base:                model.Base{DeviceID: "acs-1", DeviceKind: "acs", OccurredAt: firstStall},
		Reachable:           false,
		Reason:              err.Error(),
		ConsecutiveFailures: 1,
		LastContact:         start,
	})
	if encErr != nil || ok || enc != nil {
		t.Fatalf("catalog emitter accepted the supplied reachability event: ok=%v err=%v", ok, encErr)
	}

	plc.Resume()
	plc.SetClock(start.Add(6 * time.Second))
	if evs := applyRead(t, engine, plc); len(evs) != 0 {
		t.Fatalf("unchanged counter 29 after the failed read = %v; must not clear or re-raise", evs)
	}

	plc.SetCounter(30)
	plc.SetClock(start.Add(7 * time.Second))
	clearedEv := oneFault(t, applyRead(t, engine, plc), "counter 29 -> 30")
	if _, ok := clearedEv.(model.FaultCleared); !ok {
		t.Fatalf("counter 29 -> 30 event %T, want FaultCleared", clearedEv)
	}
	cleared := decodeWatchdogCleared(t, em, clearedEv)
	if cleared.GetKind() != watchdogControllerKind || cleared.GetFaultId() != watchdogFaultID || cleared.GetComponent() != nil {
		t.Fatalf("clear kind=%q id=%q component=%v", cleared.GetKind(), cleared.GetFaultId(), cleared.GetComponent())
	}
	requireFirstObserved(t, cleared.GetFirstObserved(), firstStall, "clear after 29 -> 30")

	plc.SetCounter(31)
	plc.SetClock(start.Add(8 * time.Second))
	if evs := applyRead(t, engine, plc); len(evs) != 0 {
		t.Fatalf("counter change 30 -> 31 events = %v", evs)
	}
	plc.SetClock(secondStall)
	raisedEv = oneFault(t, applyRead(t, engine, plc), "second stall at counter 31")
	raised = decodeWatchdogRaised(t, em, raisedEv)
	assertControllerFault(t, raised.GetKind(), raised.GetFaultId(), raised.GetDescription(), raised.GetComponent() != nil, raised.GetSeverity())
	requireFirstObserved(t, raised.GetFirstObserved(), secondStall, "second raise")
	if raised.GetFirstObserved().AsTime().Equal(firstStall) {
		t.Fatal("second stall reused the first observation time")
	}

	plc.SetCounter(0)
	plc.SetClock(start.Add(14 * time.Second))
	clearedEv = oneFault(t, applyRead(t, engine, plc), "counter wrap 31 -> 0")
	if _, ok := clearedEv.(model.FaultCleared); !ok {
		t.Fatalf("wrap event %T, want FaultCleared; wrap 31 -> 0 must count as progress", clearedEv)
	}
	cleared = decodeWatchdogCleared(t, em, clearedEv)
	if cleared.GetKind() != watchdogControllerKind || cleared.GetFaultId() != watchdogFaultID || cleared.GetComponent() != nil {
		t.Fatalf("wrap clear kind=%q id=%q component=%v", cleared.GetKind(), cleared.GetFaultId(), cleared.GetComponent())
	}
	requireFirstObserved(t, cleared.GetFirstObserved(), secondStall, "clear after wrap 31 -> 0")
}

func applyRead(t *testing.T, engine *synth.Engine, plc *cameleon.ScriptedPLC) []model.Event {
	t.Helper()
	snap, err := plc.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return engine.Apply(snap)
}

func oneFault(t *testing.T, evs []model.Event, what string) model.Event {
	t.Helper()
	if len(evs) != 1 {
		t.Fatalf("%s: events = %v, want one fault", what, evs)
	}
	return evs[0]
}

func decodeWatchdogRaised(t *testing.T, em wire.Emitter, ev model.Event) *commonv1.FaultRaised {
	t.Helper()
	enc, ok, err := em.Encode(ev)
	if err != nil || !ok {
		t.Fatalf("encode raise: ok=%v err=%v", ok, err)
	}
	if enc.CEType != "openits.reversible-lane.fault-raised.v1" {
		t.Fatalf("raise ce-type = %q", enc.CEType)
	}
	var got commonv1.FaultRaised
	if err := proto.Unmarshal(enc.Data, &got); err != nil {
		t.Fatal(err)
	}
	return &got
}

func decodeWatchdogCleared(t *testing.T, em wire.Emitter, ev model.Event) *commonv1.FaultCleared {
	t.Helper()
	enc, ok, err := em.Encode(ev)
	if err != nil || !ok {
		t.Fatalf("encode clear: ok=%v err=%v", ok, err)
	}
	if enc.CEType != "openits.reversible-lane.fault-cleared.v1" {
		t.Fatalf("clear ce-type = %q", enc.CEType)
	}
	var got commonv1.FaultCleared
	if err := proto.Unmarshal(enc.Data, &got); err != nil {
		t.Fatal(err)
	}
	return &got
}

func requireFirstObserved(t *testing.T, got *timestamppb.Timestamp, want time.Time, what string) {
	t.Helper()
	if got == nil {
		t.Fatalf("%s: first_observed is absent", what)
	}
	if !got.AsTime().Equal(want) {
		t.Fatalf("%s: first_observed = %v, want %v", what, got.AsTime(), want)
	}
}

func assertControllerFault(t *testing.T, kind, id, description string, hasComponent bool, severity commonv1.FaultSeverity) {
	t.Helper()
	if kind != watchdogControllerKind || id != watchdogFaultID || description != watchdogDescription ||
		hasComponent || severity != commonv1.FaultSeverity_FAULT_SEVERITY_CRITICAL {
		t.Fatalf("kind=%q id=%q description=%q component=%v severity=%v", kind, id, description, hasComponent, severity)
	}
}
