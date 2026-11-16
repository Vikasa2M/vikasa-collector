package cameleon

import (
	"context"
	"sync"
	"time"

	"github.com/Vikasa2M/vikasa-collector/sdk/adapter"
	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

// ScriptedPLC is an in-memory ACS reader for tests. It uses the same
// watchdog window as a configured adapter. The test moves the clock and
// the counter; nothing is dialed.
//
// The type is in a non-_test.go file so tests in another package can import
// it. A helper in a _test.go file is visible only inside package cameleon.
type ScriptedPLC struct {
	mu     sync.Mutex
	a      *acs
	words  []uint16
	clock  time.Time
	stall  time.Duration
	polls  int
	reader wordClient
}

// NewScriptedPLC returns a reader whose %R90 watchdog counter starts at
// counter and whose stall window is stall. start is the first sample's clock.
func NewScriptedPLC(counter uint16, stall time.Duration, start time.Time) *ScriptedPLC {
	words := make([]uint16, 110)
	words[0] = counter << 11
	s := &ScriptedPLC{words: words, clock: start, stall: stall}
	inner := &staticWords{start: 90, words: words}
	s.reader = guardedWords{plc: s, inner: inner}
	s.a = s.readerFor("acs-1")
	return s
}

// readerFor builds a new ACS adapter over this image. Watchdog history
// starts empty. The clock and the words stay shared.
func (s *ScriptedPLC) readerFor(deviceID string) *acs {
	a := NewACS(deviceID, s.reader, 90, 110,
		[]gateCfg{{id: "WG-1", kind: model.GateKindWarning, dsw: 101}},
		cabinetCfg{},
	).(*acs)
	a.plc = plcCfg{watchdogStall: s.stall}
	a.now = func() time.Time {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.clock
	}
	return a
}

// Register installs this PLC as the cameleon-acs factory. Each Build
// returns a new ACS adapter, so a restarted collector does not keep the
// previous process's watchdog history. Nothing is dialed.
func (s *ScriptedPLC) Register(reg *adapter.Registry) {
	reg.Register(acsDescriptor, func(deviceID string, _ map[string]any) (adapter.Adapter, error) {
		return s.readerFor(deviceID), nil
	})
}

// Polls is the number of successful reads.
func (s *ScriptedPLC) Polls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.polls
}

// SetClock moves the fake sample clock. It does not sleep.
func (s *ScriptedPLC) SetClock(t time.Time) {
	s.mu.Lock()
	s.clock = t
	s.mu.Unlock()
}

// SetCounter replaces the five-bit watchdog counter in %R90. Other status
// bits, including the PLC fault bit, stay clear.
func (s *ScriptedPLC) SetCounter(counter uint16) {
	s.mu.Lock()
	s.words[0] = counter << 11
	s.mu.Unlock()
}

// FailReads makes the next Read return err, the same way a timed-out SRTP
// read does. An already-raised stall is kept.
func (s *ScriptedPLC) FailReads(err error) { s.a.client = failWords{err: err} }

// Resume restores scripted word reads after FailReads.
func (s *ScriptedPLC) Resume() { s.a.client = s.reader }

// Read decodes one sample and marks it as an ACS snapshot.
func (s *ScriptedPLC) Read(ctx context.Context) (*model.Snapshot, error) {
	snap, err := s.a.Read(ctx)
	if snap != nil {
		snap.DeviceKind = "acs"
	}
	return snap, err
}

// guardedWords serializes fixture reads with the scripted clock and counter.
type guardedWords struct {
	plc   *ScriptedPLC
	inner *staticWords
}

func (g guardedWords) ReadWords(ctx context.Context, mem byte, addr, count int) ([]uint16, error) {
	g.plc.mu.Lock()
	defer g.plc.mu.Unlock()
	g.plc.polls++
	return g.inner.ReadWords(ctx, mem, addr, count)
}

func (g guardedWords) Close() error { return nil }

type failWords struct{ err error }

func (f failWords) ReadWords(context.Context, byte, int, int) ([]uint16, error) {
	return nil, f.err
}

func (f failWords) Close() error { return nil }
