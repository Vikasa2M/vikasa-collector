# Lab gate validation

One bounded run of the configurable `cameleon-acs` adapter, through a
loopback relay, against the ACS3 cabinet. The raw capture, the relay, and
the collector config stay outside this repository. The local branch name
is an older review label and is not the cabinet in this run. This note
records what that run established.

## What was run

The collector binary was built from source revision
`aadf7638e8243c1257966842c2117aaa6e479ab9` with Go 1.26.11. Its sha256 is
`3642854a2984adc7728f698545e1092674adda64f13813e425be9be95bc6924f`. The
module pin was `github.com/Vikasa2M/openits-models v0.9.0`, with no
`replace` directive. Adopting that release included the emitter changes
that release required. The binary's embedded revision matched that commit
and was not dirty.

The collector did not dial the cabinet. It dialed a relay listening on
loopback. The relay held the one upstream session and forwarded the
collector's reads. The relay is a lab harness, not part of this repository,
and it is not a production component.

ACS3's configured block was the fourteen gate words G01–G14. The run
completed six polls on one upstream connection: eighteen read requests,
three per poll, and no seventh poll. The courtesy exchange at shutdown
completed, and the collector then closed. A baseline for G01–G14 was
persisted. No gate transition or fault was observed. Other program
exports are not this run, and agreement with an export is not fleet-wide
hardware validation. Each controller needs its own connection block.

PLC status, watchdog, and cabinet registers were outside that block and
were not read.

Earlier harness runs the same day are separate results. One stopped before
a poll, one never dialed the cabinet, and one completed a single poll and
then closed. None of those is the run above.

## What that run does not show

Live gate-event delivery was not exercised. Durable gate-event delivery
was verified separately using synthetic device input.

The watchdog was not observed. `plc/not-scanning` is a controller
condition, timed on the monotonic clock, and it is not a communication
fault. `ce-time` and `occurred_at` are UTC. Those are different clocks.
See [`configuration.md`](../reference/configuration.md).

Lock bits, gate subtype, and physical position were not independently
validated. A decoded position of unknown is not a measured position, and
kind `unknown` is the configured kind, not a field the cabinet reported.

The DSW span is whatever the operator configures. This run's span was the
gate block only. A wider contiguous span would read the words inside it,
including username words if the span covers them. The collector does not
drop those words. Lane, TSC, and sign objects are not decoded as gates.

## Synthetic coverage

`TestACSLayoutProfiles`, `TestACSRelocatedBlockIsNotPinnedTo101`,
`TestACSNonconsecutiveGatesStayInsideConfiguredSpan`,
`TestACSTwoControllersKeepIndependentG01`, and
`TestACSGenericKindNeedsNoWarningOrBarrier` drive the config loader and
the ACS reader with scripted words. The profiles are neutral 10-, 14-,
and 24-gate layouts, a block that does not start at 101, and two device
ids that both use G01. Those tests use the current decoder contract.
They do not validate packed status bits on every PLC program.

`TestACSDurableGateDelivery` uses the ACS reader, differ, emitter, and
publisher. It fetches from a named durable consumer, decodes the stored
protobuf, and acknowledges it. A redelivery of the same `ce-id` is not
counted as a second event. The cases are: no gate event on the first
snapshot, none while the snapshot is stable, one position change, a
command-refused raise and clear with
`openits-vendor-cameleon-reversible-lane-types:cameleon-gate-command-refused`,
gate component `G-1`, and the raise's `first_observed` kept on the clear,
no clear invented by a failed read or by a restart while the fault still
stands, and a manual non-interlocked mode change with interlock bypass
set. Mode and bypass semantics for the other mode pairs remain the
in-process tests named in that test. The checks named in `acsHarnessChecks`
ran on the stored events. `TestCEDataSchema_PayloadMatchesSnapshot` is the
payload-schema check. The catalog allowlist is not that check, and neither
is a YANG state comparison.
