package cameleon

import (
	"context"
	"sync"

	"github.com/Vikasa2M/vikasa-collector/sdk/adapter"
)

// ScriptedGateBank is an in-memory DSW image for tests. Each Build returns
// the real ACS reader over this image. Nothing is dialed.
type ScriptedGateBank struct {
	mu       sync.Mutex
	start    int
	words    []uint16
	gates    []GateWord
	attempts int
	polls    int
	fail     error
	current  *acs
}

// NewScriptedGateBank copies words. start is the 1-based %R of words[0].
func NewScriptedGateBank(start int, words []uint16, gates []GateWord) *ScriptedGateBank {
	return &ScriptedGateBank{
		start: start,
		words: append([]uint16(nil), words...),
		gates: append([]GateWord(nil), gates...),
	}
}

// Register installs this bank as the cameleon-acs factory. Every Build
// shares the same image, so a restarted collector reads the words the
// test set.
func (s *ScriptedGateBank) Register(reg *adapter.Registry) {
	reg.Register(acsDescriptor, func(deviceID string, _ map[string]any) (adapter.Adapter, error) {
		gs := make([]gateCfg, len(s.gates))
		for i, g := range s.gates {
			gs[i] = gateCfg{id: g.ID, kind: g.Kind, dsw: g.DSW}
		}
		a := NewACS(deviceID, s, s.start, len(s.words), gs, cabinetCfg{}).(*acs)
		s.mu.Lock()
		s.current = a
		s.mu.Unlock()
		return a, nil
	})
}

// Restore puts this bank back after a failed read. The ACS reader drops
// its client on a read error and, with no dial address, does not replace
// it. A live reader would dial again.
func (s *ScriptedGateBank) Restore() {
	s.mu.Lock()
	a := s.current
	s.fail = nil
	s.mu.Unlock()
	if a == nil {
		return
	}
	a.mu.Lock()
	a.client = s
	a.mu.Unlock()
}

// SetWord replaces one word in the image. index 0 is start.
func (s *ScriptedGateBank) SetWord(index int, w uint16) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.words[index] = w
}

// Fail makes later reads return err until Fail(nil).
func (s *ScriptedGateBank) Fail(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fail = err
}

// Attempts is the number of ReadWords calls, including failed ones.
func (s *ScriptedGateBank) Attempts() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.attempts
}

// Polls is the number of ReadWords calls that returned words.
func (s *ScriptedGateBank) Polls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.polls
}

func (s *ScriptedGateBank) ReadWords(ctx context.Context, mem byte, address, count int) ([]uint16, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.attempts++
	if s.fail != nil {
		return nil, s.fail
	}
	s.polls++
	return (&staticWords{start: s.start, words: s.words}).ReadWords(ctx, mem, address, count)
}

func (s *ScriptedGateBank) Close() error { return nil }
