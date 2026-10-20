# Consumer contract

What a consumer reading events off the cabinet bus can and cannot rely on.

Everything below is a property of **this producer**, not of the catalog.
The schema of every `openits.*` event belongs to openits-models and is
documented in that repo's `bindings/nats/asyncapi.yaml`. The collector's
own events — `openits-collector.instance.*` (its own health) and
`openits-collector.health.*` (device reachability where the catalog has no
channel for it) — are documented in [`asyncapi.yaml`](../../asyncapi.yaml)
at the root of this repo. Neither file says anything about redelivery,
ordering or loss. This page does.

It is written for the person on the other end of the bus. If you are
adding an adapter or a mapping,
[`explanation/wire-boundary.md`](../explanation/wire-boundary.md) covers
the same envelope from the producing side, in more depth.

## Match on `ce-type`, never on the subject

`ce-type` is the schema identity: the catalog's own string, unmodified. The
**subject is not**. Subject grammar is operator-configurable
([ADR 0009](../adr/0009-configurable-subject-templates.md),
[ADR 0011](../adr/0011-namespace-rooted-subject-spaces.md)), so the same
ce-type lands on different subjects in different deployments. The default
grammar is seven tokens —
`{namespace}.{region}.{agency}.{agency_unit}.{service}.{device_id}.{event}`
— but a deployment with its own template publishes the same events
somewhere else entirely.

Subscribe on whatever subject pattern the deployment uses, and
**discriminate on the `ce-type` header**. A consumer that parses meaning
out of subject tokens is coupled to one operator's routing choices.

There are two subject roots, each captured by its own JetStream stream:
`openits.…` for catalog telemetry and `openits-collector.…` for the
collector's own events. They are separate so each can be given its own
retention and access control; the collector provisions both the same way
(file storage, a two-minute duplicate window) and leaves retention to the
broker's operator.

## Delivery: neither at-least-once nor exactly-once

This is the part most likely to be assumed rather than read. The collector
makes a **bounded** effort to deliver, and events can be lost in the ways
below. The first four are logged and counted in the collector's cumulative
counters (`internal/stats`), which are served on the local
[`/status` endpoint](configuration.md#the-management-surface) and carried
on every `instance.heartbeat`. The last cannot be: a collector without its
stored state does not know what it missed.

1. **The publisher cannot hold it.** A broker that is down or restarting
   costs latency, not events: the publisher (`internal/publish`) queues
   events in memory and retries with backoff until the broker is back
   ([ADR 0020](../adr/0020-broker-absence-is-latency.md)). But the queue is
   bounded (10,000 events), so three cases still lose an event:
   - the queue is full, and the **oldest** queued event is evicted to make
     room — during a long outage you keep the most recent events and lose
     the earliest (`dropped_queue_full`);
   - the broker will never accept it — for example, it exceeds the
     broker's maximum payload — so it is dropped at once rather than
     blocking the queue (`dropped_unpublishable`);
   - it is still queued when shutdown's drain window closes
     (`dropped_at_shutdown`).
2. **No emitter claims the event.** Either the domain event has no ce-type
   mapping, or the emitter declined it because the value has no honest
   wire identity — it refuses rather than substituting a near neighbour
   ([the drop rule](../explanation/wire-boundary.md#the-drop-rule-decline-rather-than-approximate)).
   Both are logged as `event dropped: no emitter for domain event` and
   counted as `dropped_no_emitter`. A visible drop beats a wrong value on
   the bus.
3. **The device kind has no `ce-source` entity kind.** Publishing would
   mean inventing a URN shape the profile does not define, so the event is
   dropped (`dropped_no_source`). Every device kind this build's adapters
   serve has one.
4. **Encoding failed** (`encode_errors`).
5. **The collector restarted without its stored state.** A restart
   normally recovers what changed while it was down (see
   [Restart behaviour](#restart-behaviour)), but not after a first boot, a
   reflash or a lost write, and never for detector counts.

**The consequence to design around: the absence of an event is not
evidence that nothing happened.** If your logic needs a device's current
state, keep your own materialized view and treat staleness as unknown,
rather than inferring "no event, therefore unchanged."

## Duplicates: expect them, and dedupe on `(device, fault-id)`

The collector sets `Nats-Msg-Id` to the event's `ce-id`, and provisions
each stream with a **two-minute** JetStream duplicate window (`dedupWindow`
in `internal/publish/publish.go`). So a retransmission of the *same
observation* within two minutes is suppressed by the broker.

That covers less than it sounds like, and the gap is the important part.

`ce-id` is a deterministic ULID derived from
`(ce-source, ce-type, occurred-at, payload-identity)`
([the full derivation](../explanation/wire-boundary.md#ce-id-a-deterministic-ulid-not-a-content-hash)).
Two collectors observing the same occurrence produce the same id, and so
does one collector re-encoding the same observation. But a *different
observation of the same underlying state* has a different `occurred-at`,
and therefore a different id — correctly, because it is a different
observation.

**So a fault re-raised after a restart cannot be deduped by id**, and the
requirement below is part of the contract rather than something the broker
can do for you.

> **Required:** treat `fault-raised` as idempotent per
> `(source-device-id, fault-id)`. Receiving a raise for a fault you already
> consider raised is a no-op, not a new incident.

The collector usually remembers standing faults across a restart (next
section), but not always: no local state survives a reflash, a replaced
storage device or a first boot, and after any of those it re-raises every
standing fault — correctly, because it genuinely does not know.

## Device reachability

A device becoming unreachable, or coming back, is published on the
**catalog** root as `openits.<service>.comm-health-event.v1`, with `kind`
`openits-common-comm-health-events:comm-lost` or `…:comm-restored`,
wherever the pinned openits-models release gives that service the channel
— signal-control today. For a device kind whose service does not have it
yet (DMS today), the same transition goes out as the collector-owned
`openits-collector.health.device-status-changed.v1` instead, so it is
never lost. That fallback shrinks as upstream adds the channel
([ADR 0019](../adr/0019-collector-self-health.md), as amended).

Both report transitions only: the first failed read after a success, and
the first success after a failure. An adapter crash or contract violation
(a panic, or a nil snapshot with no error) is **not** a device fault and
produces neither — the device may never have been contacted. It is counted
as `adapter_panics` on the heartbeat instead.

## Restart behaviour

**The collector remembers its previous state across a restart**
([ADR 0017](../adr/0017-durable-synth-state.md)). Every facet the engine
diffs against is written through to a key-value bucket,
`VIKASA_SYNTH_STATE`, on the cabinet's own JetStream, and a restarted
collector loads it before it diffs anything (`internal/durable`). So:

- **A standing fault is not re-raised.** It was known before the restart
  and is known after it.
- **A transition that happened while the collector was down is emitted
  once,** on the first diff after the restart, with that poll's time as
  `occurred-at` — the collector cannot know when, inside the gap, it
  actually happened.
- **Until the state has loaded, the collector reports device reachability
  and its own `instance.*` events, but no other device events**
  ([ADR 0020](../adr/0020-broker-absence-is-latency.md)). If the broker is
  not up at boot, that hold lasts until it is; there is no fallback to
  diffing from empty.

What it cannot cover, and why the idempotency requirement above stays:

- **No stored state** — the first boot, a reflash, a replaced storage
  device, or a deleted bucket. The collector starts from first
  observations, and every standing fault is raised.
- **State not yet written when the process died.** Writes are asynchronous
  and coalesced. A crash (not a clean shutdown, which flushes) can lose the
  last change, so the next boot may report it again.
- **Detector counts.** They change on every poll, so they are deliberately
  not stored (that would be a flash write per poll). The first poll after a
  restart is a first observation and emits no detector report, so one
  interval's volume is missing.

## Ordering

**Per device, in order. Across devices, no order at all.**

Each configured device has one runner goroutine polling it in a sequential
loop (`internal/runner`). Each poll's events are encoded and queued, in
order, on that goroutine, and a single publisher goroutine publishes the
queue first-in, first-out. So two events about the same device arrive in
the order the collector observed them.

Nothing orders events across devices, and nothing should be inferred from
their relative arrival: devices poll on independent, jittered timers.

Catalog payloads carry `sequence`, a **per-device** counter that starts at
1 and **resets when the collector restarts** (`nextSequence` in
`internal/wire/openits/emitter.go`), and `sequence-epoch`, which names the
stream the counter belongs to. The collector uses its boot id (the same
`boot_id` as `/status` and the heartbeat), so every restart is a new epoch:
treat a new epoch as a new stream, not as loss. Within one epoch `sequence`
is not a global log position, and a gap in it is not proof of loss: an
event dropped after encoding still consumed a number. Use it to order within
one device's events, not to detect loss. Neither leaf feeds `ce-id`, so a
restart never changes an event's id.

## Provenance: who observed this

Every catalog payload carries `observed-by`, set from the collector's
configured `collector_id`. That field distinguishes *"the device reported
this"* from *"a poller inferred it by comparing consecutive reads."* The
collector is a
[transitional shim](../adr/0016-collector-as-transitional-shim.md), and
nearly everything it publishes is the second kind: mode, plan, fault and
incident transitions are all synthesized by comparing two snapshots.

`ce-source` is the profile URN
([ADR 0015](../adr/0015-ce-source-urn-scheme.md)):

```
urn:openits:<entity-kind>:<region>:<agency>:<agency_unit>:<id>
```

`<entity-kind>` is the profile's vocabulary, not the collector's device
kind: an `asc` device appears as `controller`, a `dms` as `sign`. The
collector's own `openits-collector.instance.*` events have no device, so
their entity kind is `collector` and the id is the deployment's configured
`site`.

Because `ce-source`'s literal bytes are hashed into `ce-id`, changing
`region`, `agency` or `agency_unit` — or `site`, for the collector's own
events — changes the id of every event published afterwards. See
[the configuration reference](configuration.md#region--agency--agency_unit--site).

## Envelope summary

CloudEvents 1.0 in NATS binary mode: `ce-*` attributes travel as message
headers and the body is the raw payload. For `openits.*` events the body is
protobuf, not JSON — the JSON Schema in openits-models' AsyncAPI describes
that protobuf's *structure* for tooling and is not the wire encoding. For
`openits-collector.*` events the body is JSON
([ADR 0007](../adr/0007-collector-owned-health-schema.md)).

| Header | Present | Notes |
|---|---|---|
| `ce-specversion` | always | `1.0` |
| `ce-id` | always | Deterministic ULID (26 Crockford base32 characters). See [Duplicates](#duplicates-expect-them-and-dedupe-on-device-fault-id) for what it does and does not dedupe. |
| `ce-source` | always | The profile URN above. |
| `ce-type` | always | Catalog-verbatim. **This is what to match on.** |
| `ce-time` | always | RFC 3339 in UTC. The same `occurred-at` the id is derived from. |
| `ce-datacontenttype` | always | `application/protobuf` for `openits.*`; `application/json` for `openits-collector.*`. |
| `ce-dataschema` | `openits.*` only | The immutable schema-registry revision the body validates against. The collector's own events omit it rather than point at a registry they were never part of. |
| `Nats-Msg-Id` | always | Equal to `ce-id`; drives JetStream's duplicate window. |

## What the collector does not offer

The bus is publish-only: there is no query interface, and nothing answers
"what does the collector currently think" over NATS — build a materialized
view from the event stream instead. The only pull interface is the
[management surface](configuration.md#the-management-surface), which is
loopback-only and meant for the host's supervisor and an on-site
technician, not for consumers. The collector exposes no Prometheus-style
metrics endpoint; its counters reach the fleet on the heartbeat.
