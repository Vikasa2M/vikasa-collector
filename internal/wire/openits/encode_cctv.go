package openits

import (
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	cctvv1 "github.com/Vikasa2M/openits-models/pkg/proto/openits/cctv/v1"

	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

// encodeCCTV builds the payload for a CCTV event.
func (e *emitter) encodeCCTV(ev model.Event) (proto.Message, bool) {
	var msg proto.Message
	switch v := ev.(type) {
	case model.CCTVControlModeChanged:
		current, ok := cctvControlModeIdentity(v.To)
		if !ok {
			return nil, false
		}
		prior, _ := cctvControlModeIdentity(v.From)
		msg = e.modeChanged(v.Base, cctvTypes+"cctv-mode-event-kind", prior, current)

	case model.CCTVTourStateChanged:
		to, ok := tourRunStateFor(v.To)
		if !ok {
			return nil, false
		}
		// previous_state is optional: a prior state we never observed is
		// absent rather than a claim the tour was stopped (the differ only
		// reports transitions from a known prior today, so this is defence).
		tc := &cctvv1.TourStateChanged{
			Kind:           cctvTypes + "cctv-tour-state-changed",
			SourceDeviceId: v.DeviceID,
			OccurredAt:     timestamppb.New(v.OccurredAt.UTC()),
			ObservedBy:     proto.String(e.collectorID),
			SequenceEpoch:  e.epoch,
			Sequence:       e.nextSequence(v.DeviceID),
			TourId:         v.TourID,
			CurrentState:   to,
		}
		if from, ok := tourRunStateFor(v.From); ok {
			tc.PreviousState = from.Enum()
		}
		msg = tc
	default:
		return nil, false
	}
	return msg, true
}
