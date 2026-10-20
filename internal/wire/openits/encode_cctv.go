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
		// previous_state has no "unknown" either, so a tour whose prior state
		// we never observed encodes as STOPPED. That is only reachable if a
		// differ emitted a transition from an unknown state, which this one
		// does not — new tours stay silent until they have a known prior.
		from, _ := tourRunStateFor(v.From)
		msg = &cctvv1.TourStateChanged{
			Kind:           cctvTypes + "cctv-tour-state-changed",
			SourceDeviceId: v.DeviceID,
			OccurredAt:     timestamppb.New(v.OccurredAt.UTC()),
			ObservedBy:     e.collectorID,
			Sequence:       e.nextSequence(v.DeviceID),
			TourId:         v.TourID,
			PreviousState:  from,
			CurrentState:   to,
		}
	default:
		return nil, false
	}
	return msg, true
}
