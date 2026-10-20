package openits

import (
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	dmsv1 "github.com/Vikasa2M/openits-models/pkg/proto/openits/dms/v1"

	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

// encodeDMS builds the payload for a DMS event.
func (e *emitter) encodeDMS(ev model.Event) (proto.Message, bool) {
	var msg proto.Message
	switch v := ev.(type) {
	case model.DMSControlModeChanged:
		current, ok := dmsControlModeIdentity(v.To)
		if !ok {
			return nil, false
		}
		prior, _ := dmsControlModeIdentity(v.From)
		msg = e.modeChanged(v.Base, dmsTypes+"dms-mode-event-kind", prior, current)

	case model.DMSDisplayStateChanged:
		current, ok := dmsDisplayStateIdentity(v.To)
		if !ok {
			return nil, false
		}
		prior, _ := dmsDisplayStateIdentity(v.From)
		msg = e.modeChanged(v.Base, dmsTypes+"dms-mode-event-kind", prior, current)

	case model.DMSMessageActivationFailed:
		msg = &dmsv1.MessageActivationFailed{
			Kind:                dmsTypes + "dms-message-activation-failed",
			SourceDeviceId:      v.DeviceID,
			OccurredAt:          timestamppb.New(v.OccurredAt.UTC()),
			ObservedBy:          e.collectorID,
			Sequence:            e.nextSequence(v.DeviceID),
			AttemptedMemoryType: memoryTypeFor(v.MemoryType),
			AttemptedSlotNumber: v.Slot,
			ErrorType:           errorTypeFor(v.Error),
			ErrorPosition:       v.ErrorPosition,
		}

	case model.DMSMessageChanged:
		mc := &dmsv1.MessageChanged{
			Kind:           dmsTypes + "dms-message-changed",
			SourceDeviceId: v.DeviceID,
			OccurredAt:     timestamppb.New(v.OccurredAt.UTC()),
			ObservedBy:     e.collectorID,
			Sequence:       e.nextSequence(v.DeviceID),
			MemoryType:     memoryTypeFor(v.ToMemoryType),
			SlotNumber:     v.ToSlot,
			MultiString:    v.ToText,
			Crc:            v.ToCRC,
			// FromCRC is DROPPED: the wire carries no prior-crc leaf. The
			// prior message is identified to consumers by (bank, slot, text);
			// an in-place rewrite still reports because the CURRENT crc moved.
			PriorMemoryType:  memoryTypeFor(v.FromMemoryType),
			PriorSlotNumber:  v.FromSlot,
			PriorMultiString: v.FromText,
		}
		// activation-trigger is optional: a trigger with no upstream identity
		// (unknown, reset, vendor-specific) leaves the leaf unset rather than
		// declining the event or asserting a near-neighbour cause.
		if trig, ok := activationTriggerIdentity(v.Trigger); ok {
			mc.ActivationTrigger = trig
		}
		msg = mc

	case model.DMSSignStatusReport:
		// display-state and its brightness sibling live on the dms-status
		// facet; this report is built from the environment facet alone, so
		// display-state stays unset (optional upstream) rather than being
		// fetched across facets the differ cannot see.
		//
		// Per-sensor absence: a sensor the device did not answer for stays at
		// the proto zero value. For the decimal64 temps that is honestly
		// absent (empty string); for the numeric and boolean leaves proto3
		// cannot distinguish a reported zero/false from an absent one — a
		// wire limitation to keep in mind, not something the emitter can fix.
		r := &dmsv1.SignStatusReport{
			Kind:           dmsTypes + "dms-sign-status-report",
			SourceDeviceId: v.DeviceID,
			OccurredAt:     timestamppb.New(v.OccurredAt.UTC()),
			ObservedBy:     e.collectorID,
			Sequence:       e.nextSequence(v.DeviceID),
		}
		if v.BrightnessReported {
			r.BrightnessCurrent = uint32(v.BrightnessPercent)
		}
		if v.AmbientLightReported {
			r.AmbientLightLevel = uint32(v.AmbientLightPercent)
		}
		if v.IlluminanceReported {
			r.AmbientIlluminanceLux = v.IlluminanceLux
		}
		if v.CabinetTempReported {
			r.CabinetTemperatureC = deciCelsius(v.CabinetTempDeciC)
		}
		if v.FaceTempReported {
			r.SignFaceTemperatureC = deciCelsius(v.FaceTempDeciC)
		}
		if v.HumidityReported {
			r.HumidityPercent = uint32(v.HumidityPercent)
		}
		if v.DoorReported {
			r.DoorOpen = v.DoorOpen
		}
		if v.FanReported {
			r.FanActive = v.FanActive
		}
		if v.HeaterReported {
			r.HeaterActive = v.HeaterActive
		}
		msg = r
	default:
		return nil, false
	}
	return msg, true
}
