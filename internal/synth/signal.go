package synth

import "github.com/Vikasa2M/vikasa-collector/sdk/model"

// NewSignalDiffer diffs the signal-status facet: a status report every
// poll, plus transition events when a previous value exists.
func NewSignalDiffer() Differ { return signalDiffer{} }

type signalDiffer struct{}

func (signalDiffer) Facet() model.Facet { return model.SignalStatus{} }

func (signalDiffer) Diff(prev, curr model.Facet, base model.Base) []model.Event {
	c := curr.(model.SignalStatus)
	events := []model.Event{model.OperationalStatusReport{
		Base: base, Mode: c.Mode, CabinetOutput: c.CabinetOutput, FlashCause: c.FlashCause,
		PreemptionActive: c.PreemptionActive, ActivePlanID: c.ActivePlanID,
	}}
	if prev == nil {
		return events
	}
	p := prev.(model.SignalStatus)
	// Coordination and cabinet output are independent axes (ADR 0024): a
	// coordinated controller can go to flash without its mode changing, so
	// each reports on its own. A move TO unknown is not evidence of a change
	// (ADR 0013); a move FROM unknown is, and carries an unknown From.
	if p.Mode != c.Mode && c.Mode != model.ModeUnknown {
		events = append(events, model.ModeChanged{Base: base, From: p.Mode, To: c.Mode})
	}
	if p.CabinetOutput != c.CabinetOutput && c.CabinetOutput != model.OutputUnknown {
		ev := model.CabinetOutputChanged{Base: base, From: p.CabinetOutput, To: c.CabinetOutput}
		if c.CabinetOutput == model.OutputFlash {
			ev.Cause = c.FlashCause
		}
		events = append(events, ev)
	}
	if p.ActivePlanID != c.ActivePlanID {
		events = append(events, model.PlanChanged{Base: base, FromPlanID: p.ActivePlanID, ToPlanID: c.ActivePlanID})
	}
	if !p.PreemptionActive && c.PreemptionActive {
		events = append(events, model.PreemptionActivated{Base: base, Source: c.PreemptionSource})
	}
	if p.PreemptionActive && !c.PreemptionActive {
		events = append(events, model.PreemptionCleared{Base: base})
	}
	return events
}
