package app

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/Vikasa2M/vikasa-collector/internal/config"
	"github.com/Vikasa2M/vikasa-collector/sdk/adapter"
)

// ADR 0019 clause 5: a stable per-collector offset — the same every boot,
// different across collectors, always inside the interval.
func TestPhaseIsStableAndSpread(t *testing.T) {
	const iv = 300 * time.Second
	a1, a2 := phaseFor("cab-001-collector", iv), phaseFor("cab-001-collector", iv)
	b := phaseFor("cab-002-collector", iv)
	if a1 != a2 {
		t.Fatalf("phase not stable: %v vs %v", a1, a2)
	}
	if a1 == b {
		t.Fatalf("two collectors share phase %v; the fleet would not spread", a1)
	}
	seen := map[time.Duration]bool{}
	for i := range 1000 {
		p := phaseFor("cab-"+string(rune('a'+i%26))+time.Duration(i).String(), iv)
		if p < 0 || p >= iv {
			t.Fatalf("phase %v outside [0, %v)", p, iv)
		}
		seen[p.Truncate(30*time.Second)] = true
	}
	if len(seen) < 10 {
		t.Fatalf("1000 collectors fell into only %d of 10 thirty-second buckets", len(seen))
	}
}

// Beats land on wall-clock slots ≡ phase (mod interval): regular, and the
// same slots across a restart, so a hub's dead-man timer can predict them.
func TestNextBeatIsTheNextAlignedSlot(t *testing.T) {
	const iv = 300 * time.Second
	phase := 42 * time.Second
	epoch := time.Unix(0, 0).UTC()
	for _, tc := range []struct{ now, want time.Time }{
		{epoch, epoch.Add(42 * time.Second)},
		{epoch.Add(42 * time.Second), epoch.Add(342 * time.Second)}, // exactly on a slot: the next one
		{epoch.Add(100 * time.Second), epoch.Add(342 * time.Second)},
		{epoch.Add(3*time.Hour + 41*time.Second), epoch.Add(3*time.Hour + 42*time.Second)},
	} {
		if got := nextBeat(tc.now, iv, phase); !got.Equal(tc.want) {
			t.Errorf("nextBeat(%v) = %v, want %v", tc.now.Sub(epoch), got.Sub(epoch), tc.want.Sub(epoch))
		}
	}
}

// End to end (ADR 0019): started at boot, heartbeats on the instance root
// addressed by site, and stopping on a clean shutdown — through the real Run.
func TestInstanceEventsReachTheBus(t *testing.T) {
	ns := startNATS(t)
	nc, err := nats.Connect(ns.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	var mu sync.Mutex
	subjects := map[string]string{} // ce-type -> subject
	if _, err := nc.Subscribe("openits-collector.>", func(m *nats.Msg) {
		mu.Lock()
		subjects[m.Header.Get("ce-type")] = m.Subject
		mu.Unlock()
	}); err != nil {
		t.Fatal(err)
	}
	seen := func(ceType string) string { mu.Lock(); defer mu.Unlock(); return subjects[ceType] }

	reg := adapter.NewRegistry()
	dev := &recordingASC{}
	reg.Register(dev.Descriptor(), func(string, map[string]any) (adapter.Adapter, error) { return dev, nil })
	cfg := &config.Config{
		Region: "us-ga", Agency: "metro", AgencyUnit: "d01", Site: "cab-1",
		CollectorID: "metro-cab-1-collector", ModelVersion: "openits/v1", HeartbeatInterval: 100 * time.Millisecond,
		Devices: []config.Device{{ID: "asc-1", Vendor: "fixture", DeviceKind: "asc", PollInterval: time.Hour}},
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, cfg, reg, Options{NATSURL: ns.ClientURL(), Version: "test"}) }()

	eventually(t, "a heartbeat", func() bool { return seen("openits-collector.instance.heartbeat.v1") != "" })
	if got := seen("openits-collector.instance.heartbeat.v1"); got != "openits-collector.us-ga.metro.d01.instance.cab-1.heartbeat" {
		t.Fatalf("heartbeat subject = %q", got)
	}
	if seen("openits-collector.instance.started.v1") == "" {
		t.Fatal("no started event")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := nc.Flush(); err != nil {
		t.Fatal(err)
	}
	eventually(t, "stopping on clean shutdown", func() bool { return seen("openits-collector.instance.stopping.v1") != "" })
}
