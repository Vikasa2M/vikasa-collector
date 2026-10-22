package openits

import (
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	commonv1 "github.com/Vikasa2M/openits-models/pkg/proto/openits/common/v1"

	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

// encodeShared builds the payload for the events every device kind shares:
// reachability and the fault family. ceTypeFor routes them per service.
func (e *emitter) encodeShared(ev model.Event) (proto.Message, bool) {
	var msg proto.Message
	switch v := ev.(type) {
	case model.DeviceStatusChanged:
		kind := commHealth + "comm-lost"
		if v.Reachable {
			kind = commHealth + "comm-restored"
		} else if v.LastContact.IsZero() {
			// comm-lost must carry last-contact (openits-models v0.8.0). A
			// device that has not answered since boot has none, so the event
			// is declined and falls through to the collector-owned
			// device-status-changed, which makes no such claim (ADR 0024).
			return nil, false
		}
		// Only kind, device, time, observer and sequence. The catalog's
		// attempt counters belong to comm-attempt-window, and the YANG
		// forbids them on comm-lost ("a contradictory instance"); the read
		// error stays in the collector's log and /status, not on the wire.
		msg = &commonv1.CommHealthEvent{
			Kind:           kind,
			SourceDeviceId: v.DeviceID,
			OccurredAt:     timestamppb.New(v.OccurredAt.UTC()),
			ObservedBy:     proto.String(e.collectorID),
			SequenceEpoch:  e.epoch,
			Sequence:       e.nextSequence(v.DeviceID),
		}
		if !v.Reachable {
			msg.(*commonv1.CommHealthEvent).LastContact = timestamppb.New(v.LastContact.UTC())
		}

	case model.FaultRaised:
		// For most services an unmappable category falls back to the service
		// base identity. ACS is the exception: the catalog rejects the base
		// kind, so faultKindIdentity returns ok=false and Encode declines
		// (loud-drop) rather than approximating a near-neighbour leaf.
		kind, ok := faultKindIdentity(v.Category, v.DeviceKind, v.Type)
		if !ok {
			return nil, false
		}
		msg = &commonv1.FaultRaised{
			Kind:           kind,
			SourceDeviceId: v.DeviceID,
			OccurredAt:     timestamppb.New(v.OccurredAt.UTC()),
			ObservedBy:     proto.String(e.collectorID),
			SequenceEpoch:  e.epoch,
			Sequence:       e.nextSequence(v.DeviceID),
			FaultId:        v.FaultID,
			Severity:       severityFor(v.Severity),
			Description:    optString(v.Description),
			FirstObserved:  optTime(v.FirstObserved),
			Component:      gateComponent(v.GateID),
		}

	case model.FaultCleared:
		kind, ok := faultKindIdentity(v.Category, v.DeviceKind, v.Type)
		if !ok {
			return nil, false
		}
		msg = &commonv1.FaultCleared{
			// The clear names the same fault class as its raise: the differ
			// carries the cleared fault's category and type from the remembered
			// state (openits-models v0.8.0 conformance requires a service
			// fault kind on both halves, not the base event kind).
			Kind:           kind,
			SourceDeviceId: v.DeviceID,
			OccurredAt:     timestamppb.New(v.OccurredAt.UTC()),
			ObservedBy:     proto.String(e.collectorID),
			SequenceEpoch:  e.epoch,
			Sequence:       e.nextSequence(v.DeviceID),
			FaultId:        v.FaultID,
			FirstObserved:  optTime(v.FirstObserved),
			Component:      gateComponent(v.GateID),
		}
	default:
		return nil, false
	}
	return msg, true
}
