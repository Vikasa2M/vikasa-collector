package tier2_test

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	commonv1 "github.com/Vikasa2M/openits-models/pkg/proto/openits/common/v1"

	"github.com/Vikasa2M/vikasa-collector/internal/app"
	"github.com/Vikasa2M/vikasa-collector/internal/config"
	"github.com/Vikasa2M/vikasa-collector/internal/publish"
	"github.com/Vikasa2M/vikasa-collector/internal/vendors/cameleon"
	"github.com/Vikasa2M/vikasa-collector/sdk/adapter"
)

// TestACSWatchdogRestartKeepsTheStandingStall runs app.Run twice against
// one broker. The second process builds a new ACS adapter. The test does
// not call RestoreFaults; the seed hook in app.Run does.
func TestACSWatchdogRestartKeepsTheStandingStall(t *testing.T) {
	ns, err := server.NewServer(&server.Options{Host: "127.0.0.1", Port: -1, JetStream: true, StoreDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	ns.Start()
	t.Cleanup(ns.Shutdown)
	if !ns.ReadyForConnections(5 * time.Second) {
		t.Fatal("nats not ready")
	}

	start := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	firstPLC := cameleon.NewScriptedPLC(4, 5*time.Second, start)
	cfg := loadWatchdogConfig(t, firstPLC, "20ms")
	streamName := publish.StreamNameForBinding("openits.us-ga.metro.d01.>")

	nc, err := nats.Connect(ns.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { nc.Close() })
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatal(err)
	}

	appCtx, cancel := context.WithCancel(context.Background())
	runErr := make(chan error, 1)
	go func() {
		runErr <- app.Run(appCtx, cfg, registryFor(firstPLC), app.Options{NATSURL: ns.ClientURL(), Version: "test"})
	}()

	st := waitStream(t, js, streamName)
	waitPLCPolls(t, firstPLC, 1)
	if msgs := streamMsgs(t, st); msgs != 0 {
		t.Fatalf("baseline stored %d catalog messages, want 0", msgs)
	}
	firstPLC.SetClock(start.Add(5 * time.Second))
	waitCatalog(t, st, 1)

	cons, err := st.CreateOrUpdateConsumer(t.Context(), jetstream.ConsumerConfig{
		Durable:       "acs-watchdog-restart",
		AckPolicy:     jetstream.AckExplicitPolicy,
		DeliverPolicy: jetstream.DeliverAllPolicy,
	})
	if err != nil {
		t.Fatal(err)
	}
	raised := takeLogical(t, cons)
	if raised.ceType != "openits.reversible-lane.fault-raised.v1" {
		t.Fatalf("ce-type = %q", raised.ceType)
	}
	fr := raised.data.(*commonv1.FaultRaised)
	if fr.GetFaultId() != "plc/not-scanning" ||
		fr.GetKind() != "openits-reversible-lane-types:reversible-lane-fault-controller" ||
		fr.GetFirstObserved() == nil {
		t.Fatalf("raised = id %q kind %q first %v", fr.GetFaultId(), fr.GetKind(), fr.GetFirstObserved())
	}
	original := fr.GetFirstObserved().AsTime()
	expectNone(t, cons)
	if msgs := streamMsgs(t, st); msgs != 1 {
		t.Fatalf("standing stall stored %d catalog messages, want 1", msgs)
	}

	cancel()
	if err := <-runErr; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("first run: %v", err)
	}

	// T1 is the post-seed poll with the counter still at 4. T2 is the later
	// poll that observes the counter advance. ce-time distinguishes them.
	t1 := start.Add(5 * time.Second)
	t2 := start.Add(6 * time.Second)
	secondPLC := cameleon.NewScriptedPLC(4, 5*time.Second, t1)
	secondPLC.HoldReads()
	secondCfg := loadWatchdogConfig(t, secondPLC, "5s")
	seeded := make(chan struct{}, 1)
	log := slog.New(&seedLoadedLog{ch: seeded})
	appCtx, cancelRestart := context.WithCancel(context.Background())
	defer cancelRestart()
	go func() {
		runErr <- app.Run(appCtx, secondCfg, registryFor(secondPLC),
			app.Options{NATSURL: ns.ClientURL(), Version: "test", Logger: log})
	}()
	select {
	case <-seeded:
	case <-time.After(12 * time.Second):
		t.Fatal("durable load did not finish")
	}
	if !secondPLC.WatchdogRestored() {
		t.Fatal("seed returned without restoring plc/not-scanning to the new adapter")
	}
	// One read may already be inside Read with a pre-seed DiffReady sample.
	// Releasing it, then waiting for the next entry, orders the proof read
	// after that pollOnce returns and therefore after seed.
	waitRead(t, secondPLC)
	secondPLC.ReleaseRead()
	waitRead(t, secondPLC)
	secondPLC.SetGateWord(0x0031) // automatic, opening; the seeded word is 0
	secondPLC.ReleaseRead()
	waitRead(t, secondPLC) // proof pollOnce has returned; this read has not copied the image
	secondPLC.SetClock(t2)
	secondPLC.SetCounter(5)
	secondPLC.ReleaseRead()

	var after []storedEvent
	deadline := time.Now().Add(12 * time.Second)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			t.Fatal("post-seed polls did not produce the counter-advance clear")
		}
		ev := fetchStored(t, cons, remaining)
		after = append(after, ev)
		if ev.ceType == "openits.reversible-lane.fault-cleared.v1" {
			break
		}
	}
	var gateAt time.Time
	var gateN, clearN, raiseN int
	var fc *commonv1.FaultCleared
	for _, ev := range after {
		switch ev.ceType {
		case "openits.reversible-lane.gate-status-changed.v1":
			gateN++
			gateAt = ev.ceTime
		case "openits.reversible-lane.fault-cleared.v1":
			clearN++
			fc = ev.data.(*commonv1.FaultCleared)
			if !ev.ceTime.Equal(t2) {
				t.Fatalf("clear ce-time = %s, want the advance poll %s", ev.ceTime, t2)
			}
		case "openits.reversible-lane.fault-raised.v1":
			raiseN++
		default:
			t.Fatalf("unexpected catalog event %s", ev.ceType)
		}
	}
	if gateN != 1 || !gateAt.Equal(t1) {
		t.Fatalf("gate events = %d at %s, want one applied poll at %s", gateN, gateAt, t1)
	}
	if raiseN != 0 || clearN != 1 {
		t.Fatalf("after restart raises=%d clears=%d, want no raise and one advance clear", raiseN, clearN)
	}
	if fc.GetFaultId() != "plc/not-scanning" ||
		fc.GetKind() != "openits-reversible-lane-types:reversible-lane-fault-controller" {
		t.Fatalf("cleared = id %q kind %q", fc.GetFaultId(), fc.GetKind())
	}
	if fc.GetFirstObserved() == nil || !fc.GetFirstObserved().AsTime().Equal(original) {
		t.Fatalf("cleared first_observed = %v, want %v", fc.GetFirstObserved().AsTime(), original)
	}

	cancelRestart()
	if err := <-runErr; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("second run: %v", err)
	}
}

// seedLoadedLog reports the durable package's existing "synth state loaded"
// line. That line is written after Seed returns, so the engine mutex has
// been released and the device is marked ready. Enabled stays true so the
// wrapper still delivers the record; the record itself is not printed.
type seedLoadedLog struct {
	ch chan struct{}
}

func (s *seedLoadedLog) Enabled(context.Context, slog.Level) bool { return true }

func (s *seedLoadedLog) Handle(_ context.Context, r slog.Record) error {
	if r.Message == "synth state loaded" {
		select {
		case s.ch <- struct{}{}:
		default:
		}
	}
	return nil
}

func (s *seedLoadedLog) WithAttrs([]slog.Attr) slog.Handler { return s }

func (s *seedLoadedLog) WithGroup(string) slog.Handler { return s }

func registryFor(plc *cameleon.ScriptedPLC) *adapter.Registry {
	reg := adapter.NewRegistry()
	plc.Register(reg)
	return reg
}

func loadWatchdogConfig(t *testing.T, plc *cameleon.ScriptedPLC, interval string) *config.Config {
	t.Helper()
	cfgYAML := `
collector_id: scripted-acs-collector
region: us-ga
agency: metro
agency_unit: d01
site: cab-1
model_version: openits/v1
devices:
  - { id: acs-1, vendor: cameleon, device_kind: acs, poll_interval: ` + interval + `, connection: {} }
`
	cfgPath := filepath.Join(t.TempDir(), "collector.yaml")
	if err := os.WriteFile(cfgPath, []byte(cfgYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(cfgPath, registryFor(plc))
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func waitRead(t *testing.T, plc *cameleon.ScriptedPLC) {
	t.Helper()
	select {
	case <-plc.ReadEntered():
	case <-time.After(12 * time.Second):
		t.Fatal("held read did not start")
	}
}

func fetchStored(t *testing.T, cons jetstream.Consumer, wait time.Duration) storedEvent {
	t.Helper()
	msg, err := cons.Next(jetstream.FetchMaxWait(wait))
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	ev := decodeStored(t, msg)
	if err := msg.Ack(); err != nil {
		t.Fatal(err)
	}
	return ev
}

func waitPLCPolls(t *testing.T, plc *cameleon.ScriptedPLC, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if plc.Polls() >= n {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("polls = %d, want >= %d", plc.Polls(), n)
}

func waitCatalog(t *testing.T, st jetstream.Stream, n uint64) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if streamMsgs(t, st) >= n {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("catalog messages = %d, want >= %d", streamMsgs(t, st), n)
}
