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
		}
		// Only kind, device, time, observer and sequence. The catalog's
		// attempt counters belong to comm-attempt-window, and the YANG
		// forbids them on comm-lost ("a contradictory instance"); the read
		// error stays in the collector's log and /status, not on the wire.
		msg = &commonv1.CommHealthEvent{
			Kind:           kind,
			SourceDeviceId: v.DeviceID,
			OccurredAt:     timestamppb.New(v.OccurredAt.UTC()),
			ObservedBy:     e.collectorID,
			Sequence:       e.nextSequence(v.DeviceID),
		}

	case model.FaultRaised:
		// Unlike modes, an unmappable category does NOT decline the event —
		// faultKindIdentity falls back to the service base identity. It only
		// fails for a device kind this emitter does not serve, which the
		// ce-type lookup above has already excluded.
		kind, ok := faultKindIdentity(v.Category, v.DeviceKind)
		if !ok {
			return nil, false
		}
		msg = &commonv1.FaultRaised{
			Kind:           kind,
			SourceDeviceId: v.DeviceID,
			OccurredAt:     timestamppb.New(v.OccurredAt.UTC()),
			ObservedBy:     e.collectorID,
			Sequence:       e.nextSequence(v.DeviceID),
			FaultId:        v.FaultID,
			Severity:       severityFor(v.Severity),
			Description:    v.Description,
		}

	case model.FaultCleared:
		kind, ok := faultKindIdentity(model.CategoryUnknown, v.DeviceKind)
		if !ok {
			return nil, false
		}
		msg = &commonv1.FaultCleared{
			// A clear carries no category of its own — the domain FaultCleared
			// has only the id, because the differ identifies it by set
			// difference. The service base identity is the honest `kind`:
			// consumers correlate raise and clear on (source-device-id,
			// fault-id), not on kind.
			Kind:           kind,
			SourceDeviceId: v.DeviceID,
			OccurredAt:     timestamppb.New(v.OccurredAt.UTC()),
			ObservedBy:     e.collectorID,
			Sequence:       e.nextSequence(v.DeviceID),
			FaultId:        v.FaultID,
		}
	default:
		return nil, false
	}
	return msg, true
}
