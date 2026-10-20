# ADR 0024: Domain changes for openits-models v0.8.0

**Status:** Accepted (2026-10-20)
**Amends:** [ADR 0013](0013-absence-of-evidence.md)'s application to the
signal facet (the rule itself stands); the reachability fallback in
[ADR 0019](0019-collector-self-health.md)'s 2026-10-14 amendment gains a
second trigger.

## Context
openits-models v0.8.0 changed what several events mean, not only how they
are spelled. Five of those changes reach past the emitter into the domain
model, which `sdk/model` owns (ADR 0002). The emitter only maps; it cannot
honestly invent a fact the domain does not carry.

- **Signal "mode" split into independent facts.** `controller-mode` now
  means coordination only (`mode-coordinated`, `mode-free`, `mode-unknown`).
  Flash and dark became `cabinet-output` (normal, flash, dark) with a
  `flash-cause`, published by a new event, `cabinet-output-changed`.
  Manual control moved to `control-source`, and preemption and priority
  became flags. The collector's single `ControllerMode` (normal, flash,
  standby, off) is, in what `ntcip-asc` reads (NTCIP unit operation
  status), really cabinet output. Nothing reads coordination today.
- **DMS mode-changed is bound to sign mode.** A DMS control-mode change
  (central to local) no longer rides `mode-changed`, and upstream has not
  yet moved it to `control-source`.
- **Faults carry `first-observed`.** It is mandatory on both fault-raised
  and fault-cleared: the device's time when it reports one, otherwise the
  time the fault was first seen. NTCIP alarm bits carry no time.
- **comm-lost carries `last-contact`.** It is mandatory for comm-lost: the
  last time the observer got a valid response. A device that has not
  answered since the collector started has no such time.
- **operational-status-report is rebuilt** on the shared status summary.

## Decision
1. **The signal facet is split to match.** `SignalStatus` gains
   `CabinetOutput` (unknown, normal, flash, dark) and `FlashCause`. Its
   `Mode` means coordination only (unknown, coordinated, free), and the old
   normal, flash, standby and off values are removed. The old
   conflict-flash flag becomes `FlashCause = monitor`.
   - A new domain event, `CabinetOutputChanged`, is emitted when the cabinet
     output changes. `ModeChanged` is emitted for coordination changes.
   - Each event has one ce-type, so the (event kind, device kind) → ce-type
     table, `CETypes()` and the catalog checks are unchanged in kind. The
     alternative, routing one domain event to two ce-types by value inside
     the emitter, was rejected for breaking that.
   - `ntcip-asc` maps NTCIP unit operation status 2 to cabinet output
     normal and 4 to flash (cause monitor when its conflict-flash reading
     says so). Standby and unrecognized values are unknown. It reports
     coordination as unknown until an adapter reads NTCIP coordination
     status, which needs hardware to verify.
   - Absence of evidence is still not a change (ADR 0013): a transition is
     reported only when the new value is known. An unknown previous value
     is sent as an absent previous field, not as a transition from unknown.
2. **DMS control-mode changes are declined.** `DMSControlModeChanged` has
   no ce-type in v0.8.0, so the emitter does not claim it, and it is
   dropped loudly and counted (`dropped_no_emitter`), as any catalog gap is.
   Mapping it onto anything available would fail conformance. It is
   reclaimed when upstream moves DMS control onto `control-source`. DMS
   display-state changes still ride `mode-changed`.
3. **The engine remembers when each fault was first seen.**
   - `model.Fault` gains `FirstObserved`. An adapter may set it from the
     device's own time.
   - Otherwise the fault differ stamps the time of the first poll that saw
     the fault, and carries it forward on every later poll. It is stored
     with the rest of the facet, so it survives a restart in the
     `VIKASA_SYNTH_STATE` bucket (ADR 0017).
   - Both fault-raised and fault-cleared carry it.
4. **comm-lost needs a known last contact, or it degrades.**
   - `DeviceStatusChanged` gains `LastContact`, the runner's last
     successful poll.
   - When there is none (the device has not answered since boot), the
     catalog emitter declines the event. The collector-owned
     `openits-collector.health.device-status-changed.v1` then carries it,
     because the emitter chain falls through to the health emitter. That
     schema makes no last-contact claim, so the loss is still reported,
     without inventing a time.
   - comm-restored needs no last contact and always goes to the catalog.
5. **A clear names the same fault class as its raise.** v0.8.0's
   conformance check requires a fault event's `kind` to be one of the
   service's fault identities. Clears used to send the service's base event
   kind, because the domain `FaultCleared` carried only the fault id.
   `FaultCleared` now carries the cleared fault's category, from the
   remembered state, so both halves map to the same specific identity.
   - A category with no specific identity still falls back to the
     service's base fault kind. The v0.8.0 YANG documents that base as
     "the fallback kind when a decoder cannot map a more specific leaf
     identity", while the v0.8.0 conformance check rejects it. The
     collector follows the normative YANG, and the contradiction is
     reported upstream. No shipped adapter reaches the fallback: every
     `ntcip-asc` alarm category maps, and `ntcip-dms` reads no faults.
6. **operational-status-report publishes what the domain knows:**
   - cabinet output and flash cause;
   - the coordination mode, when known;
   - whether preemption is active.
   
   Everything else upstream added (oper status, monitor status, control
   source, uptime, mode-since) is left absent rather than guessed, and
   comes when an adapter reads it.

## Consequences
- A signal controller in flash now reports `cabinet-output-changed`
  (normal to flash, cause monitor), not `mode-changed` to flash. Consumers
  of the old shape must move. Pre-1.0, with no release shipped, this costs
  nothing.
- `ntcip-asc` publishes no `mode-changed` until coordination is read. That
  is a loss of nothing true: the old `mode-changed` to normal was a
  cabinet-output fact under a coordination name.
- DMS central/local takeovers are not on the catalog bus until upstream
  provides a place for them. The drop is counted, never silent.
- `first-observed` after a restart is the stored first-seen time, not the
  restart time, so a fault standing across an upgrade keeps its age.

## Alternatives considered
- **Translate in the emitter.** It would keep one domain `Mode` and send
  normal↔flash as cabinet-output-changed. Rejected: one domain event with
  value-dependent ce-types breaks the routing table, `CETypes()`, and the
  catalog conformance checks.
- **Send comm-lost with the boot time as last contact.** Rejected: it
  claims a contact that never happened.
- **Stamp first-observed with the event's own time.** Rejected for clears:
  it would claim the fault began when it ended.
