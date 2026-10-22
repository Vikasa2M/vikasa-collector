package synth

import (
	"sort"
	"time"

	"github.com/Vikasa2M/vikasa-collector/sdk/model"
)

// NewFaultDiffer diffs the fault-set facet: a raise for each fault that
// appeared, a clear for each that vanished.
func NewFaultDiffer() Differ { return faultDiffer{} }

type faultDiffer struct{}

func (faultDiffer) Facet() model.Facet { return model.FaultSet{} }

// Carry stamps each fault with when it was first seen: the device's own time
// if the adapter supplied one, else the time remembered from an earlier poll,
// else this poll's time. Carried forward, it survives restarts with the rest
// of the stored facet (ADR 0024).
func (faultDiffer) Carry(prev, curr model.Facet, at time.Time) model.Facet {
	var seen map[string]model.Fault
	if prev != nil {
		seen = index(prev.(model.FaultSet))
	}
	in := curr.(model.FaultSet).Faults
	out := make([]model.Fault, len(in))
	for i, f := range in {
		if f.FirstObserved.IsZero() {
			if p, ok := seen[f.ID]; ok && !p.FirstObserved.IsZero() {
				f.FirstObserved = p.FirstObserved
			} else {
				f.FirstObserved = at
			}
		}
		out[i] = f
	}
	return model.FaultSet{Faults: out}
}

func (faultDiffer) Diff(prev, curr model.Facet, base model.Base) []model.Event {
	currFaults := index(curr.(model.FaultSet))
	var prevFaults map[string]model.Fault
	if prev != nil {
		prevFaults = index(prev.(model.FaultSet))
	}

	// Sorted iteration: gen-1 ranged over a map here, making event order
	// nondeterministic. Determinism is a construction-time requirement.
	var events []model.Event
	for _, id := range sortedIDs(currFaults) {
		if _, existed := prevFaults[id]; existed {
			continue // still raised; an attribute change is not a raise
		}
		f := currFaults[id]
		events = append(events, model.FaultRaised{
			Base: base, FaultID: f.ID, Severity: f.Severity,
			Category: f.Category, Description: f.Description, FirstObserved: f.FirstObserved,
			Type: f.Type, GateID: f.GateID,
		})
	}
	for _, id := range sortedIDs(prevFaults) {
		if _, still := currFaults[id]; still {
			continue
		}
		// A fault restored from state written before first-seen times were
		// kept has none; its clear then claims no start rather than a wrong one.
		p := prevFaults[id]
		events = append(events, model.FaultCleared{
			Base: base, FaultID: id, Category: p.Category, FirstObserved: p.FirstObserved,
			Type: p.Type, GateID: p.GateID,
		})
	}
	return events
}

func index(fs model.FaultSet) map[string]model.Fault {
	out := make(map[string]model.Fault, len(fs.Faults))
	for _, f := range fs.Faults {
		out[f.ID] = f
	}
	return out
}

func sortedIDs(m map[string]model.Fault) []string {
	ids := make([]string, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
