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
	cases := []struct {
		name     string
		mode     model.GateMode
		prev     model.GateMode
		wantMode string
		wantPrev string
		bypass   bool // true means the optional leaf is present and true
	}{
		{"automatic", model.GateModeAuto, model.GateModeOffline, auto, offline, false},
		{"manual", model.GateModeManual, model.GateModeAuto, manual, auto, false},
		{"offline", model.GateModeOffline, model.GateModeManual, offline, manual, false},
		{"unknown leaves mode absent", model.GateModeUnknown, model.GateModeAuto, "", auto, false},
		{"manual non-interlocked", model.GateModeManualNonInterlocked, model.GateModeManual, manual, manual, true},
		{"leaving manual non-interlocked", model.GateModeManual, model.GateModeManualNonInterlocked, manual, manual, false},
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
			if tc.bypass {
				if msg.InterlockBypassed == nil || !msg.GetInterlockBypassed() {
					t.Fatalf("interlock_bypassed = %v, want true", msg.InterlockBypassed)
				}
			} else if msg.InterlockBypassed != nil {
				t.Fatalf("interlock_bypassed = %v, want unset", msg.GetInterlockBypassed())
			}
		})
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
