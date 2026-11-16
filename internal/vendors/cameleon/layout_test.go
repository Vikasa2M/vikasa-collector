package cameleon

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Vikasa2M/vikasa-collector/internal/config"
	"github.com/Vikasa2M/vikasa-collector/internal/synth"
	"github.com/Vikasa2M/vikasa-collector/internal/vendors/cameleon/gesrtp"
	"github.com/Vikasa2M/vikasa-collector/sdk/adapter"
	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

// These tests use the current DecodeGateWord contract on synthetic words.
// They do not validate packed status bits on every PLC program.

const (
	wordAutoClosed  = 0x0021
	wordAutoOpening = 0x0031
	wordRefusedOpen = 0x2031
)

func TestACSLayoutProfiles(t *testing.T) {
	cases := []struct {
		name     string
		start, n int
		kind     string
		wantKind model.GateKind
	}{
		{name: "profile-10", start: 101, n: 10, kind: "unknown", wantKind: model.GateKindUnknown},
		{name: "profile-14", start: 101, n: 14, kind: "gate", wantKind: model.GateKindUnknown},
		{name: "profile-24", start: 101, n: 24, kind: "unknown", wantKind: model.GateKindUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := loadACS(t, oneDevice(tc.name, tc.start, tc.n, tc.kind))[0]
			words := repeatWord(tc.n, wordAutoClosed)
			client := attachWords(a, words)
			snap := readOK(t, a)
			assertSpan(t, client, tc.start, tc.n)
			ids := gateIDs(snap)
			if len(ids) != tc.n {
				t.Fatalf("gates = %d, want %d (%v)", len(ids), tc.n, ids)
			}
			highest := fmt.Sprintf("G%02d", tc.n)
			if ids[len(ids)-1] != highest {
				t.Fatalf("highest gate = %q, want %q", ids[len(ids)-1], highest)
			}
			for i, id := range ids {
				want := fmt.Sprintf("G%02d", i+1)
				if id != want {
					t.Fatalf("gate[%d] = %q, want %q", i, id, want)
				}
			}
			for _, g := range gateBank(t, snap).Gates {
				if g.Kind != tc.wantKind {
					t.Fatalf("%s kind = %v, want %v", g.ID, g.Kind, tc.wantKind)
				}
			}
			eng := synth.NewEngine(synth.NewGateDiffer(), synth.NewFaultDiffer())
			if evs := gateEvents(eng.Apply(snap)); len(evs) != 0 {
				t.Fatalf("first observation emitted %v", evs)
			}
			if evs := gateEvents(eng.Apply(readOK(t, a))); len(evs) != 0 {
				t.Fatalf("stable poll emitted %v", evs)
			}
			client.words[tc.n-1] = wordAutoOpening
			evs := gateEvents(eng.Apply(readOK(t, a)))
			if len(evs) != 1 || evs[0].GateID != highest || evs[0].DeviceID != tc.name {
				t.Fatalf("highest-gate change = %+v", evs)
			}
		})
	}
}

func TestACSRelocatedBlockIsNotPinnedTo101(t *testing.T) {
	const start, n = 240, 8
	a := loadACS(t, oneDevice("relocated", start, n, "unknown"))[0]
	client := attachWords(a, repeatWord(n, wordAutoClosed))
	snap := readOK(t, a)
	assertSpan(t, client, start, n)
	for _, rd := range client.reads {
		if rd[0] == 101 {
			t.Fatalf("read address 101 from a block that starts at %d", start)
		}
	}
	if ids := gateIDs(snap); len(ids) != n || ids[n-1] != "G08" {
		t.Fatalf("gates = %v", ids)
	}
	eng := synth.NewEngine(synth.NewGateDiffer(), synth.NewFaultDiffer())
	if evs := gateEvents(eng.Apply(snap)); len(evs) != 0 {
		t.Fatalf("first observation emitted %v", evs)
	}
	client.words[n-1] = wordAutoOpening
	evs := gateEvents(eng.Apply(readOK(t, a)))
	if len(evs) != 1 || evs[0].GateID != "G08" {
		t.Fatalf("events = %+v", evs)
	}
}

func TestACSNonconsecutiveGatesStayInsideConfiguredSpan(t *testing.T) {
	body := `  - id: sparse
    vendor: cameleon
    device_kind: acs
    poll_interval: 1s
    connection:
      srtp: { address: "203.0.113.10:18245" }
      dsw: { start: 300, length: 12 }
      gates:
        - { id: "G01", dsw: 300, kind: unknown }
        - { id: "G02", dsw: 305, kind: unknown }
        - { id: "G07", dsw: 311, kind: unknown }
`
	a := loadACS(t, body)[0]
	client := attachWords(a, repeatWord(12, wordAutoClosed))
	snap := readOK(t, a)
	assertSpan(t, client, 300, 12)
	if ids := gateIDs(snap); strings.Join(ids, ",") != "G01,G02,G07" {
		t.Fatalf("gates = %v", ids)
	}
	eng := synth.NewEngine(synth.NewGateDiffer(), synth.NewFaultDiffer())
	_ = eng.Apply(snap)
	client.words[1] = wordAutoOpening // %R301, inside the span, not a configured gate
	again := readOK(t, a)
	if ids := gateIDs(again); strings.Join(ids, ",") != "G01,G02,G07" {
		t.Fatalf("unconfigured word invented gates %v", ids)
	}
	if evs := gateEvents(eng.Apply(again)); len(evs) != 0 {
		t.Fatalf("unconfigured word emitted %v", evs)
	}
	client.words[11] = wordAutoOpening
	evs := gateEvents(eng.Apply(readOK(t, a)))
	if len(evs) != 1 || evs[0].GateID != "G07" {
		t.Fatalf("events = %+v", evs)
	}
}

func TestACSTwoControllersKeepIndependentG01(t *testing.T) {
	body := `  - id: east
    vendor: cameleon
    device_kind: acs
    poll_interval: 1s
    connection:
      srtp: { address: "203.0.113.10:18245" }
      dsw: { start: 101, length: 2 }
      gates:
        - { id: "G01", dsw: 101, kind: unknown }
        - { id: "G02", dsw: 102, kind: unknown }
  - id: west
    vendor: cameleon
    device_kind: acs
    poll_interval: 1s
    connection:
      srtp: { address: "203.0.113.11:18245" }
      dsw: { start: 480, length: 2 }
      gates:
        - { id: "G01", dsw: 480, kind: gate }
        - { id: "G02", dsw: 481, kind: gate }
`
	devs := loadACS(t, body)
	east, west := devs[0], devs[1]
	eastWords := attachWords(east, repeatWord(2, wordAutoClosed))
	westWords := attachWords(west, repeatWord(2, wordAutoClosed))
	eng := synth.NewEngine(synth.NewGateDiffer(), synth.NewFaultDiffer())
	eastSnap := readOK(t, east)
	westSnap := readOK(t, west)
	assertSpan(t, eastWords, 101, 2)
	assertSpan(t, westWords, 480, 2)
	if evs := eng.Apply(eastSnap); len(evs) != 0 {
		t.Fatalf("east baseline emitted %v", evs)
	}
	if evs := eng.Apply(westSnap); len(evs) != 0 {
		t.Fatalf("west baseline emitted %v", evs)
	}
	eastWords.words[0] = wordAutoOpening
	evs := gateEvents(eng.Apply(readOK(t, east)))
	if len(evs) != 1 || evs[0].DeviceID != "east" || evs[0].GateID != "G01" {
		t.Fatalf("east change = %+v", evs)
	}
	if evs := eng.Apply(readOK(t, west)); len(evs) != 0 {
		t.Fatalf("west changed because east did: %v", evs)
	}
	eastWords.words[0] = wordRefusedOpen
	refused := eng.Apply(readOK(t, east))
	if gates, raises, clears := countEvents(refused); gates != 0 || raises != 1 || clears != 0 {
		t.Fatalf("east refuse = gates %d raises %d clears %d", gates, raises, clears)
	}
	if fr := refused[0].(model.FaultRaised); fr.DeviceID != "east" || fr.FaultID != "gate/G01/command-refused" {
		t.Fatalf("raised = %+v", fr)
	}
	if evs := eng.Apply(readOK(t, west)); len(evs) != 0 {
		t.Fatalf("west saw east's fault: %v", evs)
	}
	eastWords.fail = fmt.Errorf("scripted read failed")
	if _, err := east.Read(context.Background()); err == nil {
		t.Fatal("east read succeeded")
	}
	failed := &model.Snapshot{
		DeviceID: "east", DeviceKind: "acs", SampledAt: time.Now().UTC(),
		Errors: []model.FacetError{{Kind: model.KindGateBank, Err: "scripted read failed"}},
	}
	if evs := eng.Apply(failed); len(evs) != 0 {
		t.Fatalf("east failure emitted %v", evs)
	}
	if evs := eng.Apply(readOK(t, west)); len(evs) != 0 {
		t.Fatalf("west emitted after east failed: %v", evs)
	}
}

func TestACSGenericKindNeedsNoWarningOrBarrier(t *testing.T) {
	for _, kind := range []string{"unknown", "gate"} {
		t.Run(kind, func(t *testing.T) {
			a := loadACS(t, oneDevice("kind-"+kind, 101, 1, kind))[0]
			attachWords(a, []uint16{wordAutoClosed})
			snap := readOK(t, a)
			gates := gateBank(t, snap).Gates
			if len(gates) != 1 || gates[0].Kind != model.GateKindUnknown {
				t.Fatalf("kind %s decoded as %+v", kind, gates)
			}
		})
	}
}

type recordingWords struct {
	words []uint16
	reads [][2]int
	fail  error
}

func (r *recordingWords) ReadWords(_ context.Context, mem byte, addr, count int) ([]uint16, error) {
	r.reads = append(r.reads, [2]int{addr, count})
	if mem != gesrtp.MemR {
		return nil, fmt.Errorf("memory type %d", mem)
	}
	if r.fail != nil {
		return nil, r.fail
	}
	if count != len(r.words) {
		return nil, fmt.Errorf("count %d, image %d", count, len(r.words))
	}
	return append([]uint16(nil), r.words...), nil
}

func (r *recordingWords) Close() error { return nil }

func loadACS(t *testing.T, devices string) []*acs {
	t.Helper()
	raw := "collector_id: layout-collector\nregion: us-ga\nagency: metro\nagency_unit: d01\nsite: cab-1\nmodel_version: openits/v1\ndevices:\n" + devices
	path := filepath.Join(t.TempDir(), "collector.yaml")
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	reg := adapter.NewRegistry()
	RegisterTo(reg)
	cfg, err := config.Load(path, reg)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]*acs, 0, len(cfg.Devices))
	for _, d := range cfg.Devices {
		built, err := reg.Build(d.Vendor, d.DeviceKind, d.ID, d.Connection)
		if err != nil {
			t.Fatalf("build %s: %v", d.ID, err)
		}
		out = append(out, built.(*acs))
	}
	return out
}

func oneDevice(id string, start, n int, kind string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "  - id: %s\n    vendor: cameleon\n    device_kind: acs\n    poll_interval: 1s\n    connection:\n      srtp: { address: \"203.0.113.10:18245\" }\n      dsw: { start: %d, length: %d }\n      gates:\n", id, start, n)
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "        - { id: \"G%02d\", dsw: %d, kind: %s }\n", i+1, start+i, kind)
	}
	return b.String()
}

func attachWords(a *acs, words []uint16) *recordingWords {
	c := &recordingWords{words: append([]uint16(nil), words...)}
	a.client = c
	return c
}

func readOK(t *testing.T, a *acs) *model.Snapshot {
	t.Helper()
	snap, err := a.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	snap.DeviceKind = "acs"
	return snap
}

func assertSpan(t *testing.T, c *recordingWords, addr, count int) {
	t.Helper()
	if len(c.reads) == 0 || c.reads[len(c.reads)-1] != [2]int{addr, count} {
		t.Fatalf("reads = %v, want last [%d %d]", c.reads, addr, count)
	}
}

func repeatWord(n int, w uint16) []uint16 {
	out := make([]uint16, n)
	for i := range out {
		out[i] = w
	}
	return out
}

func gateBank(t *testing.T, snap *model.Snapshot) model.GateBank {
	t.Helper()
	for _, f := range snap.Facets {
		if g, ok := f.(model.GateBank); ok {
			return g
		}
	}
	t.Fatal("no gate bank")
	return model.GateBank{}
}

func gateIDs(snap *model.Snapshot) []string {
	var bank model.GateBank
	for _, f := range snap.Facets {
		if g, ok := f.(model.GateBank); ok {
			bank = g
		}
	}
	ids := make([]string, len(bank.Gates))
	for i, g := range bank.Gates {
		ids[i] = g.ID
	}
	return ids
}

func gateEvents(evs []model.Event) []model.GateStatusChanged {
	var out []model.GateStatusChanged
	for _, ev := range evs {
		if g, ok := ev.(model.GateStatusChanged); ok {
			out = append(out, g)
		}
	}
	return out
}

func countEvents(evs []model.Event) (gates, raises, clears int) {
	for _, ev := range evs {
		switch ev.(type) {
		case model.GateStatusChanged:
			gates++
		case model.FaultRaised:
			raises++
		case model.FaultCleared:
			clears++
		}
	}
	return gates, raises, clears
}
