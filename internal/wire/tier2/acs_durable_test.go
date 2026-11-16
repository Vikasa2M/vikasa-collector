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
	"google.golang.org/protobuf/proto"

	commonv1 "github.com/Vikasa2M/openits-models/pkg/proto/openits/common/v1"
	rlv1 "github.com/Vikasa2M/openits-models/pkg/proto/openits/reversible_lane/v1"
	conformance "github.com/Vikasa2M/openits-models/tools/conformance/tests"

	"github.com/Vikasa2M/vikasa-collector/internal/app"
	"github.com/Vikasa2M/vikasa-collector/internal/config"
	"github.com/Vikasa2M/vikasa-collector/internal/publish"
	"github.com/Vikasa2M/vikasa-collector/internal/vendors/cameleon"
	"github.com/Vikasa2M/vikasa-collector/sdk/adapter"
	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

// Durable-consumer checks that judge the stored reversible-lane payloads.
// TestYANG_Validate and TestReversibleLaneEvent_GateStatusChangedMirrorsLiveState
// need a YANG device tree this test does not publish, so they are not run.
// TestMode_ValueMatchesKind only inspects mode-changed subjects; gate
// operating mode is asserted on the decoded payload below, and the mode
// matrix stays in TestGateOperatingModeAndInterlockBypass.
var acsHarnessChecks = []string{
	"TestSubject_SevenTokenShape",
	"TestSubject_OpenitsPrefix",
	"TestCEType_OpenitsFormat",
	"TestCESource_URN",
	"TestCEID_Present",
	"TestCEDataSchema_ResolvesToSnapshot",
	"TestCEDataSchema_PayloadMatchesSnapshot",
	"TestEvent_SequenceContiguousWithinEpoch",
	"TestFault_KindMatchesService",
	"TestReversibleLaneEvent_FaultRaisedShape",
	"TestReversibleLaneEvent_GateFaultRaisedNamesGate",
}

// TestACSDurableGateDelivery runs the ACS adapter, differ, emitter and
// publisher against scripted words and an isolated JetStream broker.
// It fetches from a named durable consumer, decodes the stored body, and
// acks. A Nak redelivery of the same ce-id is not a second logical event.
//
// In-process cases that already exist and are not repeated here:
// TestGateFirstPollEmitsNothing, TestGateNoChangeEmitsNothing,
// TestGateFailedReadEmitsNothingAndRecoveryDiffsAgainstPreFailure,
// TestGateModePairTransitions, TestGateOperatingModeAndInterlockBypass,
// TestInterlockBypassedFalseWhenLeavingMode3ForAutomatic,
// TestCommandRefusedAlarm_DecodeDifferEmit,
// TestFaultFirstObservedIsStampedCarriedAndCleared,
// TestRestartDoesNotReRaiseAStandingFault.
// TestACSEventsReachJetStream only watches an ephemeral core subscription.
func TestACSDurableGateDelivery(t *testing.T) {
	const (
		wordClosed  = 0x0021 // automatic, closed
		wordOpening = 0x0031 // automatic, opening
		wordRefused = 0x2031 // opening plus command-refused
		wordBypass  = 0x0033 // manual non-interlocked, opening
	)
	ns, err := server.NewServer(&server.Options{Host: "127.0.0.1", Port: -1, JetStream: true, StoreDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	ns.Start()
	t.Cleanup(ns.Shutdown)
	if !ns.ReadyForConnections(5 * time.Second) {
		t.Fatal("nats not ready")
	}

	bank := cameleon.NewScriptedGateBank(101, []uint16{wordClosed}, []cameleon.GateWord{{
		ID: "G-1", Kind: model.GateKindWarning, DSW: 101,
	}})
	reg := adapter.NewRegistry()
	bank.Register(reg)

	cfgYAML := `
collector_id: scripted-acs-collector
region: us-ga
agency: metro
agency_unit: d01
site: cab-1
model_version: openits/v1
devices:
  - { id: scripted-acs, vendor: cameleon, device_kind: acs, poll_interval: 20ms, connection: {} }
`
	cfgPath := filepath.Join(t.TempDir(), "collector.yaml")
	if err := os.WriteFile(cfgPath, []byte(cfgYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(cfgPath, reg)
	if err != nil {
		t.Fatal(err)
	}

	nc, err := nats.Connect(ns.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { nc.Close() })
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatal(err)
	}
	streamName := publish.StreamNameForBinding("openits.us-ga.metro.d01.>")
	appCtx, cancel := context.WithCancel(context.Background())
	runErr := make(chan error, 1)
	go func() { runErr <- app.Run(appCtx, cfg, reg, app.Options{NATSURL: ns.ClientURL(), Version: "test"}) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-runErr:
		case <-time.After(5 * time.Second):
		}
	})

	st := waitStream(t, js, streamName)
	waitPolls(t, bank, 3)
	if msgs := streamMsgs(t, st); msgs != 0 {
		t.Fatalf("catalog stream has %d messages after the initial snapshot; a first gate observation must not emit gate-status-changed", msgs)
	}
	cons, err := st.CreateOrUpdateConsumer(t.Context(), jetstream.ConsumerConfig{
		Durable:       "acs-gate-delivery",
		AckPolicy:     jetstream.AckExplicitPolicy,
		DeliverPolicy: jetstream.DeliverAllPolicy,
	})
	if err != nil {
		t.Fatal(err)
	}

	bank.SetWord(0, wordOpening)
	opening := takeLogical(t, cons)
	if opening.ceType != "openits.reversible-lane.gate-status-changed.v1" {
		t.Fatalf("ce-type = %q", opening.ceType)
	}
	gs := opening.data.(*rlv1.GateStatusChanged)
	if gs.GetGateId() != "G-1" || gs.GetPosition() != rlv1.GatePosition_GATE_POSITION_OPENING ||
		gs.GetPreviousPosition() != rlv1.GatePosition_GATE_POSITION_CLOSED {
		t.Fatalf("gate status = id %q pos %v prev %v", gs.GetGateId(), gs.GetPosition(), gs.GetPreviousPosition())
	}
	if gs.GetOperatingMode() != "openits-reversible-lane-types:reversible-lane-gate-mode-automatic" || gs.GetInterlockBypassed() {
		t.Fatalf("mode = %q bypassed %v", gs.GetOperatingMode(), gs.GetInterlockBypassed())
	}
	waitPolls(t, bank, bank.Polls()+2)
	expectNone(t, cons)
	if msgs := streamMsgs(t, st); msgs != 1 {
		t.Fatalf("stable snapshots stored %d messages, want 1", msgs)
	}

	bank.SetWord(0, wordRefused)
	raised := takeLogical(t, cons)
	if raised.ceType != "openits.reversible-lane.fault-raised.v1" {
		t.Fatalf("ce-type = %q", raised.ceType)
	}
	fr := raised.data.(*commonv1.FaultRaised)
	const (
		refusedKind = "openits-vendor-cameleon-reversible-lane-types:cameleon-gate-command-refused"
		gateClass   = "openits-reversible-lane-types:rl-component-gate"
	)
	if fr.GetKind() != refusedKind || fr.GetFaultId() != "gate/G-1/command-refused" {
		t.Fatalf("raised kind %q id %q", fr.GetKind(), fr.GetFaultId())
	}
	if fr.GetComponent().GetComponentClass() != gateClass || fr.GetComponent().GetComponentId() != "G-1" {
		t.Fatalf("raised component = %+v", fr.GetComponent())
	}
	if fr.GetFirstObserved() == nil {
		t.Fatal("raised first_observed is empty")
	}
	first := fr.GetFirstObserved().AsTime()

	cancel()
	if err := <-runErr; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("first run: %v", err)
	}
	appCtx, cancel = context.WithCancel(context.Background())
	runErr = make(chan error, 1)
	go func() { runErr <- app.Run(appCtx, cfg, reg, app.Options{NATSURL: ns.ClientURL(), Version: "test"}) }()
	pollsAtRestart := bank.Polls()
	waitPolls(t, bank, pollsAtRestart+3)
	expectNone(t, cons)
	if msgs := streamMsgs(t, st); msgs != 2 {
		t.Fatalf("restart stored %d catalog messages, want 2; a standing fault must not be cleared or raised again", msgs)
	}

	bank.SetWord(0, wordOpening)
	cleared := takeLogical(t, cons)
	if cleared.ceType != "openits.reversible-lane.fault-cleared.v1" {
		t.Fatalf("ce-type = %q", cleared.ceType)
	}
	fc := cleared.data.(*commonv1.FaultCleared)
	if fc.GetKind() != refusedKind || fc.GetFaultId() != "gate/G-1/command-refused" {
		t.Fatalf("cleared kind %q id %q", fc.GetKind(), fc.GetFaultId())
	}
	if fc.GetComponent().GetComponentClass() != gateClass || fc.GetComponent().GetComponentId() != "G-1" {
		t.Fatalf("cleared component = %+v", fc.GetComponent())
	}
	if fc.GetFirstObserved() == nil || !fc.GetFirstObserved().AsTime().Equal(first) {
		t.Fatalf("cleared first_observed = %v, want %v", fc.GetFirstObserved().AsTime(), first)
	}

	attempts := bank.Attempts()
	bank.Fail(errors.New("scripted read failed"))
	waitAttempts(t, bank, attempts+1)
	time.Sleep(300 * time.Millisecond)
	expectNone(t, cons)
	if msgs := streamMsgs(t, st); msgs != 3 {
		t.Fatalf("failed reads stored %d catalog messages, want 3", msgs)
	}
	bank.Restore()
	waitPolls(t, bank, bank.Polls()+2)
	expectNone(t, cons)
	if msgs := streamMsgs(t, st); msgs != 3 {
		t.Fatalf("recovery of the same word stored %d catalog messages, want 3", msgs)
	}

	bank.SetWord(0, wordBypass)
	bypass := takeLogical(t, cons)
	bgs := bypass.data.(*rlv1.GateStatusChanged)
	if bgs.GetOperatingMode() != "openits-reversible-lane-types:reversible-lane-gate-mode-manual" || !bgs.GetInterlockBypassed() {
		t.Fatalf("bypass mode = %q bypassed %v", bgs.GetOperatingMode(), bgs.GetInterlockBypassed())
	}
	if bgs.GetPreviousOperatingMode() != "openits-reversible-lane-types:reversible-lane-gate-mode-automatic" {
		t.Fatalf("previous mode = %q", bgs.GetPreviousOperatingMode())
	}

	runHarness(t, []storedEvent{opening, raised, cleared, bypass})
}

type storedEvent struct {
	subject, ceType, ceSource, ceID, schema string
	ceTime                                  time.Time
	data                                    proto.Message
}

func takeLogical(t *testing.T, cons jetstream.Consumer) storedEvent {
	t.Helper()
	first := fetchOne(t, cons)
	id := first.Headers().Get("ce-id")
	if id == "" || first.Headers().Get("Nats-Msg-Id") != id {
		t.Fatalf("ce-id %q Nats-Msg-Id %q", id, first.Headers().Get("Nats-Msg-Id"))
	}
	if err := first.Nak(); err != nil {
		t.Fatal(err)
	}
	again := fetchOne(t, cons)
	if again.Headers().Get("ce-id") != id {
		t.Fatalf("redelivery ce-id = %q, want %q", again.Headers().Get("ce-id"), id)
	}
	if err := again.DoubleAck(t.Context()); err != nil {
		t.Fatal(err)
	}
	info, err := cons.Info(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if info.NumAckPending != 0 {
		t.Fatalf("ack pending = %d after ack", info.NumAckPending)
	}
	ev := decodeStored(t, again)
	if ev.ceID != id {
		t.Fatalf("decoded ce-id = %q, want %q", ev.ceID, id)
	}
	return ev
}

func fetchOne(t *testing.T, cons jetstream.Consumer) jetstream.Msg {
	t.Helper()
	msg, err := cons.Next(jetstream.FetchMaxWait(5 * time.Second))
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	return msg
}

func expectNone(t *testing.T, cons jetstream.Consumer) {
	t.Helper()
	msg, err := cons.Next(jetstream.FetchMaxWait(250 * time.Millisecond))
	if err == nil {
		t.Fatalf("unexpected subject %s ce-type %s ce-id %s", msg.Subject(), msg.Headers().Get("ce-type"), msg.Headers().Get("ce-id"))
	}
}

func decodeStored(t *testing.T, msg jetstream.Msg) storedEvent {
	t.Helper()
	h := msg.Headers()
	ev := storedEvent{
		subject: msg.Subject(), ceType: h.Get("ce-type"), ceSource: h.Get("ce-source"),
		ceID: h.Get("ce-id"), schema: h.Get("ce-dataschema"),
	}
	var err error
	ev.ceTime, err = time.Parse(time.RFC3339Nano, h.Get("ce-time"))
	if err != nil {
		t.Fatalf("ce-time: %v", err)
	}
	switch ev.ceType {
	case "openits.reversible-lane.gate-status-changed.v1":
		m := &rlv1.GateStatusChanged{}
		if err := proto.Unmarshal(msg.Data(), m); err != nil {
			t.Fatal(err)
		}
		ev.data = m
	case "openits.reversible-lane.fault-raised.v1":
		m := &commonv1.FaultRaised{}
		if err := proto.Unmarshal(msg.Data(), m); err != nil {
			t.Fatal(err)
		}
		ev.data = m
	case "openits.reversible-lane.fault-cleared.v1":
		m := &commonv1.FaultCleared{}
		if err := proto.Unmarshal(msg.Data(), m); err != nil {
			t.Fatal(err)
		}
		ev.data = m
	default:
		t.Fatalf("unexpected ce-type %q", ev.ceType)
	}
	if len(msg.Data()) == 0 {
		t.Fatal("stored body is empty")
	}
	return ev
}

func runHarness(t *testing.T, events []storedEvent) {
	t.Helper()
	suite := map[string]func(*conformance.T, *conformance.Observation){}
	for _, tc := range conformance.All("reversible-lane") {
		suite[tc.Name] = tc.Fn
	}
	obs := &conformance.Observation{Window: time.Second, Registry: pinnedRegistry(t)}
	for _, ev := range events {
		obs.Events = append(obs.Events, conformance.EventEnvelope{
			Subject: ev.subject, CEType: ev.ceType, CESource: ev.ceSource,
			CEID: ev.ceID, CETime: ev.ceTime, CEDataSchema: ev.schema, Data: ev.data,
		})
	}
	for _, name := range acsHarnessChecks {
		fn, ok := suite[name]
		if !ok {
			t.Errorf("pinned reversible-lane harness has no %s", name)
			continue
		}
		if r := conformance.Run(name, fn, obs); !r.Pass {
			t.Errorf("%s: %s", name, r.Message)
		}
	}
}

func waitStream(t *testing.T, js jetstream.JetStream, name string) jetstream.Stream {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		st, err := js.Stream(t.Context(), name)
		if err == nil {
			return st
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("stream %s was not provisioned", name)
	return nil
}

func streamMsgs(t *testing.T, st jetstream.Stream) uint64 {
	t.Helper()
	info, err := st.Info(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return info.State.Msgs
}

func waitPolls(t *testing.T, bank *cameleon.ScriptedGateBank, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if bank.Polls() >= n {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("polls = %d, want >= %d", bank.Polls(), n)
}

func waitAttempts(t *testing.T, bank *cameleon.ScriptedGateBank, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if bank.Attempts() >= n {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("attempts = %d, want >= %d", bank.Attempts(), n)
}
