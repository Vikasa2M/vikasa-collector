# Architecture: the pipeline end to end

Read this first. It explains how the pieces fit together, so that when you
open `internal/synth/signal.go` or `internal/wire/openits/emitter.go` you
already know what calls it and what it hands off to. The other documents in
this tier each go deep on one part; [Reading on](#reading-on) lists them.

## A few terms

- **Device** — a field device in the cabinet: a signal controller (`asc`),
  a dynamic message sign (`dms`), and so on.
- **Adapter** — the code that talks to one vendor's device of one kind,
  such as `ntcip-asc`.
- **Facet** — one typed slice of a device's state, such as signal mode or
  the set of raised faults.
- **Snapshot** — everything one poll of one device read: a list of facets,
  plus a note of each facet the adapter failed to read.
- **Domain event** — a change the collector found by comparing two
  snapshots, such as "the mode changed from free to flash". Domain types
  live in `sdk/model`, which the collector owns.
- **ce-type** — the event's name in the openits-models catalog, such as
  `openits.signal-control.mode-changed.v1`. openits-models is a separate
  project that defines the wire schemas.
- **Subject** — the NATS address an event is published to.
- **JetStream** — the persistence layer of the NATS broker that runs in
  the cabinet beside the collector.

## The pipeline

```
device ──transport──▶ ADAPTER ──Snapshot──▶ RUNNER ──Snapshot──▶ SYNTH ENGINE
   ──domain events──▶ WIRE EMITTERS ──ce-type + payload──▶ PUBLISHER ──CloudEvent──▶ local JetStream
```

Each stage owns one kind of decision. A later stage sees only an earlier
stage's output, never how it was produced. `internal/app.Run` wires the
stages together at boot.

### 1. Adapters own the transport

Package: `internal/vendors/<vendor>/`, one file per device kind (for
example `internal/vendors/ntcip/asc.go`, not a `<kind>/` subdirectory).
Two adapters exist today: `ntcip-asc` and `ntcip-dms`.

An adapter's only obligation is to return `sdk/model` types. Whether the
data came over SNMP, REST, or anything else never reaches the core. The
core has no idea what a transport is. CI enforces that adapters and `sdk/`
never import openits-models; see
[the boundary rule](../reference/invariants.md#adapters-and-sdk-never-import-openits-models).
[`pluggability.md`](pluggability.md) covers the adapter contract and
[`adapter-to-model.md`](adapter-to-model.md) covers what a snapshot holds.

### 2. The runner polls one device

Package: `internal/runner/`. There is one `Runner` per configured device,
each on its own goroutine. It calls the adapter's `Read`, stamps the
snapshot with the device kind, and hands it to the synth engine. It also
turns a failed poll into a device-reachability event. The details are in
[the poll loop](#the-poll-loop-jitter-per-poll-timeout-and-panic-isolation)
below.

### 3. The synth engine diffs snapshots into domain events

Package: `internal/synth/`. There is one `Differ` per facet kind. On every
poll, `synth.Engine.Apply` (`internal/synth/synth.go`) compares each facet
just read against the last value it saw for that device and facet kind,
and returns domain events for whatever changed.

A facet that failed to read is never treated as a change. That rule has a
canonical statement in
[absence of evidence](../reference/invariants.md#absence-of-evidence-is-never-a-state-change).
Why the collector diffs at all, rather than publishing raw state for
consumers to diff, is [ADR 0016](../adr/0016-collector-as-transitional-shim.md).

### 4. Wire emitters map a domain event to a ce-type and a payload

Package: `internal/wire/`. This is the only layer allowed to import
openits-models. `internal/app` tries two emitters in order, and the first
to claim an event encodes it:

1. `internal/wire/openits` maps events onto the openits-models catalog
   (protobuf payloads). Its routing table, `ceTypeFor`, is keyed on the
   pair (event kind, device kind). `Encode` looks the pair up, then a
   `payload` function asks each service family's encoder in turn
   (`encode_signal_control.go`, `encode_dms.go`, and so on) to build the
   message.
2. `internal/wire/health` encodes the collector-owned schema
   ([ADR 0007](../adr/0007-collector-owned-health-schema.md)) as JSON.

An emitter declines (`ok == false`) any event it cannot encode faithfully.
An event that no emitter claims is dropped, logged, and counted.
[`wire-boundary.md`](wire-boundary.md) explains why the boundary sits here
and what the envelope carries.

### 5. The publisher queues and ships to JetStream

Package: `internal/publish/`. `internal/app` wraps each encoded payload in
a CloudEvents envelope (`internal/cloudevents`), then calls
`Publisher.Enqueue`. `Enqueue` renders the subject from the operator's
template (`internal/subject/`) and appends the message to a bounded
in-memory FIFO queue. It never blocks: when the queue is full, the oldest
event is evicted and counted.

One drainer goroutine publishes the queue in order. A transient failure is
retried with capped exponential backoff for as long as the broker is away.
A failure the broker will never accept is dropped and counted. This is
[ADR 0020](../adr/0020-broker-absence-is-latency.md): broker absence costs
latency, not data, until the queue fills.

The publisher also creates one stream per ce-type namespace. Each stream's
name is derived from its subject filter (`publish.StreamNameForBinding`),
so the two cannot disagree.

## Where state lives

The collector keeps very little state, and each piece has one home:

| State | Where | Survives a restart? |
|---|---|---|
| Last-known facet per device and kind | `synth.Engine`, in memory | Yes: written through to the JetStream key-value bucket `VIKASA_SYNTH_STATE` by `internal/durable` ([ADR 0017](../adr/0017-durable-synth-state.md)). Fast-changing counter facets (detector samples) are not persisted. |
| Events waiting to publish | the publisher's queue, in memory | No. Shutdown gets a bounded drain window; what is left is counted. |
| Per-device `sequence` counter on catalog events | the openits emitter, in memory | No. It restarts at 1, which the catalog allows. |
| The collector's own counters | `internal/stats.Counters` | No. A new `boot_id` tells consumers they restarted. |

At boot the engine is **held**: it diffs nothing for a device until
`internal/durable` has loaded that device's previous state from the bucket
and seeded the engine with it. Runners poll and report reachability during
the hold; only diffing waits. Without the hold, every restart would re-raise
every standing fault. If the broker is down at boot, the hold lasts until it
is back, because nothing could be published anyway
([ADR 0020](../adr/0020-broker-absence-is-latency.md), clause 2).

## Worked trace: one OID, start to finish

Follow one reading through every stage: the NTCIP operation-status OID,
`.1.3.6.1.4.1.1206.4.2.1.2.7.0`, on an `ntcip-asc` device.

**1. Adapter.** `asc.readSignalStatus` (`internal/vendors/ntcip/asc.go`)
looks up the OID in the poll's results:

```go
op, ok := vals[oidOperationStatus]
```

If the OID answered, it appends a `model.SignalStatus` facet with
`Mode: modeFromOperation(op)`. If it did not, the adapter does not guess a
mode. It records a `model.FacetError` for `KindSignalStatus` and leaves the
facet out of the snapshot.

**2. Runner.** `pollOnce` sets `snap.DeviceKind` to `asc` (from the
adapter's `Descriptor`) and calls `engine.Apply(snap)`.

**3. Synth engine.** `Apply` finds the `SignalStatus` facet and calls its
differ (`signalDiffer`, `internal/synth/signal.go`) with the device's
last-known `SignalStatus`. The differ always emits an
`OperationalStatusReport`. When a previous value exists and the mode
changed, it also emits `model.ModeChanged{From: ..., To: ...}`.

**4. Wire emitter.** The openits emitter's `Encode` looks up
`key{"mode-changed", "asc"}` in `ceTypeFor` and gets
`openits.signal-control.mode-changed.v1`. The signal-control encoder then
builds the protobuf payload. It declines the event instead if the mode has
no controller-mode identity in the catalog (standby, for example).

**5. Publisher.** `internal/app.encodeAndEnqueue` builds the envelope and
calls `Publisher.Enqueue` with the device ID. With the default seven-token
template, device `asc-main-and-5th`, and tenant `region=us-ga`,
`agency=metro-atlanta`, `agency_unit=d01`, the subject is:

```
openits.us-ga.metro-atlanta.d01.signal-control.asc-main-and-5th.mode-changed
```

The ce-type travels separately, in the CloudEvents `type` header of the same
message. The subject is operator-configurable; the ce-type is not. How the
two may relate is a rule with a canonical statement:
[subjects vs. envelope](../reference/invariants.md#subjects-are-operator-configurable-the-cloudevents-envelope-stays-canonical).

## The poll loop: jitter, per-poll timeout, and panic isolation

`internal/app.Run` builds one `Runner` per entry in `cfg.Devices` with
`runner.New(reader, runner.Config{...})` and runs each on its own
goroutine.

- **Jittered start.** Before the first poll, `Run` waits a random delay
  between zero and one poll interval (`runner.Config.Jitter`; tests pass a
  fixed one). Devices with the same interval do not all poll at once.
- **Per-poll timeout.** Each poll runs under its own
  `context.WithTimeout` (`runner.Config.Timeout`, defaulting to the poll
  interval). The runner does not trust the adapter to honour it: `read`
  runs `Read` on a separate goroutine and stops waiting when the timeout or
  shutdown arrives. A hung adapter is a failed poll, never a frozen loop or
  a stalled shutdown. The abandoned `Read` keeps running; its result is
  discarded as stale.
- **One read at a time per device.** While an abandoned `Read` is still
  running, later polls fail fast instead of calling `Read` again. An
  adapter is never asked to do two things at once. Concurrency exists
  across devices, not within one.
- **Panic isolation.** `readGuarded` recovers a panic in `Read`, and also
  catches a `Read` that returns neither a snapshot nor an error. Both are
  *adapter* faults: the device was never contacted. They are counted
  (`adapter_panics`) and logged, and the device's reachability is left
  unchanged. Reporting the device as down would publish a false fact, and
  one bad adapter release would look like a wave of dead devices
  ([ADR 0019](../adr/0019-collector-self-health.md), clause 12).

What happens after each poll:

- **Success** marks the device reachable (emitting a recovery event if it
  had been failing) and hands the snapshot to `engine.Apply`.
- **The first failure** in a row emits a `model.DeviceStatusChanged`
  (unreachable) event. Later failures emit nothing until the device recovers.
- **A poll cut short by the collector's own shutdown** is not reported at
  all. That is the absence-of-evidence rule applied to a whole poll.

Device reachability is routed like any other event. For `asc` the openits
emitter maps it to the catalog's
`openits.signal-control.comm-health-event.v1`. The catalog has no
comm-health channel for other device kinds yet, so for them the openits
emitter declines and the health emitter publishes
`openits-collector.health.device-status-changed.v1`.

## The collector's own events

Besides device events, the collector reports on itself, on its own subject
root ([ADR 0011](../adr/0011-namespace-rooted-subject-spaces.md),
[ADR 0019](../adr/0019-collector-self-health.md)):

```
openits-collector.<region>.<agency>.<agency_unit>.instance.<site>.<event>
```

The events are `started`, `heartbeat`, `log`, and `stopping`, all encoded
by `internal/wire/health`. The heartbeat (`internal/app/heartbeat.go`)
fires once per `heartbeat_interval`. It carries uptime, resource use, and a
`stats.Snapshot` of every counter. `internal/logship` wraps the logger so
that warn-and-above records are also shipped as `log` events, rate-limited
per heartbeat interval. Records over the limit are counted in
`logs_suppressed`.

## Around the pipeline

- **Counters** — `internal/stats` holds every counter in one place: events
  published, each kind of drop, encode errors, adapter panics, and more.
  `/status` and the heartbeat both read the same `Counters.Snapshot`, so
  they cannot disagree. There is no Prometheus or OpenTelemetry endpoint.
- **Management surface** — `internal/manage` serves three read-only HTTP
  endpoints on loopback: `/livez` (is the process responsive), `/readyz`
  (has at least one device poll succeeded; it latches and does not depend
  on the broker), and `/status` (a JSON summary for a technician).
- **Broker security** — the broker credential comes only from a `.creds`
  file (`-nats-creds`), never the URL. A broker that is not on loopback
  must use `tls://`, verified against `-nats-ca` or the system roots. A
  test proves the documented least-privilege grant is enough
  ([ADR 0022](../adr/0022-transport-security-and-credentials.md)).
- **Config** — `internal/config` validates `collector.yaml` at boot and
  refuses anything it does not recognize; see
  [the config rule](../reference/invariants.md#config-is-the-trust-boundary-boot-fails-on-the-unrecognized).

## Reading on

- [`adapter-to-model.md`](adapter-to-model.md) — snapshots, facets, and how
  the synth engine turns them into domain events.
- [`pluggability.md`](pluggability.md) — the `sdk/adapter` contract, and
  why adapters compile in and are pull-only.
- [`wire-boundary.md`](wire-boundary.md) — why the domain model is not the
  wire model, what the CloudEvents envelope carries, and when an event is
  dropped.
- [`testing-strategy.md`](testing-strategy.md) — what each kind of test
  proves, and what it does not.
