package openits

import (
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	rlv1 "github.com/Vikasa2M/openits-models/pkg/proto/openits/reversible_lane/v1"

	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

// encodeReversibleLane builds the payload for a reversible-lane (ACS) event.
func (e *emitter) encodeReversibleLane(ev model.Event) (proto.Message, bool) {
	switch v := ev.(type) {
	case model.GateStatusChanged:
		prevPos := gatePositionFor(v.PreviousPosition)
		msg := &rlv1.GateStatusChanged{
			Kind:                  rlTypes + "rl-gate-status-changed",
			SourceDeviceId:        v.DeviceID,
			OccurredAt:            timestamppb.New(v.OccurredAt.UTC()),
			ObservedBy:            proto.String(e.collectorID),
			SequenceEpoch:         e.epoch,
			Sequence:              e.nextSequence(v.DeviceID),
			GateId:                v.GateID,
			Position:              gatePositionFor(v.Position),
			PreviousPosition:      &prevPos,
			OperatingMode:         optString(gateOperatingModeFor(v.Mode)),
			PreviousOperatingMode: optString(gateOperatingModeFor(v.PreviousMode)),
			InterlockBypassed:     interlockBypassedFor(v.Mode),
		}
		return msg, true
	default:
		return nil, false
	}
}
