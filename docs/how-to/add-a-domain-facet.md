# Add a domain facet

**At the end** you will have a new concept in the domain model — a kind of
device state nothing in `sdk/model` represents yet — as a facet, the events
it produces, and a differ that turns one into the other, with the differ
tests a reviewer expects. Mapping those events onto the wire is the next
guide, [`map-an-event-to-the-wire.md`](map-an-event-to-the-wire.md).

**Check first that you need one.** Most adapter work reuses an existing
facet, and several are modeled and diffed with no adapter producing them
yet — see
[`add-a-vendor-adapter.md`](add-a-vendor-adapter.md#choose-the-device-kind-and-check-for-an-existing-facet).

**You need** [`adapter-to-model.md`](../explanation/adapter-to-model.md)
read. It covers the shape a facet and differ must respect, including the
present/absent/empty distinction; this guide stays procedural.

The DMS domain is the worked example throughout: facet in
`sdk/model/dms.go`, events in `sdk/model/events.go`, differ in
`internal/synth/dms.go`. The steps, in order:

1. [Define the facet](#facet-sdkmodeldomaingo) in `sdk/model/<domain>.go`.
2. [Define its events](#events-sdkmodeleventsgo) in `sdk/model/events.go`.
3. [Write the differ](#differ-internalsynthdomaingo) in
   `internal/synth/<domain>.go` and register it in `internal/app/app.go`.
4. [Write the differ tests](#tests).
5. [Map the events to the wire](#the-ripple-your-new-facet-usually-needs-a-wire-mapping-too),
   usually as a follow-up.
6. [Verify](#verify).

## Facet (`sdk/model/<domain>.go`)

A facet is a plain struct implementing `FacetKind() Kind`, with its own
`Kind<Name>` constant (`KindDMSStatus`, `KindDetectorSamples`, and so on —
`sdk/model/model.go`'s `Facet` interface is the whole contract).

- **Keep it lossless.** Store what the device reports at the precision it
  reports, not the precision the wire needs. `DetectorSample`'s
  `OccupancyTenths` field keeps half-percent resolution exactly; the wire
  emitter rounds later, in `internal/wire`, never here — a fidelity loss
  belongs at the mapping layer, where it's a reviewable, one-line
  decision, not baked silently into the domain type.
- **Document zero-value and empty semantics on the type.** Say, on the
  type itself, whether "this device doesn't have this subsystem" is
  representable as an empty facet rather than an error. See
  [`adapter-to-model.md`'s "Present, absent, or empty" section](../explanation/adapter-to-model.md#present-absent-or-empty-three-states-a-facet-can-be-in)
  for the three states a facet can be in and `readFaultSet`'s worked
  example of getting both non-obvious cases right.
- **Enums get their own named type with a `String()` and an explicit
  "unknown" zero value**, the way `DMSControlMode` and `DMSDisplayState`
  do in `sdk/model/dms.go` (see `sdk/model/enums.go` for the pattern
  applied to `ControllerMode` and friends). The unknown value exists so an
  adapter that reads a device value it doesn't recognize has somewhere
  honest to put it, rather than guessing or defaulting to whatever value
  happens to be numbered zero.

## Events (`sdk/model/events.go`)

Every event embeds `Base` (`DeviceID`, `DeviceKind`, `OccurredAt`) and
implements `EventKind() string`. Two things worth getting right before you
add one:

- **`DeviceKind` is stamped by the runner, never set by adapters or
  differs.** `Base`'s doc comment in `sdk/model/events.go` says why the
  field exists at all: the catalog defines some event shapes once and
  reuses them across services, and without `DeviceKind` a wire emitter has
  no way to route a shared event to the right one.
- **Prefer transition events over bare state reports.** `DMSControlModeChanged`
  and `DMSDisplayStateChanged` (`sdk/model/events.go`) carry `From`/`To`
  and fire only when the differ observes a change. A periodic report
  (`OperationalStatusReport`, emitted every poll) is a different,
  deliberate shape: only add one when the catalog defines a status-report
  ce-type for your service, because otherwise there is nothing to map it
  onto. Most catalog services have none, and the collector does not get to
  invent one — see
  [openits-models is not reshaped to suit the collector](../reference/invariants.md#openits-models-is-not-reshaped-to-suit-the-collector).

  `internal/synth/dms.go`'s doc comment works through the consequence: the
  DMS status facet emits on transitions only, so a restarted collector does
  not re-announce a sign's state until it next changes. The answer to that
  is [ADR 0017](../adr/0017-durable-synth-state.md)'s durable previous
  state, not inventing a transition that did not happen.

## Differ (`internal/synth/<domain>.go`)

A differ implements `synth.Differ`
(`internal/synth/synth.go`): `Facet() model.Facet` returns the zero value
of the one facet type it diffs, from which the engine takes its kind, and
`Diff(prev, curr model.Facet, base model.Base) []model.Event` does the
work. Register it by adding its constructor to the `synth.NewEngine(...)`
call in `internal/app/app.go`, which lists every differ the collector
runs.

- `prev == nil` means first observation: there is no known prior state to
  transition from, so a differ observing DMS's shape returns no events on
  first poll (`dmsDiffer.Diff` in `internal/synth/dms.go`). The fault
  differ is the deliberate exception — it raises everything currently
  raised on first poll, because a standing fault is a state to report, not
  a transition to detect.
- **A failed or absent read never reaches your differ at all.** See
  [`docs/reference/invariants.md`'s row on this](../reference/invariants.md#absence-of-evidence-is-never-a-state-change)
  for the rule this mechanism exists to hold. Mechanically:
  `synth.Engine.Apply` (`internal/synth/synth.go`) only ever ranges over
  `snap.Facets`, so a facet kind with no entry there this poll is simply
  never looked up, and your differ is never called for it — the engine's
  remembered state survives untouched. A new differ does not need to (and
  should not try to) handle a "failed read" case itself; there is nothing
  to handle, because `Diff` is never invoked for that poll.
- **Independent axes diff independently.** `dmsDiffer.Diff` checks
  control-mode and display-state separately and emits a separate event for
  each that changed, even when both move in the same poll — never one
  combined event covering two unrelated axes.
- **Counters: a decrease is a device reset, not a negative delta.** See
  `internal/synth/detector.go`'s stateful differ (it tracks
  `last map[string]time.Time` per device rather than trusting the engine's
  generic `prev`/`curr` pair alone) for the shape this takes when a differ
  needs more memory than one previous facet value.

## Tests

[`docs/reference/test-requirements.md`'s "A new differ" section](../reference/test-requirements.md#a-new-differ)
is the checklist a reviewer holds a new differ to — read it rather than
this paraphrase. In short, mirror `internal/synth/dms_test.go`
(`TestDMSFirstPollEmitsNothing`, `TestDMSNoChangeEmitsNothing`,
`TestDMSAxesChangeIndependently`, `TestDMSBothAxesChangeAtOnce`,
`TestDMSFailedReadEmitsNothing`, `TestDMSEventsCarryDeviceKind`), not
`internal/synth/signal_test.go` — that document explains why. Facet decode
tests (proving an adapter actually produces the facet you defined) live
with the adapter, not with the differ; see
[`add-a-vendor-adapter.md`](add-a-vendor-adapter.md) for that side.

Your differ's previous state survives restarts in the `VIKASA_SYNTH_STATE`
bucket ([ADR 0017](../adr/0017-durable-synth-state.md)), stored as the
facet's JSON. Add the new facet to
`TestDecodeFacetRoundTripsEveryDurableKind` (`internal/synth/durable_test.go`)
so a restart is proven to read back exactly what was written. A facet that
changes on every poll, like detector counters, would wear the broker's
storage for nothing: give its differ a `Volatile() bool` method returning
true, as `internal/synth/detector.go` does, and it is never persisted.

## The ripple: your new facet usually needs a wire mapping too

A facet and differ with no wire mapping is a legitimate, working interim
state — unmapped events drop loudly at the emitter chain rather than
blocking anything, per the drop rule in
[`docs/explanation/wire-boundary.md`](../explanation/wire-boundary.md#the-drop-rule-decline-rather-than-approximate) —
but it's rarely the end state you want. Once your facet and differ compile
and pass their tests, the next step is deciding how each new event maps
onto an openits-models ce-type (or, deliberately, that it shouldn't). That
decision, plus the golden test that has to accompany it, is
[`map-an-event-to-the-wire.md`](map-an-event-to-the-wire.md)'s whole
subject.

## Verify

```bash
make check
```

This is the CI gate: `go vet`, golangci-lint (gofmt included), the
go.mod tidiness check, the whole suite under `-race`, and the boundary and
docs lints. A facet or differ change never touches `internal/wire` or
imports openits-models, so if the boundary lint flags your change, the
concept belongs somewhere else in the layering. `-race` matters here
because `synth.Engine.Apply` is called from every device's poll goroutine
— see
[`testing-strategy.md`'s `-race` section](../explanation/testing-strategy.md#-race-because-polling-and-publishing-are-concurrent).
