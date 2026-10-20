package openits

import (
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	tsv1 "github.com/Vikasa2M/openits-models/pkg/proto/openits/traffic_sensor/v1"

	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

// encodeTrafficSensor builds the payload for a traffic-sensor event.
func (e *emitter) encodeTrafficSensor(ev model.Event) (proto.Message, bool) {
	var msg proto.Message
	switch v := ev.(type) {
	case model.TrafficIntervalReport:
		lanes := make([]*tsv1.TrafficIntervalReportLane, 0, len(v.Lanes))
		for _, l := range v.Lanes {
			classes := make([]*tsv1.TrafficIntervalReportLaneClassVolume, 0, len(l.ClassVolumes))
			for _, cv := range l.ClassVolumes {
				classes = append(classes, &tsv1.TrafficIntervalReportLaneClassVolume{
					ClassId: proto.Uint32(cv.ClassID), Volume: proto.Uint32(cv.Volume),
				})
			}
			lane := &tsv1.TrafficIntervalReportLane{
				LaneId:      proto.Uint32(l.LaneID),
				Volume:      proto.Uint32(l.Volume),
				Occupancy:   proto.String(occupancyPercent(l.OccupancyTenths)),
				ClassVolume: classes,
				DataQuality: dataQualityFor(l.Quality),
				// The interval belongs to the DEVICE, and the wire repeats it
				// per lane. Both come from the report, not from poll timing.
				IntervalStart:     timestamppb.New(v.IntervalStart.UTC()),
				IntervalDurationS: proto.Uint32(wholeSeconds(v.IntervalDuration)),
			}
			// Left EMPTY when the sensor did not report speed. Rendering
			// "0.00" would turn "we don't know" into "stopped traffic", and a
			// consumer computing travel time cannot tell those apart after
			// the fact.
			if l.SpeedReported {
				lane.SpeedAverageKmh = proto.String(hundredths(l.SpeedAvgHundredthsKPH))
			}
			lanes = append(lanes, lane)
		}
		msg = &tsv1.TrafficIntervalReport{
			Kind:           trafficSenTypes + "ts-traffic-interval-report",
			SourceDeviceId: v.DeviceID,
			OccurredAt:     timestamppb.New(v.OccurredAt.UTC()),
			ObservedBy:     proto.String(e.collectorID),
			SequenceEpoch:  e.epoch,
			Sequence:       e.nextSequence(v.DeviceID),
			Lane:           lanes,
		}
	default:
		return nil, false
	}
	return msg, true
}
