package tier2_test

import (
	"context"
	"errors"
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
	cfg := loadWatchdogConfig(t, firstPLC)
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

	secondPLC := cameleon.NewScriptedPLC(4, 5*time.Second, start.Add(5*time.Second))
	appCtx, cancelRestart := context.WithCancel(context.Background())
	defer cancelRestart()
	go func() {
		runErr <- app.Run(appCtx, cfg, registryFor(secondPLC), app.Options{NATSURL: ns.ClientURL(), Version: "test"})
	}()
	waitPLCPolls(t, secondPLC, 1)
	quietUntil := time.Now().Add(400 * time.Millisecond)
	for time.Now().Before(quietUntil) {
		if msgs := streamMsgs(t, st); msgs != 1 {
			t.Fatalf("restart stored %d catalog messages, want 1; a standing watchdog fault must not clear or raise again", msgs)
		}
		time.Sleep(20 * time.Millisecond)
	}
	expectNone(t, cons)

	secondPLC.SetClock(start.Add(6 * time.Second))
	secondPLC.SetCounter(5)
	cleared := takeLogical(t, cons)
	if cleared.ceType != "openits.reversible-lane.fault-cleared.v1" {
		t.Fatalf("ce-type = %q", cleared.ceType)
	}
	fc := cleared.data.(*commonv1.FaultCleared)
	if fc.GetFaultId() != "plc/not-scanning" ||
		fc.GetKind() != "openits-reversible-lane-types:reversible-lane-fault-controller" {
		t.Fatalf("cleared = id %q kind %q", fc.GetFaultId(), fc.GetKind())
	}
	if fc.GetFirstObserved() == nil || !fc.GetFirstObserved().AsTime().Equal(original) {
		t.Fatalf("cleared first_observed = %v, want %v", fc.GetFirstObserved().AsTime(), original)
	}
	if msgs := streamMsgs(t, st); msgs != 2 {
		t.Fatalf("recovery stored %d catalog messages, want 2", msgs)
	}

	cancelRestart()
	if err := <-runErr; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("second run: %v", err)
	}
}

func registryFor(plc *cameleon.ScriptedPLC) *adapter.Registry {
	reg := adapter.NewRegistry()
	plc.Register(reg)
	return reg
}

func loadWatchdogConfig(t *testing.T, plc *cameleon.ScriptedPLC) *config.Config {
	t.Helper()
	const cfgYAML = `
collector_id: scripted-acs-collector
region: us-ga
agency: metro
agency_unit: d01
site: cab-1
model_version: openits/v1
devices:
  - { id: acs-1, vendor: cameleon, device_kind: acs, poll_interval: 20ms, connection: {} }
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
