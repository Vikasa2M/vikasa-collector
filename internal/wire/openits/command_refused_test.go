package openits

import (
	"testing"

	"google.golang.org/protobuf/proto"

	commonv1 "github.com/Vikasa2M/openits-models/pkg/proto/openits/common/v1"

	"github.com/Vikasa2M/vikasa-collector/internal/synth"
	"github.com/Vikasa2M/vikasa-collector/internal/vendors/cameleon"
	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

const (
	commandRefusedKind = "openits-vendor-cameleon-reversible-lane-types:cameleon-gate-command-refused"
	interlockKind      = "openits-reversible-lane-types:reversible-lane-fault-interlock"
	commandRefusedDesc = "command refused because of PLC interlock or device is offline or invalid PLC cabinet ID"
)

// TestCommandRefusedAlarm_DecodeDifferEmit drives a synthetic gate word
// with only the command-refused alarm set through the ACS decoder, the
// fault differ, and the protobuf emitter.
func TestCommandRefusedAlarm_DecodeDifferEmit(t *testing.T) {
	const gateID = "gate-1"
	const faultID = "gate/gate-1/command-refused"
	// 0x0025 is automatic, lock-open, closed, and no alarm bits.
	const quiet uint16 = 0x0025
	refused := quiet | (1 << 13)

	read := func(word uint16) *model.Snapshot {
		t.Helper()
		block := []uint16{word}
		snap, err := cameleon.ReadBlock("acs-1", 90, block, []cameleon.GateWord{{
			ID: gateID, Kind: model.GateKindWarning, DSW: 90,
		}}, cameleon.CabinetWords{})
		if err != nil {
			t.Fatal(err)
		}
		snap.DeviceKind = "acs"
		return snap
	}

	eng := synth.NewEngine(synth.NewFaultDiffer())
	raisedEvs := eng.Apply(read(refused))
	if len(raisedEvs) != 1 {
		t.Fatalf("raise events = %d, want 1", len(raisedEvs))
	}
	raised, ok := raisedEvs[0].(model.FaultRaised)
	if !ok {
		t.Fatalf("raise event %T", raisedEvs[0])
	}
	if raised.FaultID != faultID || raised.Type != model.FaultTypeGateCommandRefused || raised.GateID != gateID {
		t.Fatalf("raised domain = %+v", raised)
	}
	if raised.Severity != model.SeverityWarning || raised.Description != commandRefusedDesc {
		t.Fatalf("severity/description = %v %q", raised.Severity, raised.Description)
	}

	em := New("collector", testEpoch)
	enc, ok, err := em.Encode(raised)
	if err != nil || !ok {
		t.Fatalf("raise Encode: ok=%v err=%v", ok, err)
	}
	if enc.CEType != "openits.reversible-lane.fault-raised.v1" {
		t.Fatalf("raise ce-type = %q", enc.CEType)
	}
	var msg commonv1.FaultRaised
	if err := proto.Unmarshal(enc.Data, &msg); err != nil {
		t.Fatal(err)
	}
	assertCommandRefusedWire(t, msg.GetKind(), msg.GetFaultId(), msg.GetComponent().GetComponentClass(), msg.GetComponent().GetComponentId(), faultID, gateID)
	if msg.GetSeverity() != commonv1.FaultSeverity_FAULT_SEVERITY_WARNING {
		t.Errorf("severity = %s", msg.GetSeverity())
	}
	if msg.GetDescription() != commandRefusedDesc {
		t.Errorf("description = %q", msg.GetDescription())
	}
	if msg.GetFirstObserved() == nil {
		t.Fatal("raise first_observed is unset")
	}

	clearedEvs := eng.Apply(read(quiet))
	if len(clearedEvs) != 1 {
		t.Fatalf("clear events = %d, want 1", len(clearedEvs))
	}
	cleared, ok := clearedEvs[0].(model.FaultCleared)
	if !ok {
		t.Fatalf("clear event %T", clearedEvs[0])
	}
	enc, ok, err = em.Encode(cleared)
	if err != nil || !ok {
		t.Fatalf("clear Encode: ok=%v err=%v", ok, err)
	}
	if enc.CEType != "openits.reversible-lane.fault-cleared.v1" {
		t.Fatalf("clear ce-type = %q", enc.CEType)
	}
	var clearMsg commonv1.FaultCleared
	if err := proto.Unmarshal(enc.Data, &clearMsg); err != nil {
		t.Fatal(err)
	}
	assertCommandRefusedWire(t, clearMsg.GetKind(), clearMsg.GetFaultId(), clearMsg.GetComponent().GetComponentClass(), clearMsg.GetComponent().GetComponentId(), faultID, gateID)
	if clearMsg.GetFirstObserved() == nil || !clearMsg.GetFirstObserved().AsTime().Equal(msg.GetFirstObserved().AsTime()) {
		t.Fatalf("clear first_observed = %v, want raise %v", clearMsg.GetFirstObserved(), msg.GetFirstObserved())
	}

	// The kind comes from the fault type. A different fault-id string
	// does not change it, and it is not the interlock identity.
	renamed := raised
	renamed.FaultID = "alarm/gate-1/unspecified"
	enc, ok, err = em.Encode(renamed)
	if err != nil || !ok {
		t.Fatalf("renamed Encode: ok=%v err=%v", ok, err)
	}
	var renamedMsg commonv1.FaultRaised
	if err := proto.Unmarshal(enc.Data, &renamedMsg); err != nil {
		t.Fatal(err)
	}
	assertCommandRefusedWire(t, renamedMsg.GetKind(), renamedMsg.GetFaultId(), renamedMsg.GetComponent().GetComponentClass(), renamedMsg.GetComponent().GetComponentId(), renamed.FaultID, gateID)
}

// A second alarm set at the same time does not retarget command-refused.
func TestCommandRefusedAlarm_OtherBitDoesNotReclassify(t *testing.T) {
	const gateID = "gate-1"
	word := uint16(0x0025) | (1 << 13) | (1 << 14)
	snap, err := cameleon.ReadBlock("acs-1", 90, []uint16{word}, []cameleon.GateWord{{
		ID: gateID, Kind: model.GateKindWarning, DSW: 90,
	}}, cameleon.CabinetWords{})
	if err != nil {
		t.Fatal(err)
	}
	snap.DeviceKind = "acs"
	evs := synth.NewEngine(synth.NewFaultDiffer()).Apply(snap)
	em := New("collector", testEpoch)
	var sawRefused, sawInvalid bool
	for _, ev := range evs {
		fr, ok := ev.(model.FaultRaised)
		if !ok {
			t.Fatalf("event %T", ev)
		}
		enc, ok, err := em.Encode(fr)
		if err != nil || !ok {
			t.Fatalf("%s Encode: ok=%v err=%v", fr.FaultID, ok, err)
		}
		var msg commonv1.FaultRaised
		if err := proto.Unmarshal(enc.Data, &msg); err != nil {
			t.Fatal(err)
		}
		if msg.GetKind() == interlockKind {
			t.Errorf("%s encoded as interlock", fr.FaultID)
		}
		switch fr.FaultID {
		case "gate/gate-1/command-refused":
			sawRefused = true
			if msg.GetKind() != commandRefusedKind {
				t.Errorf("command-refused kind = %q", msg.GetKind())
			}
		case "gate/gate-1/invalid-inputs":
			sawInvalid = true
		default:
			t.Errorf("unexpected fault %s kind %s", fr.FaultID, msg.GetKind())
		}
	}
	if !sawRefused || !sawInvalid {
		t.Fatalf("saw refused=%v invalid-inputs=%v", sawRefused, sawInvalid)
	}
}

func assertCommandRefusedWire(t *testing.T, kind, faultID, class, componentID, wantFault, wantGate string) {
	t.Helper()
	if kind != commandRefusedKind {
		t.Errorf("kind = %q", kind)
	}
	if kind == interlockKind {
		t.Errorf("kind is the interlock identity")
	}
	if faultID != wantFault {
		t.Errorf("fault-id = %q", faultID)
	}
	if class != "openits-reversible-lane-types:rl-component-gate" || componentID != wantGate {
		t.Errorf("component = %s/%s", class, componentID)
	}
}
