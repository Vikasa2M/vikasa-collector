package app

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"

	"github.com/Vikasa2M/vikasa-collector/internal/config"
	"github.com/Vikasa2M/vikasa-collector/sdk/adapter"
	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

// recordingASC records whether it was ever read or closed, so a test can
// see what Run left behind after it returned.
type recordingASC struct {
	mu     sync.Mutex
	reads  int
	closed bool
}

func (r *recordingASC) Descriptor() adapter.Descriptor {
	return adapter.Descriptor{Vendor: "fixture", DeviceKind: "asc", Caps: adapter.CapState}
}

func (r *recordingASC) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = true
	return nil
}

func (r *recordingASC) Read(context.Context) (*model.Snapshot, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reads++
	return &model.Snapshot{DeviceID: "asc-1", SampledAt: time.Now().UTC()}, nil
}

func startNATS(t *testing.T) *server.Server {
	t.Helper()
	ns, err := server.NewServer(&server.Options{Port: -1, JetStream: true, StoreDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	ns.Start()
	t.Cleanup(ns.Shutdown)
	if !ns.ReadyForConnections(5 * time.Second) {
		t.Fatal("nats not ready")
	}
	return ns
}

// A device whose adapter cannot be built is a boot failure, and a boot
// failure must leave nothing running: before this test, devices configured
// ahead of the broken one were already polling when Run returned, nothing
// stopped or closed them, and the collector had already announced
// collector-started for a boot that then failed.
func TestRunBootFailureLeavesNothingRunning(t *testing.T) {
	ns := startNATS(t)

	good := &recordingASC{}
	reg := adapter.NewRegistry()
	reg.Register(adapter.Descriptor{Vendor: "fixture", DeviceKind: "asc", Caps: adapter.CapState},
		func(string, map[string]any) (adapter.Adapter, error) { return good, nil })
	reg.Register(adapter.Descriptor{Vendor: "broken", DeviceKind: "asc", Caps: adapter.CapState},
		func(string, map[string]any) (adapter.Adapter, error) { return nil, errors.New("dial refused") })

	cfg := &config.Config{
		Region: "us-ga", Agency: "metro", AgencyUnit: "d01", Site: "cab-1",
		CollectorID: "metro-cab-1-collector", ModelVersion: "openits/v1",
		Devices: []config.Device{
			{ID: "asc-1", Vendor: "fixture", DeviceKind: "asc", PollInterval: time.Millisecond},
			{ID: "asc-2", Vendor: "broken", DeviceKind: "asc", PollInterval: time.Millisecond},
		},
	}

	nc, err := nats.Connect(ns.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	published := make(chan string, 16)
	if _, err := nc.Subscribe(">", func(m *nats.Msg) {
		if ceType := m.Header.Get("ce-type"); ceType != "" {
			published <- ceType
		}
	}); err != nil {
		t.Fatal(err)
	}
	if err := nc.Flush(); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := Run(ctx, cfg, reg, Options{NATSURL: ns.ClientURL(), Version: "test"}); err == nil {
		t.Fatal("Run must fail when a device's adapter cannot be built")
	}

	good.mu.Lock()
	closed := good.closed
	good.mu.Unlock()
	if !closed {
		t.Error("adapter built before the failing one was never closed")
	}

	// A runner left behind would keep reading after Run returned. Give one
	// the chance to show itself: many poll intervals, not a race window.
	time.Sleep(50 * time.Millisecond)
	good.mu.Lock()
	reads := good.reads
	good.mu.Unlock()
	if reads != 0 {
		t.Errorf("adapter was read %d times; no device may poll on a failed boot", reads)
	}

	if err := nc.Flush(); err != nil {
		t.Fatal(err)
	}
	select {
	case ceType := <-published:
		t.Errorf("published %s during a boot that failed", ceType)
	default:
	}
}

// stuckCloser polls fine but never returns from Close.
type stuckCloser struct{ recordingASC }

func (s *stuckCloser) Close() error { select {} }

// Shutdown is bounded even when an adapter's Close never returns: the
// process must exit for its supervisor, not wait on a third-party bug.
func TestRunShutdownIsBoundedWhenAnAdapterCloseHangs(t *testing.T) {
	ns := startNATS(t)
	old := closeTimeout
	closeTimeout = 50 * time.Millisecond
	t.Cleanup(func() { closeTimeout = old })

	reg := adapter.NewRegistry()
	reg.Register(adapter.Descriptor{Vendor: "fixture", DeviceKind: "asc", Caps: adapter.CapState},
		func(string, map[string]any) (adapter.Adapter, error) { return &stuckCloser{}, nil })
	cfg := &config.Config{
		Region: "us-ga", Agency: "metro", AgencyUnit: "d01", Site: "cab-1",
		CollectorID: "metro-cab-1-collector", ModelVersion: "openits/v1",
		Devices: []config.Device{{ID: "asc-1", Vendor: "fixture", DeviceKind: "asc", PollInterval: time.Hour}},
	}

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, cfg, reg, Options{NATSURL: ns.ClientURL(), Version: "test"}) }()
	time.Sleep(200 * time.Millisecond) // let it boot; nothing here signals readiness yet
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return: shutdown waited on an adapter Close that never returns")
	}
}
