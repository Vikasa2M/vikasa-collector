package openits

import (
	"testing"

	"google.golang.org/protobuf/proto"

	rlv1 "github.com/Vikasa2M/openits-models/pkg/proto/openits/reversible_lane/v1"

	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

func TestGateOperatingModeAndInterlockBypass(t *testing.T) {
	const (
		auto    = "openits-reversible-lane-types:reversible-lane-gate-mode-automatic"
		manual  = "openits-reversible-lane-types:reversible-lane-gate-mode-manual"
		offline = "openits-reversible-lane-types:reversible-lane-gate-mode-offline"
	)
	// bypass: nil = unset, &true / &false = present with that value.
	tTrue, tFalse := true, false
	cases := []struct {
		name     string
		mode     model.GateMode
		prev     model.GateMode
		wantMode string
		wantPrev string
		bypass   *bool
	}{
		{"automatic", model.GateModeAuto, model.GateModeOffline, auto, offline, &tFalse},
		{"manual", model.GateModeManual, model.GateModeAuto, manual, auto, &tFalse},
		{"offline", model.GateModeOffline, model.GateModeManual, offline, manual, nil},
		{"unknown leaves mode absent", model.GateModeUnknown, model.GateModeAuto, "", auto, nil},
		{"manual non-interlocked", model.GateModeManualNonInterlocked, model.GateModeManual, manual, manual, &tTrue},
		{"leaving manual non-interlocked", model.GateModeManual, model.GateModeManualNonInterlocked, manual, manual, &tFalse},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg := encodeGate(t, tc.mode, tc.prev)
			if got := msg.GetOperatingMode(); got != tc.wantMode {
				t.Fatalf("operating mode = %q, want %q", got, tc.wantMode)
			}
			if tc.wantMode == "" && msg.OperatingMode != nil {
				t.Fatal("operating mode set for an unread mode")
			}
			if got := msg.GetPreviousOperatingMode(); got != tc.wantPrev {
				t.Fatalf("previous mode = %q, want %q", got, tc.wantPrev)
			}
			if tc.bypass == nil {
				if msg.InterlockBypassed != nil {
					t.Fatalf("interlock_bypassed = %v, want unset", msg.GetInterlockBypassed())
				}
				return
			}
			if msg.InterlockBypassed == nil || msg.GetInterlockBypassed() != *tc.bypass {
				t.Fatalf("interlock_bypassed = %v, want %v", msg.InterlockBypassed, *tc.bypass)
			}
		})
	}
}

// Mode 3 (Manual Non-Interlocked) -> Automatic must carry interlock_bypassed=false
// on the resulting gate-status-changed, not leave the leaf unset.
func TestInterlockBypassedFalseWhenLeavingMode3ForAutomatic(t *testing.T) {
	msg := encodeGate(t, model.GateModeAuto, model.GateModeManualNonInterlocked)
	if msg.GetOperatingMode() != "openits-reversible-lane-types:reversible-lane-gate-mode-automatic" {
		t.Fatalf("mode = %q", msg.GetOperatingMode())
	}
	if msg.InterlockBypassed == nil || msg.GetInterlockBypassed() {
		t.Fatalf("interlock_bypassed = %v, want false", msg.InterlockBypassed)
	}
}

func encodeGate(t *testing.T, mode, prev model.GateMode) *rlv1.GateStatusChanged {
	t.Helper()
	enc, ok, err := New("collector", testEpoch).Encode(model.GateStatusChanged{
		Base:             gbase("acs-1", "acs"),
		GateID:           "WG-1",
		Position:         model.GatePositionClosed,
		PreviousPosition: model.GatePositionClosed,
		Mode:             mode,
		PreviousMode:     prev,
	})
	if err != nil || !ok {
		t.Fatalf("encode ok=%v err=%v", ok, err)
	}
	var msg rlv1.GateStatusChanged
	if err := proto.Unmarshal(enc.Data, &msg); err != nil {
		t.Fatal(err)
	}
	return &msg
}
