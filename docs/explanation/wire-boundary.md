# The wire boundary: why the domain model isn't the wire model

[`architecture.md`](architecture.md) names the wire emitters as the stage
that turns a domain event into a ce-type and a payload, or declines it. This
document goes deeper on two things. First, why the boundary sits exactly
between `sdk/model` and `internal/wire`. Second, what rides in the
CloudEvents envelope once an emitter has produced a payload. Read it before
writing or reviewing a mapping in `internal/wire/openits/`, or when you want
to know why a `ce-id` looks the way it does.

## Why the domain model is not the wire model

The domain model (`sdk/model`) is a vocabulary this project owns.
openits-models is a vocabulary another project owns and releases as a
versioned catalog. [ADR 0002](../adr/0002-domain-model-and-wire-emitter-boundary.md)
keeps the two apart. If every adapter built openits-models types directly,
any change to the catalog (a renamed field, a renumbered proto tag) would be
a change every adapter author had to make at once. Contributed adapters come
from people who do not control the catalog's release cadence, so that cost
cannot be absorbed.

So the domain model sits upstream of the catalog. Adapters return
`sdk/model` facets; **exactly one layer, `internal/wire`, imports
openits-models and turns a domain event into a `(ce-type, payload)`
pair.** CI enforces the import rule; see
[the boundary rule](../reference/invariants.md#adapters-and-sdk-never-import-openits-models).

The payoff is that a catalog change is a one-package edit, and the domain
model may be richer than any one wire version. Where the two disagree, the
mapping layer makes an explicit map-or-drop decision (see
[the drop rule](#the-drop-rule-decline-rather-than-approximate)) instead of
blocking collection upstream.

### One catalog version per build

`internal/wire/openits` is the only catalog emitter, and its name carries no
version. Exactly one openits-models version is compiled in at a time. The
pin in `go.mod` names a release tag (v0.7.0 today), never a `replace`
directive ([ADR 0018](../adr/0018-tagged-model-pins.md)), and every
deployment runs one catalog version
([ADR 0005](../adr/0005-one-catalog-version-per-instance.md)), selected by
`model_version` in `collector.yaml`.

A package per release (`internal/wire/openits_v1`, `openits_v2`, …, with
config picking one at boot) would let one binary serve fleets on different
catalog versions. It has not started, and it is **not** tied to the pin
being tagged. [ADR 0010](../adr/0010-openits-models-lockstep-pre-v1.md)
once coupled the two; ADR 0018 separated them, because how the collector
*adopts* a release and how many releases it *compiles at once* are
different questions. The split begins when a fleet genuinely needs two
releases in one binary.

## What the envelope carries

The publisher sends every event as a CloudEvent in NATS binary mode: each
`ce-*` attribute is a message header, and the body is the raw encoded
payload (`publish.Headers`). Four attributes are worth understanding one by
one, because each is fixed by a different rule.

### `ce-type`: catalog-verbatim, fixed

`ce-type` is schema identity. The openits emitter looks it up in its
routing table, `ceTypeFor` (`internal/wire/openits/emitter.go`), keyed on
the pair (domain event kind, device kind). For example,
`{"mode-changed", "asc"}` gives `openits.signal-control.mode-changed.v1`.
The key must be a pair because the catalog defines shared events once and
reuses them across services: the same domain fault becomes
`openits.signal-control.fault-raised.v1` on a controller and
`openits.dms.fault-raised.v1` on a sign.

The string is the catalog's own, unmodified. Nothing operator-configured
touches it; it is the same whatever subject the operator routes the event
to.

### `ce-source`: the profile URN, and why it feeds `ce-id`

`ce-source` is the URN that [ADR 0015](../adr/0015-ce-source-urn-scheme.md)
defines:

```
urn:openits:<entity-kind>:<region>:<agency>:<agency_unit>:<id>
```

`SourceFor` (`internal/cloudevents/subject.go`) builds it. `entity-kind` is
not the collector's device kind. `entityKindFor` maps it onto the profile's
vocabulary: `asc` → `controller`, `dms` → `sign`, `cctv` → `cctv`,
`traffic-sensor` → `traffic-sensor`, `perception` → `perception`. The
collector's own instance events (started, heartbeat, log, stopping) have
no device; they map to `collector`, with the tenant's `site` as the id.

A device kind with no mapping (`rsu`, for example, which the collector does
not model yet) makes `SourceFor` return `""`. `internal/app` then drops
the event rather than invent a URN segment upstream never defined.

The exact shape matters beyond routing: **the literal `ce-source` bytes are
the first field hashed into `ce-id`.** Change the token order, the
entity-kind vocabulary, or the device-less fallback, and every event id
changes. That is why ADR 0015 exists: the format had drifted from every ADR
that claimed to describe it, and hashing it turns such drift into a silent
id break.

### `ce-id`: a deterministic ULID, not a content hash

"Deterministic" is easy to misread as "a hash of the payload", and that is
wrong. `EventID` (`internal/cloudevents/eventid.go`) computes:

```
digest = SHA-256( source ‖ ceType ‖ stableMillis(stableTime) ‖ identity )
id     = ULID(timestamp = stableTime-ms, randomness = digest[0:10])
```

The fields, in the order they are hashed:

1. **`source`** — the literal `ce-source` URN.
2. **`ceType`** — the literal `ce-type` string.
3. **`stableMillis(stableTime)`** — `stableTime` as RFC 3339 in UTC with
   exactly three fractional digits (`2006-01-02T15:04:05.000Z`). Fixed
   precision matters: the same instant written with a different number of
   digits would hash differently. `stableTime` is the event's occurred-at:
   the device's own clock if it reports one, otherwise the observing poll's
   `Snapshot.SampledAt`, which covers most events this collector emits.
   Today `cloudevents.New` sets `ce-time` from the same value, so the two
   coincide, but the digest is defined on occurred-at, not on `ce-time`.
4. **`identity`** — the encoded payload with producer-assigned fields
   (`sequence`, `observed-by`) cleared, so the id describes the occurrence,
   not the act of observing it. Only `internal/wire` knows the proto shape,
   so the emitter does the clearing (`wire.Encoded.Identity`) and `EventID`
   just takes bytes.

A single `0x1f` byte separates adjacent fields: three separators, none
leading or trailing. Without it, `("ab", "c")` and `("a", "bc")` would hash
the same.

The ULID's first 48 bits are `stableTime` in milliseconds, passed in as an
argument, never read from a clock. The last 80 bits are the first 10 bytes
of the digest. The 128 bits are rendered as 26 Crockford base32 characters
(no `I`, `L`, `O`, or `U`, so an id survives being read aloud). Because
every input is an argument, two collectors observing the same occurrence,
or one collector before and after a restart, produce the same id without
coordinating. JetStream's duplicate detection keys on it
(`Nats-Msg-Id` carries the same value), so retries are safe.

This is checked against an external contract, not just against itself.
openits-models' `ce-id-spec.md` names this collector as its reference
implementation and publishes test vectors, which `EventID` reproduces byte
for byte (`internal/cloudevents/eventid_test.go`). `asyncapi.yaml`
documents the same formula, and `internal/docs/asyncapi_test.go` asserts
that wording, field order included. An earlier version hashed
`(type, source, …)`, which gave every event a different id.

### `ce-dataschema`: pins the defining module

Set only on `openits.*` events, from a hard-coded table (`dataSchemaFor` in
`internal/wire/openits/identities.go`). Each entry names the module that
*defines* the event, at that module's own revision, never a base module the
payload happens to include. Health events omit the attribute rather than
point at a registry they are not part of
([ADR 0007](../adr/0007-collector-owned-health-schema.md)).

The table is hard-coded because openits-models ships no Go API to read it
from at runtime. The trade is deliberate: a pin bump shows up as a
reviewable diff of these constants, and the goldens lock them.

## The drop rule: decline rather than approximate

The emitter's mapping functions mostly return `(value, ok bool)`. When a
domain value has no honest wire rendering, `ok` is `false` and the event is
declined, rather than encoded against the nearest identity.
`controllerModeIdentity` is the clearest example. The catalog's controller
modes are coordinated, free, flash, preempt, priority, manual, and off.
`ModeStandby` and `ModeUnknown` have no match, so a `ModeChanged` carrying
either is declined. `dmsControlModeIdentity`, `cctvControlModeIdentity`,
and `tourRunStateFor` decline for the same reason: the wire enum has no
honest slot for what was observed. A wrong value on the bus is worse than
none, because a consumer cannot tell an invented state from a real one.

The rule is "decline when there is no honest statement to make", not
"decline whenever the domain is more specific than the wire". Where a
vaguer but still true statement exists, the emitter uses it.
`faultKindIdentity` falls back to a service's base fault identity
(`sc-fault-event-kind` and so on) for a fault category with no specific
leaf, because "a fault occurred, class unmapped" is still true and still
worth an operator seeing. A mode has no such fallback, because the event
must assert one specific current state. Each mapping's comment says which
behaviour it has and why.

Declining is not always the end of the road. `internal/app` tries emitters
in order, openits first and then health, and the first to claim an event
wins. Device reachability shows this: the catalog has a comm-health
channel only for signal control, so for an `asc` the openits emitter
claims `DeviceStatusChanged` as `openits.signal-control.comm-health-event.v1`.
For every other kind there is no `ceTypeFor` entry, the openits emitter
declines, and the health emitter publishes
`openits-collector.health.device-status-changed.v1`
([ADR 0019](../adr/0019-collector-self-health.md)).

An event is dropped in `encodeAndEnqueue` (`internal/app/app.go`) in two
cases:

- **No emitter claims it.** There is no `ceTypeFor` entry for the pair, no
  family encoder in `payload` builds it, or an identity mapping declined
  it, and the health emitter does not handle it either. Logged as
  `"event dropped: no emitter for domain event"` and counted in
  `dropped_no_emitter`.
- **No `ce-source` entity kind.** The payload may encode cleanly, but
  `SourceFor` returns `""`. Logged as
  `"event dropped: no ce-source entity kind for device kind"` and counted
  in `dropped_no_source`.

Both counters live in `internal/stats`, which is the collector's whole
metrics surface ([ADR 0020](../adr/0020-broker-absence-is-latency.md)):
no Prometheus or OpenTelemetry. The counters are served on the local
`/status` endpoint and carried off the box in every heartbeat event, so a
fleet operator can alert on a drop rate instead of reading a cabinet's
logs.
