package openits

import (
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	scv1 "github.com/Vikasa2M/openits-models/pkg/proto/openits/signal_control/v1"

	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

// encodeSignalControl builds the payload for a signal-control event.
func (e *emitter) encodeSignalControl(ev model.Event) (proto.Message, bool) {
	var msg proto.Message
	switch v := ev.(type) {
	case model.ModeChanged:
		// `current` is a mandatory identityref. A domain mode with no
		// controller-mode identity upstream cannot be encoded faithfully, so
		// the event is NOT claimed and falls through to the loud-drop path —
		// the same rule as an unknown DeviceKind. Never substitute a
		// near-neighbour identity to make a warning go away.
		current, ok := controllerModeIdentity(v.To)
		if !ok {
			return nil, false
		}
		// `prior` is optional ("absent when the device just started up"), so an
		// unmappable From is left empty rather than blocking the event.
		prior, _ := controllerModeIdentity(v.From)
		msg = e.modeChanged(v.Base, scTypes+"sc-mode-event-kind", prior, current)

	case model.PlanChanged:
		// PlanApplied names only the plan now in force. FromPlanID is DROPPED:
		// there is no wire field for it, and the transition is recoverable from
		// consecutive events. Recorded here as the map-or-drop decision it is.
		msg = &scv1.PlanApplied{
			Kind:           scTypes + "sc-coordination-event-kind",
			SourceDeviceId: v.DeviceID,
			OccurredAt:     timestamppb.New(v.OccurredAt.UTC()),
			ObservedBy:     proto.String(e.collectorID),
			SequenceEpoch:  e.epoch,
			Sequence:       e.nextSequence(v.DeviceID),
			PlanId:         proto.Uint32(v.ToPlanID),
		}

	case model.OperationalStatusReport:
		// Same decline rule as ModeChanged: `mode` asserts a specific state, so
		// a mode with no upstream identity cannot be reported honestly.
		mode, ok := controllerModeIdentity(v.Mode)
		if !ok {
			return nil, false
		}
		// ActivePlanID is DROPPED: the wire OperationalStatusReport has no plan
		// field. It is not lost to consumers — plan-applied carries it.
		msg = &scv1.OperationalStatusReport{
			Kind:           scTypes + "sc-mode-event-kind",
			SourceDeviceId: v.DeviceID,
			OccurredAt:     timestamppb.New(v.OccurredAt.UTC()),
			ObservedBy:     proto.String(e.collectorID),
			SequenceEpoch:  e.epoch,
			Sequence:       e.nextSequence(v.DeviceID),
			Mode:           proto.String(mode),
			FlashActive:    proto.Bool(v.InConflictFlash),
		}

	case model.PreemptionActivated:
		// preempt_number and type have no domain source yet; left unset rather
		// than invented. The adapter that learns them can fill them later
		// without a wire change, since both are additive-optional.
		msg = &scv1.PreemptionActivated{
			Kind:           scTypes + "sc-preemption-event-kind",
			SourceDeviceId: v.DeviceID,
			OccurredAt:     timestamppb.New(v.OccurredAt.UTC()),
			ObservedBy:     proto.String(e.collectorID),
			SequenceEpoch:  e.epoch,
			Sequence:       e.nextSequence(v.DeviceID),
			SourceId:       optString(v.Source),
		}

	case model.PreemptionCleared:
		msg = &scv1.PreemptionCleared{
			Kind:           scTypes + "sc-preemption-event-kind",
			SourceDeviceId: v.DeviceID,
			OccurredAt:     timestamppb.New(v.OccurredAt.UTC()),
			ObservedBy:     proto.String(e.collectorID),
			SequenceEpoch:  e.epoch,
			Sequence:       e.nextSequence(v.DeviceID),
		}

	case model.DetectorReport:
		dets := make([]*scv1.DetectorReportDetector, 0, len(v.Readings))
		for _, r := range v.Readings {
			dets = append(dets, &scv1.DetectorReportDetector{
				DetectorId: proto.Uint32(r.Channel),
				Volume:     proto.Uint32(r.VolumeDelta),
				Occupancy:  proto.String(occupancyPercent(r.OccupancyTenths)),
			})
		}
		msg = &scv1.DetectorReport{
			Kind:           scTypes + "sc-detector-event-kind",
			SourceDeviceId: v.DeviceID,
			OccurredAt:     timestamppb.New(v.OccurredAt.UTC()),
			ObservedBy:     proto.String(e.collectorID),
			SequenceEpoch:  e.epoch,
			Sequence:       e.nextSequence(v.DeviceID),
			IntervalStart:  timestamppb.New(v.IntervalStart.UTC()),
			// The domain carries true elapsed time; the wire constrains the
			// interval to whole seconds. Round ONCE, here at the edge, rather
			// than pre-breaking the domain to match the wire.
			IntervalDurationS: proto.Uint32(wholeSeconds(v.IntervalDuration)),
			Detector:          dets,
		}
	default:
		return nil, false
	}
	return msg, true
}
