# Starter tasks

Two tracks, and one question decides which you are on: **do you have the
device?**

An adapter encodes what one specific device actually returns — OID quirks,
vendor deviations from the standard, firmware differences — and none of
that is knowable from a specification. So adapter work needs hardware
access from the start, not as a formality at the end. That is the first
track below.

If you have no device, [the second track](#starter-tasks-that-need-no-hardware)
is real work, not a consolation prize: each item is an open gap this repo
already tracks against itself.

## The safest first PR — if you have the device

Nine facet kinds are modelled in `sdk/model`, each diffed by a registered
differ in `internal/synth` and mapped to ce-types in
`internal/wire/openits/emitter.go`, with goldens. (A *facet* is one slice
of a device's state that an adapter reads and a differ compares poll to
poll; see [`explanation/adapter-to-model.md`](../explanation/adapter-to-model.md).)

Two adapters exist, both in `internal/vendors/ntcip/`, and between them
they produce five facets:

| Adapter | Facets it produces |
|---|---|
| `ntcip-asc` (signal controller) | `signal-status`, `fault-set`, `detector-samples` |
| `ntcip-dms` (message sign) | `dms-status`, `dms-environment` |

The other four — `cctv-status`, `traffic-intervals`, `zone-incidents` and
`zone-intervals` — are wired end to end, and nothing produces them.

That makes a first adapter for one of those four the safest way into this
codebase **for someone with the hardware**. The facet, differ and wire
mapping are already built and tested, so the only new code is the part
that talks to your device, in `internal/vendors/<vendor>/` alone: no
`sdk/model`, `internal/synth` or `internal/wire` change. You learn one
thing at a time — how an adapter reads a device and returns `sdk/model`
types — without also learning the layering rules that are hardest to get
right on a first PR. Ship it, and events start flowing through parts of
the pipeline no real device has exercised yet.

## The four open facets

| Facet kind | Differ | Ce-types it lights up | What a device must provide |
|---|---|---|---|
| `cctv-status` | `synth.NewCCTVDiffer` (`internal/synth/cctv.go`) | `openits.cctv.mode-changed.v1`, `openits.cctv.tour-state-changed.v1` | `model.CCTVStatus` (`sdk/model/cctv.go`): who is driving the camera (central/local/central-override/other) and the run state of each configured preset tour (stopped/running/paused). A camera with no tours yields an empty `Tours` slice, not an error. |
| `traffic-intervals` | `synth.NewTrafficIntervalDiffer` (`internal/synth/trafficsensor.go`) | `openits.traffic-sensor.traffic-interval-report.v1` | `model.TrafficIntervals` (`sdk/model/trafficsensor.go`): the sensor's own completed-interval start time and duration (not poll timing — the differ uses `IntervalStart` to tell a fresh interval from a re-read), plus per-lane volume, occupancy, optional speed, and optional per-class-bin volumes. |
| `zone-incidents` | `synth.NewZoneIncidentDiffer` (`internal/synth/perception.go`) | `openits.perception.zone-incident-detected.v1`, `.zone-incident-updated.v1`, `.zone-incident-cleared.v1` | `model.ZoneIncidents` (`sdk/model/perception.go`): every incident currently active in the sensor's detection zones, each keyed by the sensor's own stable `IncidentID` (it must persist across polls, or every poll looks like clear-and-redetect), with type, severity, object class, and optional speed. |
| `zone-intervals` | `synth.NewZoneIntervalDiffer` (`internal/synth/perception.go`) | `openits.perception.zone-interval-report.v1`, `openits.zone-occupancy.zone-occupancy-interval-report.v1` | `model.ZoneIntervals` (`sdk/model/perception.go`): device-reported interval start and duration, plus per-zone crossed volume, observed count, occupancy, optional speed, and optional per-object-class counts. One interval publishes to both ce-types. |

`zone-incidents` and `zone-intervals` share one device kind, `perception`:
a device that reports both lights up both rows from a single adapter.

**Optional, and a good second PR:** each of these device kinds also has
`openits.<service>.fault-raised.v1` and `.fault-cleared.v1` mapped (see
`ceTypeFor` in `internal/wire/openits/emitter.go`). Producing them needs
your adapter to also return the existing `fault-set` facet
(`model.FaultSet`, diffed by `synth.NewFaultDiffer`). The same is true of
`ntcip-dms`, which does not read faults yet — a task for anyone with an
NTCIP 1203 sign.

**Say what you are taking on before you build it.** Open an
[adapter proposal](https://github.com/Vikasa2M/vikasa-collector/issues/new?template=adapter_proposal.yml)
first. It stops two people writing the same adapter, lets a maintainer
flag a domain-model gap while that is still a conversation rather than a
rewrite, and asks up front what hardware you will develop against — a real
prerequisite, because the fixture bar cannot be met without the device.

Before writing an adapter, read [the adapter
checklist](test-requirements.md#a-new-adapter). The step-by-step workflow —
reader contract, registration, connection parsing, fixtures — is
[`how-to/add-a-vendor-adapter.md`](../how-to/add-a-vendor-adapter.md);
`.claude/skills/add-vendor-adapter/` is the same workflow as a terse
checklist. If you have never seen this repo before,
[`tutorial/build-your-first-adapter.md`](../tutorial/build-your-first-adapter.md)
walks a throwaway adapter end to end first.

## Starter tasks that need no hardware

`make check` is the whole feedback loop for each of these.

- **Map one of the three owed ce-types.** The `unmapped` allowlist in
  `internal/wire/openits/catalog_test.go` marks three catalog ce-types
  `owedServiceAlreadyWired`: `traffic-sensor-status-report`,
  `queue-state-changed` and `zone-occupancy-changed`, in services the
  collector already serves end to end. Each needs a domain event, a
  `ceTypeFor` entry, an encoder case and a golden, and its `unmapped` line
  deleted — follow
  [`map-an-event-to-the-wire.md`](../how-to/map-an-event-to-the-wire.md).
  Start with `traffic-sensor-status-report`.
- **Assess one signal-control ce-type.** Eleven signal-control ce-types in
  the same allowlist are marked `signalControlNotAssessed`. Most are
  per-cycle events (phase, overlap, pedestrian, detector transitions) that
  an SNMP poller cannot observe, but each needs its own verdict: either it
  can be derived from what `ntcip-asc` already polls, and moves to
  `owedServiceAlreadyWired`, or the allowlist records why polling cannot
  see it. This is reading NTCIP 1202 and the catalog, not writing much
  code.

## The reference implementation

`internal/vendors/ntcip/asc.go` is the adapter to read before writing your
own — not for its fixtures, which do not meet the bar (see
[the adapter checklist](test-requirements.md#a-new-adapter)), but for three
things its `Read` shows cleanly:

1. **Per-facet failure isolation.** `Read` calls `readSignalStatus`,
   `readFaultSet` and `readDetectors` independently; as its comment puts
   it, "Each facet is an independent failure domain: one unanswered OID
   must never suppress a facet that WAS readable." One facet's OID going
   unanswered produces a `model.FacetError` for that facet alone, and the
   others still populate.
2. **The absence-of-evidence rule in practice.** `readFaultSet` and
   `readSignalStatus` distinguish "the device told us nothing" (append a
   `FacetError`, leave prior state alone) from "the device told us zero"
   (a real value — zero alarm bits is a healthy controller, not a read
   failure). `readDetectors` applies the same distinction to a controller
   that answers "I have zero detectors": an empty facet, not an error.
3. **Reading a table in one `Get`.** Rather than walking the detector
   table one OID at a time (about 510 round trips for a full 255-channel
   table), `readDetectors` builds the list of indexed OIDs up front and
   issues a single `Get`, which the SNMP transport sends in batches of 16
   — about 32 round trips.

`internal/vendors/ntcip/dms.go` is the second example, and the one to copy
for fixtures: its test file records which controller and firmware the
values were transcribed from, and when.

Lay a new adapter out the way `ntcip` is:
`internal/vendors/<vendor>/<kind>.go` and `<kind>_test.go` beside a shared
`register.go` — one file per device kind, not a subdirectory.
