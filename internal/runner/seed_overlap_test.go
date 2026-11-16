package runner

import (
	"context"
	"testing"
	"time"

	"github.com/Vikasa2M/vikasa-collector/internal/synth"
	"github.com/Vikasa2M/vikasa-collector/sdk/adapter"
	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

// blockingReader is a non-ACS StateReader. Read signals entry, then waits
// for release, then returns the snapshot currently installed. The wait is
// inside Read, which is after the runner has sampled DiffReady.
type blockingReader struct {
	entered chan struct{}
	release chan struct{}
	snap    *model.Snapshot
}

func (b *blockingReader) Descriptor() adapter.Descriptor {
	return adapter.Descriptor{Vendor: "test", DeviceKind: "asc", Caps: adapter.CapState}
}

func (b *blockingReader) Close() error { return nil }

func (b *blockingReader) Read(ctx context.Context) (*model.Snapshot, error) {
	select {
	case b.entered <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	select {
	case <-b.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return b.snap, nil
}

func faultSnap(id string, at time.Time) *model.Snapshot {
	return &model.Snapshot{
		DeviceID: "asc-1", DeviceKind: "asc", SampledAt: at,
		Facets: []model.Facet{model.FaultSet{Faults: []model.Fault{{
			ID: id, Severity: model.SeverityMajor, Category: model.CategoryCabinet,
		}}}},
	}
}

// TestPollThatStartsBeforeSeedIsNotApplied blocks inside Read after the
// runner has sampled an unseeded engine. Seed then returns. The blocked
// snapshot is distinguishable from the next one, and applying it would
// record the wrong fault id through the change hook durable uses.
func TestPollThatStartsBeforeSeedIsNotApplied(t *testing.T) {
	at := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	reader := &blockingReader{
		entered: make(chan struct{}),
		release: make(chan struct{}),
		snap:    faultSnap("from-overlap", at),
	}
	var hooked []string
	eng := synth.NewEngine(synth.NewFaultDiffer()).HoldUntilSeeded().WithChangeHook(
		func(_ string, k model.Kind, f model.Facet) {
			if k != model.KindFaultSet {
				t.Errorf("change hook kind %s", k)
				return
			}
			for _, fault := range f.(model.FaultSet).Faults {
				hooked = append(hooked, fault.ID)
			}
		},
	)
	var events []model.Event
	r := New(reader, Config{
		DeviceID: "asc-1", Interval: 5 * time.Second, Jitter: noJitter,
		Engine: eng, Out: func(evs []model.Event) { events = append(events, evs...) },
	})

	done := make(chan struct{})
	go func() {
		r.pollOnce(context.Background())
		close(done)
	}()
	select {
	case <-reader.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("read did not start")
	}

	eng.Seed("asc-1", []model.Facet{model.FaultSet{Faults: []model.Fault{{
		ID: "restored-door", Severity: model.SeverityMajor, Category: model.CategoryCabinet,
	}}}})
	reader.release <- struct{}{}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("overlapping poll did not return")
	}
	if len(hooked) != 0 || len(events) != 0 {
		t.Fatalf("overlapping snapshot was applied: hook %v events %v", hooked, events)
	}

	reader.snap = faultSnap("from-next", at.Add(time.Second))
	done = make(chan struct{})
	go func() {
		r.pollOnce(context.Background())
		close(done)
	}()
	select {
	case <-reader.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("post-seed read did not start")
	}
	reader.release <- struct{}{}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("post-seed poll did not return")
	}
	if len(hooked) != 1 || hooked[0] != "from-next" {
		t.Fatalf("change hook = %v, want [from-next]", hooked)
	}
	if ids := eventFaultIDs(events); len(ids) != 2 || ids[0] != "from-next" || ids[1] != "restored-door" {
		t.Fatalf("events = %v, want raise from-next then clear restored-door", ids)
	}
}

// TestEmptySeedReleasesHoldWithoutInventingAWatchdogStall seeds nothing.
// The following observation is applied, and the fault differ does not
// create a plc/not-scanning fault the reader did not report.
func TestEmptySeedReleasesHoldWithoutInventingAWatchdogStall(t *testing.T) {
	at := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	reader := &blockingReader{
		entered: make(chan struct{}),
		release: make(chan struct{}),
		snap:    faultSnap("door", at),
	}
	eng := synth.NewEngine(synth.NewFaultDiffer()).HoldUntilSeeded()
	eng.Seed("asc-1", nil)
	if !eng.DiffReady("asc-1") {
		t.Fatal("empty seed left the device held")
	}
	var events []model.Event
	r := New(reader, Config{
		DeviceID: "asc-1", Interval: 5 * time.Second, Jitter: noJitter,
		Engine: eng, Out: func(evs []model.Event) { events = append(events, evs...) },
	})
	done := make(chan struct{})
	go func() {
		r.pollOnce(context.Background())
		close(done)
	}()
	select {
	case <-reader.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("read did not start")
	}
	reader.release <- struct{}{}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("poll did not return")
	}
	ids := eventFaultIDs(events)
	if len(ids) != 1 || ids[0] != "door" {
		t.Fatalf("events = %v, want one door raise and no plc/not-scanning", ids)
	}
}

func eventFaultIDs(evs []model.Event) []string {
	out := make([]string, 0, len(evs))
	for _, ev := range evs {
		switch e := ev.(type) {
		case model.FaultRaised:
			out = append(out, e.FaultID)
		case model.FaultCleared:
			out = append(out, e.FaultID)
		default:
			out = append(out, ev.EventKind())
		}
	}
	return out
}
