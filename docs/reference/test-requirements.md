# Test requirements

This is the checklist a PR is measured against. It states the bar, per
contribution type — not a report on where the current code stands against
it. Where the repo itself falls short of a row below, that is a tracked
gap, not a second, looser standard you may copy.

Rule *statements* (what the rule is, why it exists, what enforces it today)
live in [`docs/reference/invariants.md`](invariants.md) — this document
only says what a PR must test, and links there for the rule itself rather
than restating it.

## Any change

- `make check` passes: vet, golangci-lint (gofmt included), tidy and
  checksum verification, the test suite under the race detector, and the
  boundary and docs lints. Poll loops are concurrent; a race here is a real
  bug, not flakiness.
- **A new guard ships with the test that trips it.** A new boot-time
  validation, lint rule or conformance check needs a test that feeds it a
  violation and asserts it fails, for the specific reason it exists — see
  [every guard must be shown to
  fail](invariants.md#every-guard-must-be-shown-to-fail).

## A new differ

A differ turns two consecutive values of a facet into domain events. Its
tests live beside it in `internal/synth/`; `internal/synth/dms_test.go` is
the safest file to copy. Cover:

- **First poll (no previous state).** The differ's first `Diff` call, with
  no prior snapshot, produces the expected initial events (often none, or a
  single status report — differs vary; match your differ's documented
  first-poll behavior).
- **No change between polls.** Feeding the same facet value twice emits
  nothing on the second call.
- **Each axis changing independently.** If the facet has more than one
  field that can change, prove that changing ONE axis fires only that
  axis's event — an exact event-count assertion, not just "the event I
  expected is present." `TestDMSAxesChangeIndependently`
  (`internal/synth/dms_test.go`) is the reference shape: it moves the
  control-mode axis alone and asserts exactly one event, then the
  display-state axis alone and asserts exactly one event, so the other
  axis's event is proven absent by the count rather than merely unmentioned.

  Two neighbouring tests are named similarly and are a *different* shape —
  useful, but not what this row asks for.
  `TestCCTVDiffer_AxesAreIndependent` (`internal/synth/cctv_test.go`) moves
  both axes in a single poll and asserts a total of 2 events;
  `TestZoneIncidentDiffer_AxesAreIndependent`
  (`internal/synth/perception_test.go`) mutates three incidents at once and
  asserts 1 detected / 1 updated / 1 cleared. Both prove
  multi-axis simultaneity — that concurrent changes each produce their own
  event and don't swallow one another — which is worth having, and neither
  proves that an *isolated* change leaves the other axis silent. A new
  differ ideally has both; if you write only one, write the isolated-axis
  shape.
- **Failed read emits nothing** — the absence-of-evidence rule. See
  [`invariants.md`](invariants.md#absence-of-evidence-is-never-a-state-change)
  for the rule and its enforcement mechanism.
- **DeviceKind stamped onto every event.** Assert the emitted events carry
  the snapshot's `DeviceKind`, e.g. `TestEngineCopiesDeviceKindOntoEvents`
  (`internal/synth/signal_test.go`), `TestDMSEventsCarryDeviceKind`
  (`internal/synth/dms_test.go`).

**Shapes to copy.** `internal/synth/dms_test.go` or
`TestSignalAxesChangeIndependently` (`internal/synth/signal_test.go`) for
the axis matrix — not `TestTransitionsEmitChangeEvents` beside it, which
moves three axes at once and only checks presence. For the failed-read
case, `internal/synth/failed_read_test.go`: drive a real `Engine` through
good poll → failed poll → recovery, and pick a recovery that would emit
something different if the engine had forgotten its state. A test that
only checks the failed poll emitted nothing cannot tell a correct engine
from one that wiped its memory.

## A new adapter

- **A golden read test per facet.** A fixed input produces an exact,
  asserted `model.Snapshot`, one test per facet the adapter produces — the
  shape of `TestASCReadGolden` and
  `TestASCDetectorGoldenAndOccupancyConversion`
  (`internal/vendors/ntcip/asc_test.go`), or `TestDMSReadGolden`
  (`internal/vendors/ntcip/dms_test.go`).
- **A facet-read failure produces `model.FacetError`, not a zero value.**
  An unanswered or failed read of one facet must appear in
  `Snapshot.Errors`, never silently become an empty or default value.
  `TestASCUnansweredAlarmIsFaultSetFacetError` and
  `TestASCDetectorTableGetFailureIsFacetError` are the pattern. Also prove
  facets fail independently of one another
  (`TestASCFacetsFailIndependently`, `TestDMSEnvGetFailureStillReturnsStatus`).
- **A malformed `connection` block is rejected at build time**, before
  anything is dialled: unknown keys, wrong-typed values, missing required
  values (`TestParseSNMPBlockRejectsUnrecognized`,
  `TestParseSNMPBlockRejectsUnknownConnectionKey`,
  `internal/vendors/ntcip/register_test.go`).
- **`Descriptor()` capability bits are correct.** `Caps` must match what the
  adapter implements — `CapState` only if it is a `StateReader`
  (`sdk/adapter/adapter.go`; `TestDMSDescriptorCaps`).
- **Recorded fixtures, not hand-typed ones** — see [no fixtures, no
  merge](invariants.md#no-fixtures-no-merge). Each fixture's comment says
  where its values came from: which device, firmware and date.
  `healthyDMSInts` in `internal/vendors/ntcip/dms_test.go` is the model.
  **Known gap, not a precedent:** `ntcip-asc`'s `healthyFixture` is a
  hand-typed `map[string]int64`, and its alarm-bitmap table has never been
  checked against a physical controller. Copy `ntcip-asc`'s test *shapes*,
  not its fixtures.

## A new ce-type mapping

- **A byte-exact golden.** Encode the domain event at a fixed timestamp
  and compare against recorded bytes: add a case to `goldenCases` in
  `internal/wire/openits/golden_test.go` (`TestGoldens`).
  `TestGoldensCoverEveryCEType` fails if any ce-type the emitter maps has
  no golden, so it catches a forgotten case.
- **A `ce-dataschema` entry.** `TestDataSchema_CoversEveryCEType` fails
  until the new ce-type has its schema-registry URL in `dataSchemaFor`
  (`internal/wire/openits/identities.go`).
- **The `unmapped` allowlist updated.** `catalog_test.go` compares the
  emitter's ce-types with the pinned openits-models release in both
  directions: `TestEmittedCETypesAreInPinnedCatalog` fails on a ce-type the
  catalog does not declare, and `TestUnmappedAllowlistIsCurrent` fails
  until you delete the newly mapped ce-type's `unmapped` line. See
  [the golden row](invariants.md#every-mapped-ce-type-has-a-byte-exact-golden).
- **Encoder tests for anything the golden cannot show** — a declined
  value, a routing choice per device kind — in the shape of
  `internal/wire/openits/emitter_test.go`.

## A new collector-owned event

A new `openits-collector.*` ce-type, or a new field on one, must be
documented in [`asyncapi.yaml`](../../asyncapi.yaml) in the same PR:
`TestAsyncAPICoversEveryEmittedType` and
`TestAsyncAPIDocumentsNothingUnemittable`
(`internal/wire/health/conformance_test.go`) compare the file with the
bytes the health emitter produces, and
`TestAsyncAPIAddressesMatchRenderedSubjects` checks its address. See
[the asyncapi row](invariants.md#asyncapiyaml-documents-exactly-the-collector-owned-ce-types).

## Contribution types not covered above

Wire-emitter version bumps and configuration changes have their own
workflows and validation surfaces documented elsewhere — see
[`docs/how-to/adopt-an-openits-models-release.md`](../how-to/adopt-an-openits-models-release.md)
(with [`map-an-event-to-the-wire.md`](../how-to/map-an-event-to-the-wire.md)
for a single mapping, and `.claude/skills/wire-emitter/` for the checklist
form) and [`docs/reference/configuration.md`](configuration.md)
respectively.
