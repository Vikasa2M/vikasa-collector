package model

import "time"

// KindFaultSet is the set of faults currently raised on a device.
const KindFaultSet Kind = "fault-set"

// Fault is one raised fault. ID is stable and human-readable ("mmu-fault"),
// not a bit position: it is the identity synth diffs on, and the value the
// wire's fault_id carries.
type Fault struct {
	ID          string
	Severity    FaultSeverity
	Category    FaultCategory
	Description string
	// FirstObserved is when the fault began: the device's own time if it
	// reports one, else zero, and the fault differ stamps the first poll
	// that saw it and carries that forward (ADR 0024).
	FirstObserved time.Time `json:",omitzero"`
}

// FaultSet is every fault raised on the device at one poll. The differ takes
// the set-difference against the previous poll, so this carries no timing:
// a raise event's OccurredAt is the first observation.
type FaultSet struct{ Faults []Fault }

func (FaultSet) FacetKind() Kind { return KindFaultSet }
