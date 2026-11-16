# Facets and the synth engine: from a device reading to a domain event

[`architecture.md`](architecture.md) walks the whole pipeline. This document
goes deep on its first half: what a `Snapshot` is, what an adapter may and
may not put in one, and how `synth.Engine.Apply` turns snapshots into domain
events. Most contributors start here, because adding a facet, adding a
differ, and writing an adapter all happen in this layer.

## Snapshot: a header plus typed facets

Every poll produces one `model.Snapshot` (`sdk/model/model.go`):

```go
type Snapshot struct {
	DeviceID   string
	DeviceKind string
	SampledAt  time.Time
	Facets     []Facet
	Errors     []FacetError
}
```

There is no single struct with a field for every possible reading. A
snapshot is a header (`DeviceID`, `DeviceKind`, `SampledAt`), a list of
facets, and a list of `FacetError`s for anything the adapter tried to read
and could not. A facet is any typed struct that implements
`Facet interface{ FacetKind() Kind }`.

The adapter does not set `DeviceKind`. The runner stamps it after `Read`
returns, from the adapter's own `Descriptor()`:

```go
// internal/runner/runner.go
snap.DeviceKind = r.deviceKind
```

This is structural, not style. The device kind must be right for every
snapshot, and it comes from the same `Descriptor` the adapter was
registered under. An adapter has no code path to report a kind that
disagrees with its registration.

## Ten facet kinds

A facet is one typed slice of device state. There are ten facet kinds
today; `grep -n 'Kind = "' sdk/model/*.go` lists them.

| Kind constant | Facet type | What it holds |
|---|---|---|
| `KindSignalStatus` | `SignalStatus` | coordination mode, cabinet output and flash cause, active plan, preemption |
| `KindFaultSet` | `FaultSet` | every fault currently raised on the device |
| `KindDetectorSamples` | `DetectorSamples` | per-channel vehicle detector counts, one sample per answering channel |
| `KindDMSStatus` | `DMSStatus` | a dynamic message sign's control mode, display state, and face message |
| `KindDMSEnvironment` | `DMSEnvironment` | a sign's environment and illumination sensors |
| `KindCCTVStatus` | `CCTVStatus` | who is driving a camera and what its tours are doing |
| `KindTrafficIntervals` | `TrafficIntervals` | a completed measurement interval from a roadside traffic sensor |
| `KindZoneIncidents` | `ZoneIncidents` | incidents a perception sensor currently observes in its zones |
| `KindZoneIntervals` | `ZoneIntervals` | a completed counting interval over a perception sensor's zones |
| `KindGateBank` | `GateBank` | per-gate mode and position for a reversible-lane ACS |

Three adapters ship today. `ntcip-asc` produces `SignalStatus`, `FaultSet`,
and `DetectorSamples`. `ntcip-dms` produces `DMSStatus` and
`DMSEnvironment`. `cameleon-acs` produces `GateBank` and `FaultSet`. The
remaining kinds are fully modelled, diffed, and mapped to the wire, with
no adapter producing them yet. Adding an adapter for one means writing the
transport read, not changing the domain model.

## Present, absent, or empty: three states a facet can be in

For one device on one poll, each facet kind is in exactly one of three
states. Adapters must get the difference right.

1. **Present with data.** The adapter read the facet and appended it to
   `snap.Facets`.
2. **Present and empty.** The adapter read the facet and the device had
   nothing to put in it: a controller with no active faults, a controller
   with no detectors wired, a quiet road with no incidents. The facet is
   still appended to `snap.Facets`; it just holds an empty list.
3. **Absent.** The read failed: a mandatory OID did not answer, a request
   timed out, or the reply could not be parsed. The adapter appends nothing
   to `snap.Facets` for this kind, and appends a `FacetError` to
   `snap.Errors` instead.

`readFaultSet` in `internal/vendors/ntcip/asc.go` shows the two
non-obvious cases side by side:

```go
func (a *asc) readFaultSet(snap *model.Snapshot, vals map[string]int64) {
	bits, ok := vals[oidShortAlarmStatus]
	if !ok {
		// Defaulting to "no faults" here would clear every real fault
		// downstream — the exact gen-1 failure this design exists to prevent.
		snap.Errors = append(snap.Errors, model.FacetError{
			Kind: model.KindFaultSet, Err: "short-alarm-status OID unanswered",
		})
		return
	}
	// Zero bits is an EMPTY fault set, not an error: a healthy controller.
	fs := model.FaultSet{}
	for _, f := range alarmBitmap {
		if bits&(1<<f.bit) == 0 {
			continue
		}
		fs.Faults = append(fs.Faults, model.Fault{
			ID: f.id, Severity: f.severity, Category: f.category, Description: f.description,
		})
	}
	snap.Facets = append(snap.Facets, fs)
}
```

If the alarm OID answers with zero, the loop appends nothing, and the empty
`FaultSet{}` still goes into `snap.Facets`: case 2, a healthy controller.
If the OID does not answer, the function returns before building any
`FaultSet`: case 3, recorded as a `FacetError`. The model's own doc
comments make the same point: `DetectorSamples` says a controller with no
detectors "yields an EMPTY facet, not an error", because a `FacetError`
would be a permanent false alarm.

The synth engine never talks to the device, so these three states are all
it has to go on. What it does with an absent facet is a rule with a
canonical statement:
[absence of evidence is never a state change](../reference/invariants.md#absence-of-evidence-is-never-a-state-change).

## Failure is isolated per facet

```go
type FacetError struct {
	Kind Kind
	Err  string
}
```

A `FacetError` says which facet kind failed and why, nothing more. One poll
can carry several `FacetError`s alongside several good facets.
`ntcip-asc`'s `Read` calls `readSignalStatus`, `readFaultSet`, and
`readDetectors` independently, and each decides for itself whether its OIDs
answered. A missing alarm OID does not stop the detector read and does not
fail the poll. It produces one `FacetError` for `KindFaultSet` in a
snapshot that may otherwise be healthy. `ntcip-dms` does the same with its
two facets: a failed environment read leaves the sign-status facet intact.

A whole poll fails only when `Read` returns an error, which an adapter does
when the device is unreachable. The runner turns that into a
device-reachability event; see
[the poll loop](architecture.md#the-poll-loop-jitter-per-poll-timeout-and-panic-isolation).

## How `synth.Engine.Apply` consumes facets

Each differ declares the one facet type it diffs, through its `Facet()`
method. `synth.NewEngine` registers one differ per facet kind and panics on
a duplicate. `Apply`, simplified:

```go
func (e *Engine) Apply(snap *model.Snapshot) []model.Event {
	if e.hold && !e.seeded[snap.DeviceID] {
		return nil // held: previous state not loaded yet
	}
	...
	for _, f := range snap.Facets {
		k := f.FacetKind()
		d, ok := e.differs[k]
		if !ok {
			continue // facet kind with no differ registered: carried, not diffed
		}
		if snap.FacetFailed(k) {
			continue // also reported failed: not evidence
		}
		if reflect.TypeOf(f) != e.types[k] {
			// wrong Go type for its kind: an adapter bug, reported and skipped
			continue
		}
		events = append(events, d.Diff(dev[k], f, base)...)
		dev[k] = f
	}
	return events
}
```

The loop only ever iterates `snap.Facets`. For each facet present, it calls
the differ with the last facet of that kind it saw for this device
(`dev[k]`, `nil` on the first observation), collects the events, and
remembers this poll's value for next time.

An absent facet never appears in the loop, so its differ is not called and
its remembered value is untouched. There is no `if failed` branch that
could be written wrong. `Apply` also guards against adapter mistakes: a
facet that is present *and* listed in `snap.Errors` is skipped, and so is a
facet whose Go type is not the one its differ diffs (counted as
`facets_rejected`). Neither is evidence.

Two further behaviours come from durable state
([ADR 0017](../adr/0017-durable-synth-state.md)). At boot the engine is
held, and diffs nothing for a device until `internal/durable` has seeded
that device's previous state. After that, every remembered facet is written
through to the JetStream key-value bucket, except for volatile kinds such
as detector counters that change on every poll.
[`architecture.md`](architecture.md#where-state-lives) summarises where
this state lives.

## Facets are per-device-kind, never per-vendor

`internal/synth`'s package doc says it in one line: "One registered Differ
per facet kind; the engine never grows vendor or wire knowledge." Nothing
in `internal/synth` branches on the vendor that produced a facet. Differs
are keyed on `model.Kind`, and the package has no `Vendor` field anywhere.

This is a governance rule, not just an implementation detail. A facet
exists for a device **kind** (a signal controller, a sign, a perception
sensor), never for one vendor's product line. A facet bent to fit one
vendor's device is a bend every later adapter for that kind inherits.

So if your adapter reads something no facet captures, there is one route:
propose a new facet, or a new field on an existing one, argued at the
device-kind level ("every ASC has this", not "our controller reports
this"). [`add-a-domain-facet.md`](../how-to/add-a-domain-facet.md) is that
workflow.

**There is deliberately no generic extension mechanism**: no attribute bag,
no `map[string]string` passthrough, nothing an adapter can fill without a
reviewed type. An untyped bag would reach `internal/wire` as keys the
emitter has never seen. The emitter's discipline is an explicit
map-or-drop decision per value (see
[the drop rule](wire-boundary.md#the-drop-rule-decline-rather-than-approximate)),
and it cannot decide about a key nobody declared. The bag would be either
dropped wholesale, which makes it pointless, or forwarded unexamined, which
puts vendor-shaped data on a catalog-shaped bus.

A reading with no home in the catalog may have nowhere to go yet, and that
is an acceptable answer. The collector adapts to the catalog, not the
reverse ([ADR 0016](../adr/0016-collector-as-transitional-shim.md)). A
missing domain concept is argued upstream on its merits, not routed around
with a passthrough field.

## Testing the absence path

Every registered differ has a test that drives a failed read through a real
engine and checks that nothing changed. The canonical list, and why the
recovery poll matters as much as the failed one, is in
[the invariant's row](../reference/invariants.md#absence-of-evidence-is-never-a-state-change).
[`test-requirements.md`](../reference/test-requirements.md#a-new-differ)
says what a new differ's tests must cover.
