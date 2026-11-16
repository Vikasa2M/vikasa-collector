# Lab gate validation

One bounded run of a gate-only collector, through a loopback relay, against
a lab cabinet. The raw capture, the relay, and the collector config stay
outside this repository. This note records what that run established.

## What was run

The collector binary was built from source revision
`aadf7638e8243c1257966842c2117aaa6e479ab9` with Go 1.26.11. Its sha256 is
`3642854a2984adc7728f698545e1092674adda64f13813e425be9be95bc6924f`. The
module pin was `github.com/Vikasa2M/openits-models v0.9.0`, with no
`replace` directive. The binary's embedded revision matched that commit
and was not dirty.

The collector did not dial the cabinet. It dialed a relay listening on
loopback. The relay held the one upstream session and forwarded the
collector's reads. The relay is a lab harness, not part of this repository,
and it is not a production component.

The configured block was the fourteen gate words for G01–G14. The run
completed six polls on one upstream connection: eighteen read requests,
three per poll, and no seventh poll. The courtesy exchange at shutdown
completed, and the collector then closed. A baseline for G01–G14 was
persisted. The catalog stream for that run contained no
`gate-status-changed` event. No gate transition was observed.

PLC status, watchdog, and cabinet registers were outside that block and
were not read.

Earlier harness runs the same day are separate results. One stopped before
a poll, one never dialed the cabinet, and one completed a single poll and
then closed. None of those is the run above.

## What that run does not show

The run did not deliver a gate transition, a fault, or a clear through
JetStream. A quiet baseline produces no `gate-status-changed`, so an empty
catalog stream is not evidence that the durable path works. That path is
covered by `TestACSDurableGateDelivery` on scripted words and an isolated
localhost broker, not by this cabinet.

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
drop those words.

## Synthetic coverage

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
