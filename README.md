# vikasa-collector

An open-source edge collector for ITS (intelligent transportation system)
cabinets. It runs on a small Linux host inside the cabinet, polls the
devices there (signal controllers, message signs, …) through vendor
adapters, turns what changed into domain events, and publishes them as
CloudEvents to the cabinet's local NATS JetStream. Event payloads use the
[openits-models](https://github.com/Vikasa2M/openits-models) catalog.

## How it works

```
[device] ─transport─▶ ADAPTER ─sdk/model─▶ CORE ─wire emitter─▶ CloudEvent ─▶ local JetStream
```

- **Adapters** (`internal/vendors/<vendor>/`) talk to one kind of device.
  Each owns its transport (SNMP today) and returns only `sdk/model` types.
- **The core** compares each poll with the previous one and turns the
  differences into domain events. It also tracks whether each device is
  reachable.
- **Wire emitters** (`internal/wire/`) encode domain events as openits-models
  payloads. They are the only code that imports openits-models — see [the
  boundary
  rule](docs/reference/invariants.md#adapters-and-sdk-never-import-openits-models).
- **The publisher** sends each event on an operator-configurable subject
  (ADR 0009). By default that is the OpenITS profile's seven-token grammar,
  rooted on the event type's namespace:
  `openits.<region>.<agency>.<agency_unit>.<service>.<device_id>.<event>`
  for catalog events, and `openits-collector.…` for the collector's own
  health. The two roots go to separate streams, so a deployment can give
  them different retention and different readers (ADR 0011).

[`docs/explanation/architecture.md`](docs/explanation/architecture.md) walks
the pipeline end to end. The decisions behind it are in
[`docs/adr/`](docs/adr/README.md).

## Status

Pre-1.0. The core is complete: ten facet kinds (typed slices of device
state) with a differ each, and 29 catalog event types plus five
collector-owned ones, each pinned by a byte-exact golden test. Events are
published in the NATS reference profile's Tier 2 shape (binary-mode
CloudEvents, deterministic ULID `ce-id`, seven-token subjects), and a
conformance test runs openits-models' own Tier 2 checks against them.

The collector is built to run unattended in a cabinet:

- it starts without its broker and queues events until the broker appears;
- it keeps device state across restarts (in a JetStream key-value bucket),
  so nothing already standing is announced again;
- it bounds every shutdown;
- it serves `/livez`, `/readyz` and `/status` on a loopback port;
- it reports its own heartbeat and warnings on the bus.

Release builds target linux `amd64` and `arm64` and ship with a hardened
systemd unit. No release has been published yet; until one is, build the
same archives with `make release-snapshot`. See [deploying a
collector](docs/how-to/deploy-a-collector.md).

What is missing is **adapters**. Three exist — `ntcip-asc` (signal
controller), `ntcip-dms` (message sign), and `cameleon-acs` (reversible-lane
gates over GE SRTP). Camera, traffic-sensor and perception devices are
modeled, diffed and wired, but nothing reads them yet — see
[starter tasks](docs/reference/starter-tasks.md). The shipped
`collector.yaml` includes an ACS device next to the signal controller.

Some events are dropped with a warning by design. The emitter declines an
event it cannot encode faithfully (for example, a controller mode the
catalog has no identity for) rather than publish a near match. A visible
drop beats a wrong value on the bus.

## Quick start

Requires Go (the version in `go.mod`). No hardware or broker needed:

```bash
make dev      # the real pipeline against an embedded JetStream and a synthetic device
make check    # the CI gate: vet, golangci-lint, tidy, race tests, boundary and docs lints
```

`make dev` prints every CloudEvent that reaches the bus, so you can watch
the collector work before you have a cabinet. Its synthetic device is not
a device simulator, and its output is not a fixture source — see
`cmd/dev`'s package comment.

To run against a real cabinet, you need a NATS server with JetStream and
devices reachable at the addresses in your config:

```bash
go run ./cmd/collector -config collector.yaml   # -nats defaults to nats://127.0.0.1:4222
```

[`collector.yaml`](collector.yaml) is a commented example.
[`docs/reference/configuration.md`](docs/reference/configuration.md)
documents every field and flag, including broker credentials and TLS.

## Contributing

New to the repo? [Build your first
adapter](docs/tutorial/build-your-first-adapter.md) takes a fresh clone to a
real event on the bus in one sitting. [CONTRIBUTING.md](CONTRIBUTING.md)
covers the rest, and the [documentation hub](docs/README.md) routes every
other task.

## License

Apache License 2.0 — see [LICENSE](LICENSE) and [NOTICE](NOTICE).
Report security issues as described in [SECURITY.md](SECURITY.md).
